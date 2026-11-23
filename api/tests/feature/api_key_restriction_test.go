package feature

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/infra/config"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// A key may be bound at creation to one organization and to some of its sending domains: a bound
// key acts there without Organization-Id and nowhere else, and a domain-restricted key sends only
// from its domains. Keys created without a restriction keep needing the header.
func TestAPIKeyRestrictionHTTPContract(t *testing.T) {
	engine := setupApp(t, func(cfg *config.Config) {
		cfg.Mailbox = config.MailboxConfig{
			Domain:                  "mail.restricted.test",
			Provider:                config.MailboxProviderLog,
			RequestTimeout:          config.DefaultMailboxRequestTimeout,
			DailySendLimit:          config.DefaultMailboxDailySendLimit,
			NewOrganizationPeriod:   config.DefaultMailboxNewOrganizationPeriod,
			MaxZonesPerOrganization: config.DefaultMailboxMaxZonesPerOrganization,
			MaxPerOrganization:      config.DefaultMailboxMaxPerOrganization,
		}
	}, config.StarterOrganization, config.StarterMailbox)
	tc := testplatform.NewTestCase(t, engine)
	owner := registerOrganizationTestUser(t, tc, "restricted-owner", "restricted-owner@example.com")
	outsider := registerOrganizationTestUser(t, tc, "restricted-outsider", "restricted-outsider@example.com")

	createOrganization := func(name, slug string) string {
		data := tc.Post("/v1/organizations").
			WithToken(owner.Token).
			WithJSON(map[string]any{"name": name, "slug": slug}).
			Call().
			AssertCreated().
			JSON()["data"].(map[string]interface{})
		return fmt.Sprintf("%.0f", data["id"].(float64))
	}
	organizationID := createOrganization("Restricted Org", "restricted-org")
	otherOrganizationID := createOrganization("Other Org", "restricted-other-org")
	var boundOrganization float64
	_, err := fmt.Sscan(organizationID, &boundOrganization)
	require.NoError(t, err)

	tc.Post("/v1/mailboxes").
		WithToken(owner.Token).
		WithHeader("Organization-Id", organizationID).
		WithJSON(map[string]any{"local_part": "billing", "type": "personal", "owner_user_id": owner.ID}).
		Call().
		AssertCreated()

	createKey := func(token string, body map[string]any) *testplatform.TestResponse {
		body["name"] = "backend"
		body["scopes"] = []string{"mail:send"}
		return testplatform.NewTestCase(t, engine).Post("/v1/api-keys").WithToken(token).WithJSON(body).Call()
	}
	plaintext := func(response *testplatform.TestResponse) string {
		return response.AssertCreated().JSON()["data"].(map[string]interface{})["plaintext_key"].(string)
	}
	send := func(key, organizationHeader, from string) *testplatform.TestResponse {
		// A fresh test case per send: headers set on a test case stick to its later requests.
		request := testplatform.NewTestCase(t, engine).Post("/v1/outbound-mail-messages").
			WithAPIKey(key).
			WithJSON(map[string]any{"from": from, "to": []string{"someone@example.com"}, "subject": "Receipt", "text": "Thanks."})
		if organizationHeader != "" {
			request = request.WithHeader("Organization-Id", organizationHeader)
		}
		return request.Call()
	}
	const from = "billing@mail.restricted.test"

	// Creation validates the binding.
	createKey(outsider.Token, map[string]any{"organization_id": boundOrganization}).
		AssertNotFound().
		AssertJSONPath("error_code", "ORGANIZATION.NOT_FOUND")
	createKey(owner.Token, map[string]any{"sending_domains": []string{"mail.restricted.test"}}).
		AssertStatus(http.StatusBadRequest).
		AssertJSONPath("error_code", "COMMON.INVALID_INPUT")
	createKey(owner.Token, map[string]any{"organization_id": boundOrganization, "sending_domains": []string{"elsewhere.example"}}).
		AssertStatus(http.StatusBadRequest).
		AssertJSONPath("error_code", "COMMON.INVALID_INPUT")

	// An unrestricted key still names its organization.
	openKey := plaintext(createKey(owner.Token, map[string]any{}))
	send(openKey, "", from).
		AssertStatus(http.StatusBadRequest).
		AssertJSONPath("error_code", "ORGANIZATION.CONTEXT_REQUIRED")
	send(openKey, organizationID, from).AssertCreated()

	// A bound key acts in its organization without the header and is refused in another.
	boundKey := plaintext(createKey(owner.Token, map[string]any{"organization_id": boundOrganization}))
	send(boundKey, "", from).AssertCreated()
	send(boundKey, organizationID, from).AssertCreated()
	send(boundKey, otherOrganizationID, from).
		AssertForbidden().
		AssertJSONPath("error_code", "API_KEY.ORGANIZATION_MISMATCH")

	// A domain-restricted key sends only from its domains.
	domainKey := plaintext(createKey(owner.Token, map[string]any{
		"organization_id": boundOrganization, "sending_domains": []string{"MAIL.restricted.test"},
	}))
	send(domainKey, "", "Billing@Mail.Restricted.Test").AssertCreated()
	send(domainKey, "", "billing@elsewhere.example").
		AssertForbidden().
		AssertJSONPath("error_code", "API_KEY.DOMAIN_NOT_ALLOWED")

	// The list shows each restriction, and nothing on unrestricted keys.
	keys := tc.Get("/v1/api-keys").WithToken(owner.Token).Call().AssertOk().JSON()["data"].([]interface{})
	require.Len(t, keys, 3)
	restricted := keys[0].(map[string]interface{})
	require.Equal(t, boundOrganization, restricted["organization_id"])
	require.Equal(t, []interface{}{"mail.restricted.test"}, restricted["sending_domains"])
	bound := keys[1].(map[string]interface{})
	require.Equal(t, boundOrganization, bound["organization_id"])
	require.NotContains(t, bound, "sending_domains")
	open := keys[2].(map[string]interface{})
	require.NotContains(t, open, "organization_id")
	require.NotContains(t, open, "sending_domains")

	// Exactly the four accepted sends were stored.
	tc.Get("/v1/mail-messages").
		WithToken(owner.Token).
		WithHeader("Organization-Id", organizationID).
		Call().
		AssertOk().
		AssertJSONPath("meta.total", float64(4))
}
