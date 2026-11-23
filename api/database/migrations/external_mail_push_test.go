package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExternalMailPushUpDown rolls the migration back over a subscription and a sync run and applies
// it again: the result is the golden schema, and existing runs read as polls.
func TestExternalMailPushUpDown(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'gmail', 'owner@gmail.example', 901, now(), now(), now())`)
	exec(`INSERT INTO external_mail_subscriptions (connection_id, provider, lookup_key, status, expires_at, renew_after, created_at, updated_at)
		VALUES (1, 'gmail', 'owner@gmail.example', 'active', now() + interval '7 days', now(), now(), now())`)

	require.NoError(t, (&addExternalMailReleaseOwner{}).Down(db))
	require.NoError(t, (&addExternalMailProviderReleases{}).Down(db))
	require.NoError(t, (&addExternalMailPush{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "external_mail_subscriptions")
	assert.NotContains(t, rolledBack, "triggered_by")
	exec(`INSERT INTO external_mail_sync_runs (connection_id, started_at, finished_at, outcome) VALUES (1, now(), now(), 'ok')`)

	require.NoError(t, (&addExternalMailPush{}).Up(db))
	require.NoError(t, (&addExternalMailProviderReleases{}).Up(db))
	require.NoError(t, (&addExternalMailReleaseOwner{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
	var trigger string
	require.NoError(t, db.Raw(`SELECT triggered_by FROM external_mail_sync_runs WHERE connection_id = 1`).Scan(&trigger).Error)
	assert.Equal(t, "poll", trigger, "an existing run reads as a poll")
}

// TestExternalMailPushConstraints: one subscription per connection, known statuses and triggers only,
// an active row has a key and an expiry, a stopped row keeps no key, secret, or pending hint, a secret
// is a SHA-256 hex digest, and deleting the connection deletes its subscription.
func TestExternalMailPushConstraints(t *testing.T) {
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@outlook.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'outlook', 'owner@outlook.example', 901, now(), now(), now())`)
	insert := `INSERT INTO external_mail_subscriptions (connection_id, provider, lookup_key, secret_hash, status, expires_at, renew_after, pending_at, created_at, updated_at)
		VALUES (?, 'outlook', ?, ?, ?, ?::timestamptz, now(), ?::timestamptz, now(), now())`
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for name, arguments := range map[string][]any{
		"unknown status":         {1, "sub-1", digest, "paused", "2030-01-01T00:00:00Z", nil},
		"active without key":     {1, "", digest, "active", "2030-01-01T00:00:00Z", nil},
		"active without expiry":  {1, "sub-1", digest, "active", nil, nil},
		"stopped with key":       {1, "sub-1", "", "stopped", nil, nil},
		"stopped with secret":    {1, "", digest, "stopped", nil, nil},
		"stopped with a hint":    {1, "", "", "stopped", nil, "2030-01-01T00:00:00Z"},
		"secret not a digest":    {1, "sub-1", "client-state", "active", "2030-01-01T00:00:00Z", nil},
		"unknown connection":     {99, "sub-1", digest, "active", "2030-01-01T00:00:00Z", nil},
		"uppercase digest value": {1, "sub-1", "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF", "active", "2030-01-01T00:00:00Z", nil},
	} {
		assert.Error(t, db.Exec(insert, arguments...).Error, name)
	}
	exec(insert, 1, "sub-1", digest, "active", "2030-01-01T00:00:00Z", nil)
	assert.Error(t, db.Exec(insert, 1, "sub-2", digest, "active", "2030-01-01T00:00:00Z", nil).Error, "a second subscription of one connection")
	assert.Error(t, db.Exec(`INSERT INTO external_mail_sync_runs (connection_id, started_at, finished_at, outcome, triggered_by) VALUES (1, now(), now(), 'ok', 'webhook')`).Error,
		"an unknown trigger")
	exec(`INSERT INTO external_mail_sync_runs (connection_id, started_at, finished_at, outcome, triggered_by) VALUES (1, now(), now(), 'ok', 'push')`)

	exec(`DELETE FROM external_mail_sync_runs`)
	exec(`DELETE FROM external_mail_connections WHERE id = 1`)
	var left int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM external_mail_subscriptions`).Scan(&left).Error)
	assert.Zero(t, left, "deleting a connection deletes its subscription")
}
