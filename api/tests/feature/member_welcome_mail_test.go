package feature

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/fastagi/zmail/api/internal/infra/config"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// Importing a roster of ten people welcomes each of them exactly once: ten welcome mails are
// queued, every one is findable in the transactional send log, and uploading the same roster
// again sends nothing new.
func TestRosterImportWelcomesEveryMemberOnce(t *testing.T) {
	tc, owner, organizationID, _ := setupWelcomeFeature(t)

	tc.Get("/v1/member-welcome-mail-setting").
		WithToken(owner.Token).
		WithHeader("Organization-Id", organizationID).
		Call().
		AssertOk().
		AssertJSONPath("data.enabled", true).
		AssertJSONPath("data.language", "zh-Hans").
		AssertJSONPath("data.customized", false)

	rows := make([]map[string]any, 0, 10)
	for n := 1; n <= 10; n++ {
		// Simulator addresses: the welcome mail is recorded as delivered without a provider call.
		rows = append(rows, map[string]any{
			"email": fmt.Sprintf("delivered+m%d@test.zmail.im", n), "name": fmt.Sprintf("Member %d", n),
			"role": "member", "mailbox": fmt.Sprintf("m%d", n),
		})
	}
	importRoster := func() map[string]any {
		return tc.Post("/v1/member-imports").
			WithToken(owner.Token).
			WithHeader("Organization-Id", organizationID).
			WithJSON(map[string]any{"dry_run": false, "domain": "welcome.example", "rows": rows}).
			Call().
			AssertOk().
			JSON()["data"].(map[string]interface{})
	}

	first := importRoster()
	require.Equal(t, float64(10), first["invited"], first)
	require.Equal(t, float64(10), first["welcome_mails_queued"], first)
	require.Equal(t, float64(0), first["welcome_mails_failed"], first)
	for _, raw := range first["rows"].([]interface{}) {
		row := raw.(map[string]interface{})
		require.Equal(t, "queued", row["welcome_mail_status"], row)
		require.Nil(t, row["welcome_mail_error_code"], row)
	}

	logEntries := func() []interface{} {
		return tc.Get("/v1/transactional-mail-log?template=member-welcome&per_page=50").
			WithToken(owner.Token).
			WithHeader("Organization-Id", organizationID).
			Call().
			AssertOk().
			JSON()["data"].([]interface{})
	}
	entries := logEntries()
	require.Len(t, entries, 10)
	keys := map[string]bool{}
	for _, raw := range entries {
		entry := raw.(map[string]interface{})
		require.Equal(t, "member-welcome", entry["template"])
		require.Equal(t, true, entry["simulated"], entry)
		keys[entry["idempotency_key"].(string)] = true
	}
	require.Len(t, keys, 10, "one idempotency key per member")
	// The import only queues; each welcome is sent off the request path.
	require.Eventually(t, func() bool {
		for _, raw := range logEntries() {
			if raw.(map[string]interface{})["status"] != "sent" {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond, "every welcome mail is sent")

	again := importRoster()
	require.Equal(t, float64(10), again["skipped"], again)
	require.Equal(t, float64(0), again["welcome_mails_queued"], again)
	for _, raw := range again["rows"].([]interface{}) {
		require.Equal(t, "already_sent", raw.(map[string]interface{})["welcome_mail_status"], raw)
	}
	require.Len(t, logEntries(), 10, "a second upload sends no second welcome")
}

// An owner sees each member mailbox's latest welcome mail and sends it again: a resend right after
// the first welcome is refused with a clear code and Retry-After, and a later one stores a new
// message under the next resend key while a roster upload still finds the mailbox welcomed.
func TestOwnerResendsAMemberWelcomeMail(t *testing.T) {
	tc, owner, organizationID, db := setupWelcomeFeature(t)
	rows := []map[string]any{{
		"email": "delivered+resend@test.zmail.im", "name": "Resend Member", "role": "member", "mailbox": "resend",
	}}
	importRoster := func() map[string]any {
		return tc.Post("/v1/member-imports").
			WithToken(owner.Token).
			WithHeader("Organization-Id", organizationID).
			WithJSON(map[string]any{"dry_run": false, "domain": "welcome.example", "rows": rows}).
			Call().
			AssertOk().
			JSON()["data"].(map[string]interface{})
	}
	first := importRoster()
	row := first["rows"].([]interface{})[0].(map[string]interface{})
	require.Equal(t, "queued", row["welcome_mail_status"], row)
	mailboxID := row["mailbox_id"].(float64)

	listWelcomes := func() []interface{} {
		return tc.Get("/v1/member-welcome-mails").
			WithToken(owner.Token).
			WithHeader("Organization-Id", organizationID).
			Call().
			AssertOk().
			JSON()["data"].([]interface{})
	}
	require.Eventually(t, func() bool {
		entries := listWelcomes()
		return len(entries) == 1 && entries[0].(map[string]interface{})["status"] == "sent"
	}, 10*time.Second, 50*time.Millisecond, "the first welcome is listed as sent")
	entry := listWelcomes()[0].(map[string]interface{})
	require.Equal(t, mailboxID, entry["mailbox_id"])
	require.Equal(t, "resend@welcome.example", entry["address"])
	require.Equal(t, true, entry["invitation_pending"])
	require.Equal(t, float64(0), entry["resend_count"])
	require.NotContains(t, entry, "recipient", "the list never carries a recipient address")

	resend := func() *testplatform.TestResponse {
		return tc.Post("/v1/member-welcome-mails").
			WithToken(owner.Token).
			WithHeader("Organization-Id", organizationID).
			WithJSON(map[string]any{"mailbox_id": mailboxID}).
			Call()
	}
	resend().
		AssertStatus(http.StatusTooManyRequests).
		AssertJSONPath("error_code", "MAILBOX.WELCOME_RESEND_LIMITED").
		AssertHeaderExists("Retry-After")

	// Age the first welcome well past the one-minute spacing, whatever the database clock says.
	require.NoError(t, db.Exec(`UPDATE member_welcome_mails SET created_at = created_at - interval '1 hour'
		WHERE mailbox_id = ?`, mailboxID).Error)
	resent := resend().
		AssertCreated().
		AssertJSONPath("data.mailbox_id", mailboxID).
		AssertJSONPath("data.resend_count", float64(1)).
		JSON()["data"].(map[string]interface{})
	require.Contains(t, []interface{}{"queued", "sent"}, resent["status"], resent)

	logEntries := tc.Get("/v1/transactional-mail-log?template=member-welcome&per_page=50").
		WithToken(owner.Token).
		WithHeader("Organization-Id", organizationID).
		Call().
		AssertOk().
		JSON()["data"].([]interface{})
	keys := map[string]bool{}
	for _, raw := range logEntries {
		keys[raw.(map[string]interface{})["idempotency_key"].(string)] = true
	}
	id := strconv.FormatFloat(mailboxID, 'f', 0, 64)
	require.Equal(t, map[string]bool{"member-welcome:" + id: true, "member-welcome:" + id + ":1": true}, keys)

	again := importRoster()
	require.Equal(t, "already_sent", again["rows"].([]interface{})[0].(map[string]interface{})["welcome_mail_status"])

	tc.Post("/v1/member-welcome-mails").
		WithToken(owner.Token).
		WithHeader("Organization-Id", organizationID).
		WithJSON(map[string]any{"mailbox_id": 999999}).
		Call().
		AssertStatus(http.StatusNotFound).
		AssertJSONPath("error_code", "MAILBOX.NOT_FOUND")
}

// setupWelcomeFeature starts the application with welcome mails configured and an organization
// whose verified, send-only domain welcome.example needs no routing rule.
func setupWelcomeFeature(t *testing.T) (*testplatform.TestCase, organizationTestUser, string, *gorm.DB) {
	t.Helper()
	invitationTokens := make(chan string, 64)
	previousTransport := http.DefaultTransport
	http.DefaultTransport = &organizationEmailTransport{invitationTokens: invitationTokens}
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	var database config.DatabaseConfig
	engine := setupApp(t, func(cfg *config.Config) {
		cfg.Email.From = "Zmail <noreply@example.com>"
		cfg.Email.ResendAPIKey = "feature-test-key"
		cfg.Email.RequestTimeout = time.Second
		cfg.Mailbox = config.MailboxConfig{
			Domain:                  "zmail.localhost",
			Provider:                config.MailboxProviderCloudflare,
			CloudflareAccountID:     "feature-account",
			CloudflareAPIToken:      "feature-token",
			CloudflareEmailWorker:   "zmail-email-worker",
			CustomDomainsEnabled:    true,
			PublicURL:               "https://app.zmail.test",
			SecretEncryptionKey:     "feature-welcome-encryption-key-0123456789",
			RequestTimeout:          config.DefaultMailboxRequestTimeout,
			DailySendLimit:          config.DefaultMailboxDailySendLimit,
			NewOrganizationPeriod:   config.DefaultMailboxNewOrganizationPeriod,
			MaxZonesPerOrganization: config.DefaultMailboxMaxZonesPerOrganization,
			MaxPerOrganization:      config.DefaultMailboxMaxPerOrganization,
		}
		database = cfg.Database
	}, config.StarterOrganization, config.StarterMailbox)
	tc := testplatform.NewTestCase(t, engine)
	owner := registerOrganizationTestUser(t, tc, "welcome-owner", "welcome-owner@example.com")
	created := tc.Post("/v1/organizations").
		WithToken(owner.Token).
		WithJSON(map[string]any{"name": "Welcome Org", "slug": "welcome-org"}).
		Call().
		AssertCreated().
		JSON()["data"].(map[string]interface{})
	organizationID := fmt.Sprintf("%.0f", created["id"].(float64))

	// A verified, send-only organization domain: mailboxes on it need no routing rule.
	db := openFeatureDatabase(t, database)
	require.NoError(t, db.Exec(`INSERT INTO mail_domains (organization_id, name, zone_id, zone_name, status, capability,
		created_by, verified_at, created_at, updated_at)
		SELECT id, 'welcome.example', 'zone-welcome', 'welcome.example', 'verified', 'send_only', created_by, now(), now(), now()
		FROM organizations WHERE id = ?`, organizationID).Error)

	return tc, owner, organizationID, db
}

// openFeatureDatabase opens the isolated database the feature application runs on, to seed what
// no public route creates in a test, such as a verified domain.
func openFeatureDatabase(t *testing.T, database config.DatabaseConfig) *gorm.DB {
	t.Helper()
	dsn := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(database.Username, database.Password),
		Host:     database.Host + ":" + strconv.Itoa(database.Port),
		Path:     "/" + database.Name,
		RawQuery: url.Values{"sslmode": []string{database.SSLMode}}.Encode(),
	}).String()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}
