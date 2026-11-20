package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// platformAccountsFixture migrates everything and seeds an organization, a user, a mailbox, and a
// mail role.
func platformAccountsFixture(t *testing.T) (*gorm.DB, func(string, ...any)) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'ledger-owner', 'ledger-owner@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (902, 'ledger-member', 'ledger-member@example.com', 'x', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	exec(`INSERT INTO mail_roles (id, organization_id, name, created_at, updated_at) VALUES (5, 10, 'Design', now(), now())`)
	return db, exec
}

// TestPlatformAccountsMigrationUpDown adds the ledger to a database with mailboxes and roles in
// it, rolls it back over stored accounts, and adds it again: the result is the golden schema.
func TestPlatformAccountsMigrationUpDown(t *testing.T) {
	db, exec := platformAccountsFixture(t)
	migrated := dumpSchema(t, db)

	exec(`INSERT INTO platform_accounts (id, organization_id, platform, handle, role_id, mailbox_id, owner_user_id, status, created_at, updated_at)
		VALUES (1, 10, 'Medium', '@acme', 5, 1, 901, 'active', now(), now())`)
	exec(`INSERT INTO platform_account_events (organization_id, account_id, kind, actor_user_id, to_value, created_at)
		VALUES (10, 1, 'created', 901, 'active', now())`)

	require.NoError(t, (&createPlatformAccounts{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "TABLE platform_accounts")
	assert.NotContains(t, rolledBack, "TABLE platform_account_events")

	require.NoError(t, (&createPlatformAccounts{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestPlatformAccountConstraints keeps the ledger's rules in the schema, so a write that skips the
// service still cannot break them.
func TestPlatformAccountConstraints(t *testing.T) {
	db, exec := platformAccountsFixture(t)
	exec(`INSERT INTO platform_accounts (id, organization_id, platform, handle, role_id, mailbox_id, owner_user_id, created_at, updated_at)
		VALUES (1, 10, 'Medium', '@acme', 5, 1, 902, now(), now())`)

	for name, statement := range map[string]string{
		"same platform and handle ignoring case": `INSERT INTO platform_accounts (organization_id, platform, handle, created_at, updated_at) VALUES (10, 'MEDIUM', '@ACME', now(), now())`,
		"blank platform":                         `INSERT INTO platform_accounts (organization_id, platform, handle, created_at, updated_at) VALUES (10, '  ', 'x', now(), now())`,
		"blank handle":                           `INSERT INTO platform_accounts (organization_id, platform, handle, created_at, updated_at) VALUES (10, 'Behance', ' ', now(), now())`,
		"unknown status":                         `INSERT INTO platform_accounts (organization_id, platform, handle, status, created_at, updated_at) VALUES (10, 'Behance', 'a', 'lost', now(), now())`,
		"unknown event kind":                     `INSERT INTO platform_account_events (organization_id, account_id, kind, created_at) VALUES (10, 1, 'renamed', now())`,
	} {
		assert.Error(t, db.Exec(statement).Error, name)
	}

	var status string
	require.NoError(t, db.Raw(`SELECT status FROM platform_accounts WHERE id = 1`).Scan(&status).Error)
	assert.Equal(t, "active", status, "the default status")

	exec(`DELETE FROM mail_roles WHERE id = 5`)
	exec(`DELETE FROM users WHERE id = 902`)
	var cleared int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM platform_accounts WHERE id = 1 AND role_id IS NULL AND owner_user_id IS NULL`).Scan(&cleared).Error)
	assert.Equal(t, int64(1), cleared, "deleting the role and the owner clears them on the account")

	exec(`INSERT INTO platform_account_events (organization_id, account_id, kind, to_value, created_at) VALUES (10, 1, 'deleted', 'active', now())`)
	exec(`DELETE FROM platform_accounts WHERE id = 1`)
	var kept int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM platform_account_events WHERE account_id = 1`).Scan(&kept).Error)
	assert.Equal(t, int64(1), kept, "events outlive the hard-deleted account")
}
