package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestSharedExternalMailUpDown rolls the migration back over a member's connection and applies it
// again: the binder is backfilled from the owner, and the schema is the golden one again.
func TestSharedExternalMailUpDown(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)

	require.NoError(t, (&addExternalMailProviderReleases{}).Down(db))
	require.NoError(t, (&addExternalMailPush{}).Down(db))
	require.NoError(t, (&addExternalMailTenants{}).Down(db))
	require.NoError(t, (&addSharedExternalMail{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "bound_by_user_id")
	assert.NotContains(t, rolledBack, "chk_external_mail_connections_shared")
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (organization_id, mailbox_id, provider, account_address, owner_user_id, next_sync_at, created_at, updated_at)
		VALUES (10, 2, 'gmail', 'owner@gmail.example', 901, now(), now(), now())`)

	require.NoError(t, (&addSharedExternalMail{}).Up(db))
	require.NoError(t, (&addExternalMailTenants{}).Up(db))
	require.NoError(t, (&addExternalMailPush{}).Up(db))
	require.NoError(t, (&addExternalMailProviderReleases{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
	var row struct {
		Shared        bool
		BoundByUserID *uint
	}
	require.NoError(t, db.Raw(`SELECT shared, bound_by_user_id FROM external_mail_connections WHERE mailbox_id = 2`).Scan(&row).Error)
	assert.False(t, row.Shared)
	require.NotNil(t, row.BoundByUserID)
	assert.EqualValues(t, 901, *row.BoundByUserID)
}

// TestSharedExternalMailConstraints: a shared connection has no owner, and a rollback refuses to
// turn an organization's connection into an ownerless member's one.
func TestSharedExternalMailConstraints(t *testing.T) {
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, created_at, updated_at)
		VALUES (2, 10, 901, 'support@gmail.example', 'shared', 'connected', now(), now())`)
	assert.Error(t, db.Exec(`INSERT INTO external_mail_connections
		(organization_id, mailbox_id, provider, account_address, owner_user_id, shared, next_sync_at, created_at, updated_at)
		VALUES (10, 2, 'gmail', 'support@gmail.example', 901, true, now(), now(), now())`).Error, "a shared connection with an owner")
	exec(`INSERT INTO external_mail_connections
		(organization_id, mailbox_id, provider, account_address, shared, bound_by_user_id, next_sync_at, created_at, updated_at)
		VALUES (10, 2, 'gmail', 'support@gmail.example', true, 901, now(), now(), now())`)
	exec(`INSERT INTO external_mail_authorizations (organization_id, user_id, provider, state_hash, sealed_verifier, sharing, expires_at, created_at)
		VALUES (10, 901, 'gmail', repeat('a', 64), 'sealed', '{"members":[{"user_id":901,"permission":"manage"}]}', now(), now())`)
	exec(`INSERT INTO external_mail_authorizations (organization_id, user_id, provider, state_hash, sealed_verifier, expires_at, created_at)
		VALUES (10, 901, 'gmail', repeat('b', 64), 'sealed', now(), now())`)
	assert.Error(t, db.Transaction(func(tx *gorm.DB) error { return (&addSharedExternalMail{}).Down(tx) }))

	// Once no shared connection is left, a rollback deletes the pending shared sign-ins and keeps
	// the members' own.
	exec(`DELETE FROM external_mail_connections WHERE shared`)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return (&addSharedExternalMail{}).Down(tx) }))
	var pending []string
	require.NoError(t, db.Raw(`SELECT state_hash FROM external_mail_authorizations ORDER BY state_hash`).Scan(&pending).Error)
	assert.Equal(t, []string{strings.Repeat("b", 64)}, pending)
}
