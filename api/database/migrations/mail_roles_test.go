package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// mailRolesFixture migrates everything and seeds an organization, a member, and two mailboxes.
func mailRolesFixture(t *testing.T) (*gorm.DB, func(string, ...any)) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'roles-owner', 'roles-owner@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (2, 10, 901, 'other@mail.example', now(), now())`)
	return db, exec
}

// TestMailRolesMigrationUpDown upgrades a database that stopped at 2026_11_21_090100 with
// mailboxes in it: every mailbox starts ungrouped, deleting a role ungroups its mailboxes, Down
// removes the table and the column, and Up again ends at the golden schema.
func TestMailRolesMigrationUpDown(t *testing.T) {
	db, exec := mailRolesFixture(t)
	migrated := dumpSchema(t, db)

	// The platform account ledger references mail_roles, so it rolls back first; AI triage adds a
	// mailboxes column after role_id, so it rolls back too to keep the column order.
	require.NoError(t, (&addExternalMailReleaseOwner{}).Down(db))
	require.NoError(t, (&addExternalMailProviderReleases{}).Down(db))
	require.NoError(t, (&addExternalMailPush{}).Down(db))
	require.NoError(t, (&addExternalMailTenants{}).Down(db))
	require.NoError(t, (&addConnectedMailboxSend{}).Down(db))
	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	require.NoError(t, (&addMailTriage{}).Down(db))
	require.NoError(t, (&createPlatformAccounts{}).Down(db))
	require.NoError(t, (&addMailInboxIndex{}).Down(db))
	require.NoError(t, (&addMailRoles{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "TABLE mail_roles")
	assert.NotContains(t, rolledBack, "COLUMN role_id")
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (3, 10, 901, 'late@mail.example', now(), now())`)

	require.NoError(t, (&addMailRoles{}).Up(db))
	require.NoError(t, (&addMailInboxIndex{}).Up(db))
	require.NoError(t, (&createPlatformAccounts{}).Up(db))
	require.NoError(t, (&addMailTriage{}).Up(db))
	require.NoError(t, (&addMailboxOrigin{}).Up(db))
	require.NoError(t, (&addConnectedMailboxSend{}).Up(db))
	require.NoError(t, (&addExternalMailTenants{}).Up(db))
	require.NoError(t, (&addExternalMailPush{}).Up(db))
	require.NoError(t, (&addExternalMailProviderReleases{}).Up(db))
	require.NoError(t, (&addExternalMailReleaseOwner{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "upgrading must leave the golden schema")

	var grouped int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mailboxes WHERE role_id IS NOT NULL`).Scan(&grouped).Error)
	assert.Zero(t, grouped, "existing mailboxes start ungrouped")

	exec(`INSERT INTO mail_roles (id, organization_id, name, color, owner_user_id, created_at, updated_at) VALUES (5, 10, 'Design', 'blue', 901, now(), now())`)
	exec(`UPDATE mailboxes SET role_id = 5 WHERE id IN (1, 2)`)
	exec(`DELETE FROM mail_roles WHERE id = 5`)
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mailboxes WHERE role_id IS NOT NULL`).Scan(&grouped).Error)
	assert.Zero(t, grouped, "deleting a role ungroups its mailboxes")

	exec(`INSERT INTO mail_roles (id, organization_id, name, owner_user_id, created_at, updated_at) VALUES (6, 10, 'Sales', 901, now(), now())`)
	exec(`UPDATE mailboxes SET role_id = 6 WHERE id = 1`)

	require.NoError(t, (&addExternalMailReleaseOwner{}).Down(db))
	require.NoError(t, (&addExternalMailProviderReleases{}).Down(db))
	require.NoError(t, (&addExternalMailPush{}).Down(db))
	require.NoError(t, (&addExternalMailTenants{}).Down(db))
	require.NoError(t, (&addConnectedMailboxSend{}).Down(db))
	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	require.NoError(t, (&addMailTriage{}).Down(db))
	require.NoError(t, (&createPlatformAccounts{}).Down(db))
	require.NoError(t, (&addMailRoles{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "TABLE mail_roles", "Down must drop the table over stored roles")
	require.NoError(t, (&addMailRoles{}).Up(db))
	require.NoError(t, (&createPlatformAccounts{}).Up(db))
	require.NoError(t, (&addMailTriage{}).Up(db))
	require.NoError(t, (&addMailboxOrigin{}).Up(db))
	require.NoError(t, (&addConnectedMailboxSend{}).Up(db))
	require.NoError(t, (&addExternalMailTenants{}).Up(db))
	require.NoError(t, (&addExternalMailPush{}).Up(db))
	require.NoError(t, (&addExternalMailProviderReleases{}).Up(db))
	require.NoError(t, (&addExternalMailReleaseOwner{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestMailRoleColorCheck keeps the palette, the trimmed name, and the case-insensitive name
// unique in the schema, so a write that skips the service still cannot store them.
func TestMailRoleColorCheck(t *testing.T) {
	db, exec := mailRolesFixture(t)
	exec(`INSERT INTO mail_roles (organization_id, name, color, created_at, updated_at) VALUES (10, 'Design', 'pink', now(), now())`)

	for name, statement := range map[string]string{
		"color outside the palette": `INSERT INTO mail_roles (organization_id, name, color, created_at, updated_at) VALUES (10, 'Ops', 'black', now(), now())`,
		"blank name":                `INSERT INTO mail_roles (organization_id, name, created_at, updated_at) VALUES (10, '   ', now(), now())`,
		"name taken ignoring case":  `INSERT INTO mail_roles (organization_id, name, created_at, updated_at) VALUES (10, 'DESIGN', now(), now())`,
	} {
		assert.Error(t, db.Exec(statement).Error, name)
	}

	var color string
	exec(`INSERT INTO mail_roles (id, organization_id, name, created_at, updated_at) VALUES (8, 10, 'Ops', now(), now())`)
	require.NoError(t, db.Raw(`SELECT color FROM mail_roles WHERE id = 8`).Scan(&color).Error)
	assert.Equal(t, "gray", color, "the default color")
}
