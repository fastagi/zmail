package feature

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/infra/config"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// An organization with several domains has one default: the oldest verified domain until an owner
// chooses another, and the oldest again once the chosen one is removed. Both domain lists say which
// one it is, and the connected list counts each domain's mailboxes.
func TestMailDomainDefaultHTTPContract(t *testing.T) {
	var database config.DatabaseConfig
	engine := setupApp(t, func(cfg *config.Config) {
		cfg.Mailbox = config.MailboxConfig{
			Domain:                  "zmail.localhost",
			Provider:                config.MailboxProviderCloudflare,
			CloudflareAccountID:     "feature-account",
			CloudflareAPIToken:      "feature-token",
			CloudflareEmailWorker:   "zmail-email-worker",
			CustomDomainsEnabled:    true,
			RequestTimeout:          config.DefaultMailboxRequestTimeout,
			DailySendLimit:          config.DefaultMailboxDailySendLimit,
			NewOrganizationPeriod:   config.DefaultMailboxNewOrganizationPeriod,
			MaxZonesPerOrganization: config.DefaultMailboxMaxZonesPerOrganization,
			MaxPerOrganization:      config.DefaultMailboxMaxPerOrganization,
		}
		database = cfg.Database
	}, config.StarterOrganization, config.StarterMailbox)
	tc := testplatform.NewTestCase(t, engine)
	owner := registerOrganizationTestUser(t, tc, "default-owner", "default-owner@example.com")
	created := tc.Post("/v1/organizations").
		WithToken(owner.Token).
		WithJSON(map[string]any{"name": "Default Org", "slug": "default-org"}).
		Call().
		AssertCreated().
		JSON()["data"].(map[string]interface{})
	organizationID := fmt.Sprintf("%.0f", created["id"].(float64))

	// Send-only domains need no routing rule, so mailboxes on them need no provider call.
	db := openFeatureDatabase(t, database)
	seed := func(name, status, age string) {
		require.NoError(t, db.Exec(`INSERT INTO mail_domains (organization_id, name, zone_id, zone_name, status, capability,
			created_by, verified_at, created_at, updated_at)
			SELECT id, ?, 'zone-default', 'default.example', ?, 'send_only', created_by, now(), now() - ?::interval, now()
			FROM organizations WHERE id = ?`, name, status, age, organizationID).Error)
	}
	seed("old.default.example", "verified", "3 hours")
	seed("new.default.example", "verified", "1 hour")
	seed("wait.default.example", "pending", "4 hours")

	call := func(method, path string, body any) *testplatform.TestResponse {
		fresh := testplatform.NewTestCase(t, engine)
		request := map[string]func(string) *testplatform.TestCase{
			http.MethodGet: fresh.Get, http.MethodPost: fresh.Post, http.MethodPut: fresh.Put, http.MethodDelete: fresh.Delete,
		}[method](path).
			WithToken(owner.Token).
			WithHeader("Organization-Id", organizationID)
		if body != nil {
			request = request.WithJSON(body)
		}
		return request.Call()
	}
	connected := func() map[string]map[string]interface{} {
		byName := map[string]map[string]interface{}{}
		for _, raw := range call(http.MethodGet, "/v1/mail-domains", nil).AssertOk().JSON()["data"].([]interface{}) {
			item := raw.(map[string]interface{})
			byName[item["name"].(string)] = item
		}
		return byName
	}
	defaults := func() []string {
		var names []string
		for name, item := range connected() {
			if item["default"] == true {
				names = append(names, name)
			}
		}
		return names
	}
	idOf := func(name string) string {
		return fmt.Sprintf("%.0f", connected()[name]["id"].(float64))
	}

	require.Equal(t, []string{"old.default.example"}, defaults(), "the oldest verified domain")

	call(http.MethodPut, "/v1/mail-domains/"+idOf("wait.default.example")+"/default", nil).
		AssertStatus(http.StatusUnprocessableEntity).
		AssertJSONPath("error_code", "MAILBOX.DOMAIN_NOT_VERIFIED")
	call(http.MethodPut, "/v1/mail-domains/999999/default", nil).
		AssertNotFound().
		AssertJSONPath("error_code", "MAILBOX.DOMAIN_NOT_FOUND")

	call(http.MethodPut, "/v1/mail-domains/"+idOf("new.default.example")+"/default", nil).
		AssertOk().
		AssertJSONPath("data.name", "new.default.example").
		AssertJSONPath("data.default", true)
	require.Equal(t, []string{"new.default.example"}, defaults())

	var mailboxDefaults []string
	for _, raw := range call(http.MethodGet, "/v1/mailbox-domains", nil).AssertOk().JSON()["data"].([]interface{}) {
		item := raw.(map[string]interface{})
		if item["default"] == true {
			mailboxDefaults = append(mailboxDefaults, item["domain"].(string))
		}
	}
	require.Equal(t, []string{"new.default.example"}, mailboxDefaults, "new mailboxes preselect it")

	call(http.MethodPost, "/v1/mailboxes", map[string]any{
		"local_part": "sales", "domain": "old.default.example", "type": "personal", "owner_user_id": owner.ID,
	}).AssertCreated()
	domains := connected()
	require.Equal(t, float64(1), domains["old.default.example"]["mailbox_count"])
	require.Equal(t, float64(0), domains["new.default.example"]["mailbox_count"])

	// Removing the chosen default falls back to the oldest verified domain.
	call(http.MethodDelete, "/v1/mail-domains/"+idOf("new.default.example"), nil).AssertNoContent()
	require.Equal(t, []string{"old.default.example"}, defaults())
}
