package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestWebPushUpDown rolls web push back over a subscription and a web_push delivery and applies it
// again: the result is the golden schema, rollback drops web_push deliveries instead of failing the
// channel check, and existing preferences read as push enabled.
func TestWebPushUpDown(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	migrated := dumpSchema(t, db)
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'owner', 'owner@example.com', 'x', now(), now())`)
	exec(`INSERT INTO notifications (id, user_id, idempotency_key, publication_hash, kind, title, body, created_at, updated_at)
		VALUES (1, 901, 'k', 'h', 'mail.received', 'New mail', 'Hello', now(), now())`)
	exec(`INSERT INTO notification_deliveries (notification_id, channel, status, available_at, created_at, updated_at)
		VALUES (1, 'in_app', 'delivered', now(), now(), now()), (1, 'web_push', 'pending', now(), now(), now())`)
	exec(`INSERT INTO notification_web_push_subscriptions (user_id, endpoint, endpoint_hash, p256dh, auth, device_label, created_at, updated_at)
		VALUES (901, 'https://fcm.googleapis.com/fcm/send/a', repeat('a', 64), 'p', 'a', 'Chrome on Mac', now(), now())`)

	require.NoError(t, (&addWebPush{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "notification_web_push_subscriptions")
	assert.NotContains(t, rolledBack, "web_push_enabled")
	var channels []string
	require.NoError(t, db.Raw(`SELECT channel FROM notification_deliveries ORDER BY channel`).Scan(&channels).Error)
	assert.Equal(t, []string{"in_app"}, channels)
	exec(`INSERT INTO notification_preferences (user_id, in_app_enabled, email_enabled, created_at, updated_at) VALUES (901, true, false, now(), now())`)

	require.NoError(t, (&addWebPush{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
	var enabled bool
	require.NoError(t, db.Raw(`SELECT web_push_enabled FROM notification_preferences WHERE user_id = 901`).Scan(&enabled).Error)
	assert.True(t, enabled)
}

// TestWebPushConstraints: one row per endpoint, the hash is a SHA-256 hex digest, keys are present,
// and deleting the user deletes their subscriptions.
func TestWebPushConstraints(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	require.NoError(t, db.Exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'owner', 'owner@example.com', 'x', now(), now())`).Error)
	insert := `INSERT INTO notification_web_push_subscriptions (user_id, endpoint, endpoint_hash, p256dh, auth, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, now(), now())`
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for name, arguments := range map[string][]any{
		"unknown user":       {99, "https://e/1", digest, "p", "a"},
		"empty endpoint":     {901, "", digest, "p", "a"},
		"hash not a digest":  {901, "https://e/1", "abc", "p", "a"},
		"missing p256dh key": {901, "https://e/1", digest, "", "a"},
		"missing auth key":   {901, "https://e/1", digest, "p", ""},
	} {
		assert.Error(t, db.Exec(insert, arguments...).Error, name)
	}
	require.NoError(t, db.Exec(insert, 901, "https://e/1", digest, "p", "a").Error)
	assert.Error(t, db.Exec(insert, 901, "https://e/1", digest, "p", "a").Error, "the same endpoint twice")
	assert.Error(t, db.Exec(`INSERT INTO notification_deliveries (notification_id, channel, status, available_at, created_at, updated_at)
		VALUES (1, 'sms', 'pending', now(), now(), now())`).Error, "an unknown channel")
	require.NoError(t, db.Exec(`DELETE FROM users WHERE id = 901`).Error)
	var count int
	require.NoError(t, db.Raw(`SELECT count(*) FROM notification_web_push_subscriptions`).Scan(&count).Error)
	assert.Zero(t, count)
}
