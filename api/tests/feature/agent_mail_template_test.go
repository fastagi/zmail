package feature

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/infra/config"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// An AI tool creates an organization template with its owner's API key; the key acts with the
// owner's current membership and feature grants, exactly as the owner's session would.
func TestAgentMailTemplateHTTPContract(t *testing.T) {
	invitationTokens := make(chan string, 4)
	previousTransport := http.DefaultTransport
	http.DefaultTransport = &organizationEmailTransport{invitationTokens: invitationTokens}
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	engine := setupApp(t, func(cfg *config.Config) {
		cfg.Email.From = "Zmail <noreply@example.com>"
		cfg.Email.ResendAPIKey = "feature-test-key"
		cfg.Email.RequestTimeout = time.Second
		cfg.Mailbox = config.MailboxConfig{
			RequestTimeout:          config.DefaultMailboxRequestTimeout,
			DailySendLimit:          config.DefaultMailboxDailySendLimit,
			NewOrganizationPeriod:   config.DefaultMailboxNewOrganizationPeriod,
			MaxZonesPerOrganization: config.DefaultMailboxMaxZonesPerOrganization,
			MaxPerOrganization:      config.DefaultMailboxMaxPerOrganization,
		}
	}, config.StarterOrganization, config.StarterMailbox)
	tc := testplatform.NewTestCase(t, engine)
	owner := registerOrganizationTestUser(t, tc, "agent-tpl-owner", "agent-tpl-owner@example.com")
	member := registerOrganizationTestUser(t, tc, "agent-tpl-member", "agent-tpl-member@example.com")
	outsider := registerOrganizationTestUser(t, tc, "agent-tpl-outsider", "agent-tpl-outsider@example.com")

	created := tc.Post("/v1/organizations").
		WithToken(owner.Token).
		WithJSON(map[string]any{"name": "Agent Template Org", "slug": "agent-template-org"}).
		Call().
		AssertCreated().
		JSON()["data"].(map[string]interface{})
	organizationID := fmt.Sprintf("%.0f", created["id"].(float64))
	inviteAndAcceptOrganizationMember(t, tc, owner.Token, member, organizationID, "member", invitationTokens)

	createKey := func(token string, scopes ...string) string {
		data := tc.Post("/v1/api-keys").
			WithToken(token).
			WithJSON(map[string]any{"name": "agent tool", "scopes": scopes}).
			Call().
			AssertCreated().
			JSON()["data"].(map[string]interface{})
		return data["plaintext_key"].(string)
	}
	template := func(name string) map[string]any {
		return map[string]any{
			"name": name, "subject": "Welcome, {{name}}", "text": "Hello {{name}}, how can we help?",
			"sample_variables": map[string]string{"name": "Alex"},
		}
	}
	createTemplate := func(key, name string) *testplatform.TestResponse {
		return tc.Post("/v1/agent-mail-templates").
			WithAPIKey(key).
			WithHeader("Organization-Id", organizationID).
			WithJSON(template(name)).
			Call()
	}

	ownerKey := createKey(owner.Token, "templates:create")
	createTemplate(ownerKey, "Welcome follow-up").
		AssertCreated().
		AssertJSONPath("data.name", "Welcome follow-up").
		AssertJSONPath("data.subject", "Welcome, {{name}}")
	createTemplate(ownerKey, "Welcome follow-up").
		AssertStatus(http.StatusConflict).
		AssertJSONPath("error_code", "MAILBOX.TEMPLATE_NAME_TAKEN")

	listed := tc.Get("/v1/mail-templates").
		WithToken(owner.Token).
		WithHeader("Organization-Id", organizationID).
		Call().
		AssertOk().
		JSON()["data"].([]interface{})
	require.Len(t, listed, 1)
	require.Equal(t, "Welcome follow-up", listed[0].(map[string]interface{})["name"])
	// Creating a template sends nothing.
	tc.Get("/v1/mail-messages").
		WithToken(owner.Token).
		WithHeader("Organization-Id", organizationID).
		Call().
		AssertOk().
		AssertJSONPath("meta.total", float64(0))

	createTemplate(createKey(owner.Token, "mail:send", "mail:read"), "Scope check").
		AssertForbidden().
		AssertJSONPath("error_code", "PERMISSION.DENIED")

	createTemplate(createKey(outsider.Token, "templates:create"), "Outsider").
		AssertNotFound().
		AssertJSONPath("error_code", "ORGANIZATION.NOT_FOUND")

	// A new member has templates but not API keys, so their key is refused until an owner grants
	// API keys, and again once the templates grant is withdrawn.
	memberKey := createKey(member.Token, "templates:create")
	createTemplate(memberKey, "Member draft").
		AssertForbidden().
		AssertJSONPath("error_code", "ORGANIZATION.FEATURE_NOT_GRANTED")
	membershipID := organizationMembershipID(t, tc, owner.Token, organizationID, member.ID)
	setMemberFeatures := func(features map[string]bool) {
		tc.Patch(fmt.Sprintf("/v1/organizations/%s/members/%d/features", organizationID, membershipID)).
			WithToken(owner.Token).
			WithJSON(features).
			Call().
			AssertOk()
	}
	setMemberFeatures(map[string]bool{"api_keys": true})
	createTemplate(memberKey, "Member draft").AssertCreated()
	setMemberFeatures(map[string]bool{"templates": false})
	createTemplate(memberKey, "Member second draft").
		AssertForbidden().
		AssertJSONPath("error_code", "ORGANIZATION.FEATURE_NOT_GRANTED")
	tc.Post("/v1/mail-templates").
		WithToken(member.Token).
		WithHeader("Organization-Id", organizationID).
		WithJSON(template("Member session draft")).
		Call().
		AssertForbidden().
		AssertJSONPath("error_code", "ORGANIZATION.FEATURE_NOT_GRANTED")
}

func organizationMembershipID(
	t *testing.T, tc *testplatform.TestCase, ownerToken, organizationID string, userID uint,
) uint {
	t.Helper()
	members := tc.Get("/v1/organizations/" + organizationID + "/members").
		WithToken(ownerToken).
		Call().
		AssertOk().
		JSON()["data"].([]interface{})
	for _, item := range members {
		entry := item.(map[string]interface{})
		if uint(entry["user_id"].(float64)) == userID {
			return uint(entry["id"].(float64))
		}
	}
	require.FailNow(t, "member not found", "user %d", userID)
	return 0
}
