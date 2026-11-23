package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExternalMailReleaseOwnerUpDown rolls the owner of provider releases back over pending releases
// and applies it again: existing rows are backfilled from their connection when it still exists, the
// others stay unknown, and the result is the golden schema.
func TestExternalMailReleaseOwnerUpDown(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)
	require.NoError(t, (&addMailboxPendingOwner{}).Down(db))
	require.NoError(t, (&addExternalMailReleaseOwner{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "owner_scope")

	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (902, 'external-other', 'external-other@example.com', 'x', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'personal', 'connected', 901, now(), now()),
		       (3, 10, 901, 'team@gmail.example', 'shared', 'connected', NULL, now(), now()),
		       (4, 10, 901, 'gone@gmail.example', 'personal', 'connected', NULL, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, shared, status, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'gmail', 'owner@gmail.example', 901, false, 'revoked', now(), now(), now()),
		       (2, 10, 3, 'gmail', 'team@gmail.example', NULL, true, 'revoked', now(), now(), now()),
		       (3, 10, 4, 'gmail', 'gone@gmail.example', NULL, false, 'revoked', now(), now(), now())`)
	// Releases of a personal, a shared, a deleted member's, and a purged connection.
	for connection := 1; connection <= 4; connection++ {
		exec(releaseInsert, 10, connection, nil, "sealed", false, 0, "1 day")
	}

	require.NoError(t, (&addExternalMailReleaseOwner{}).Up(db))
	require.NoError(t, (&addMailboxPendingOwner{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
	type owner struct {
		ConnectionID uint
		OwnerScope   string
		OwnerUserID  *uint
	}
	var owners []owner
	require.NoError(t, db.Raw(`SELECT connection_id, owner_scope, owner_user_id FROM external_mail_provider_releases ORDER BY connection_id`).
		Scan(&owners).Error)
	require.Len(t, owners, 4)
	member := uint(901)
	assert.Equal(t, []owner{
		{ConnectionID: 1, OwnerScope: "member", OwnerUserID: &member},
		{ConnectionID: 2, OwnerScope: "shared"},
		{ConnectionID: 3, OwnerScope: "unknown"},
		{ConnectionID: 4, OwnerScope: "unknown"},
	}, owners)
}

// TestExternalMailReleaseOwnerConstraints: only a member's release names a member, the scope is one
// of the three, and deleting the member keeps the release with no owner.
func TestExternalMailReleaseOwnerConstraints(t *testing.T) {
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (902, 'external-other', 'external-other@example.com', 'x', now(), now())`)
	const insert = `INSERT INTO external_mail_provider_releases (organization_id, connection_id, provider, account_address,
	  encrypted_grant, owner_scope, owner_user_id, next_attempt_at, expires_at, created_at)
	VALUES (10, 1, 'gmail', 'owner@gmail.example', 'sealed', ?, ?, now(), now() + interval '1 day', now())`
	for name, arguments := range map[string][]any{
		"shared with a member":  {"shared", 902},
		"unknown with a member": {"unknown", 902},
		"unknown scope":         {"someone", nil},
		"unknown user":          {"member", 999},
	} {
		assert.Error(t, db.Exec(insert, arguments...).Error, name)
	}
	exec(insert, "member", 902)
	exec(insert, "shared", nil)
	exec(`DELETE FROM users WHERE id = 902`)
	var owned, kept int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM external_mail_provider_releases WHERE owner_user_id IS NOT NULL`).Scan(&owned).Error)
	require.NoError(t, db.Raw(`SELECT count(*) FROM external_mail_provider_releases WHERE owner_scope = 'member'`).Scan(&kept).Error)
	assert.Zero(t, owned, "a deleted member leaves no owner")
	assert.EqualValues(t, 1, kept, "the release of a deleted member is kept")
}
