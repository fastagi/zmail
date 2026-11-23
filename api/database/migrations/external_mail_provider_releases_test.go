package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const releaseInsert = `INSERT INTO external_mail_provider_releases (organization_id, connection_id, provider, account_address, tenant_id,
	  encrypted_grant, stop_push, attempts, next_attempt_at, expires_at, created_at)
	VALUES (?, ?, 'gmail', 'owner@gmail.example', ?, ?, ?, ?, now(), now() + ?::interval, now())`

// TestExternalMailProviderReleasesUpDown rolls the migration back over a pending release and applies
// it again: the result is the golden schema.
func TestExternalMailProviderReleasesUpDown(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)
	exec(releaseInsert, 10, 1, nil, "sealed", false, 0, "1 day")

	require.NoError(t, (&addMailboxPendingOwner{}).Down(db))
	require.NoError(t, (&addExternalMailReleaseOwner{}).Down(db))
	require.NoError(t, (&addExternalMailProviderReleases{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "external_mail_provider_releases")
	require.NoError(t, (&addExternalMailProviderReleases{}).Up(db))
	require.NoError(t, (&addExternalMailReleaseOwner{}).Up(db))
	require.NoError(t, (&addMailboxPendingOwner{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestExternalMailProviderReleasesConstraints: a release has something to do, a deadline after its
// creation, no grant for a tenant mailbox, and a known organization; it outlives its connection and
// goes with its organization.
func TestExternalMailProviderReleasesConstraints(t *testing.T) {
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, status, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'gmail', 'owner@gmail.example', 901, 'revoked', now(), now(), now())`)
	for name, arguments := range map[string][]any{
		"nothing to do":        {10, 1, nil, "", false, 0, "1 day"},
		"deadline passed":      {10, 1, nil, "sealed", false, 0, "-1 minute"},
		"negative attempts":    {10, 1, nil, "sealed", false, -1, "1 day"},
		"grant of a tenant":    {10, 1, 5, "sealed", true, 0, "1 day"},
		"unknown organization": {99, 1, nil, "sealed", false, 0, "1 day"},
	} {
		assert.Error(t, db.Exec(releaseInsert, arguments...).Error, name)
	}
	exec(releaseInsert, 10, 1, nil, "sealed", false, 0, "1 day")
	exec(`DELETE FROM external_mail_connections WHERE id = 1`)
	var left int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM external_mail_provider_releases`).Scan(&left).Error)
	assert.EqualValues(t, 1, left, "a release outlives the purge of its connection")
	exec(`DELETE FROM mail_messages`)
	exec(`DELETE FROM mailboxes`)
	exec(`DELETE FROM organizations WHERE id = 10`)
	require.NoError(t, db.Raw(`SELECT count(*) FROM external_mail_provider_releases`).Scan(&left).Error)
	assert.Zero(t, left, "a release goes with its organization")
}
