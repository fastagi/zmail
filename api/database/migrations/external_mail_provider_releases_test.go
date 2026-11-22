package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExternalMailProviderReleasesUpDown rolls the migration back over a pending release and applies
// it again: the result is the golden schema.
func TestExternalMailProviderReleasesUpDown(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, status, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'gmail', 'owner@gmail.example', 901, 'revoked', now(), now(), now())`)
	exec(`INSERT INTO external_mail_provider_releases (connection_id, organization_id, provider, encrypted_grant, next_attempt_at, expires_at, created_at)
		VALUES (1, 10, 'gmail', 'sealed', now(), now() + interval '1 day', now())`)

	require.NoError(t, (&addExternalMailProviderReleases{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "external_mail_provider_releases")
	require.NoError(t, (&addExternalMailProviderReleases{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestExternalMailProviderReleasesConstraints: a release has something to do, a deadline after its
// creation, a known connection, and goes with its connection.
func TestExternalMailProviderReleasesConstraints(t *testing.T) {
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, status, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'gmail', 'owner@gmail.example', 901, 'revoked', now(), now(), now())`)
	insert := `INSERT INTO external_mail_provider_releases (connection_id, organization_id, provider, encrypted_grant, stop_push, attempts, next_attempt_at, expires_at, created_at)
		VALUES (?, 10, 'gmail', ?, ?, ?, now(), now() + ?::interval, now())`
	for name, arguments := range map[string][]any{
		"nothing to do":      {1, "", false, 0, "1 day"},
		"deadline passed":    {1, "sealed", false, 0, "-1 minute"},
		"negative attempts":  {1, "sealed", false, -1, "1 day"},
		"unknown connection": {99, "sealed", false, 0, "1 day"},
	} {
		assert.Error(t, db.Exec(insert, arguments...).Error, name)
	}
	exec(insert, 1, "", true, 0, "1 day")
	exec(`DELETE FROM external_mail_connections WHERE id = 1`)
	var left int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM external_mail_provider_releases`).Scan(&left).Error)
	assert.Zero(t, left, "a release goes with its connection")
}
