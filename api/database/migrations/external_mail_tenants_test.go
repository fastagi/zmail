package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestExternalMailTenantsUpDown rolls the migration back over a member's connection and applies it
// again: the result is the golden schema.
func TestExternalMailTenantsUpDown(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)

	require.NoError(t, (&addExternalMailTenants{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "external_mail_tenants")
	assert.NotContains(t, rolledBack, "tenant_directory_id")
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (organization_id, mailbox_id, provider, account_address, owner_user_id, next_sync_at, created_at, updated_at)
		VALUES (10, 2, 'gmail', 'owner@gmail.example', 901, now(), now(), now())`)

	require.NoError(t, (&addExternalMailTenants{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestExternalMailTenantsConstraints: a tenant connection holds no account token, only a connected
// mailbox comes from a tenant, a tenant identifier is live once across organizations, a live tenant
// holds a credential, and a rollback refuses while a mailbox comes from a tenant.
func TestExternalMailTenantsConstraints(t *testing.T) {
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (11, 'Other', 'other', 901, now(), now())`)
	tenant := `INSERT INTO external_mail_tenants (id, organization_id, provider, tenant_identifier, status, encrypted_credential, authorized_at, created_at, updated_at)
		VALUES (?, ?, 'gmail', ?, ?, ?, now(), now(), now())`
	exec(tenant, 1, 10, "C01abc", "active", "sealed")
	assert.Error(t, db.Exec(tenant, 2, 11, "c01ABC", "active", "sealed").Error, "a tenant live in two organizations")
	assert.Error(t, db.Exec(tenant, 3, 11, "C02", "active", "").Error, "a live tenant without a credential")
	assert.Error(t, db.Exec(tenant, 4, 11, "C03", "paused", "sealed").Error, "an unknown status")
	assert.Error(t, db.Exec(tenant, 5, 11, "", "active", "sealed").Error, "an empty identifier")
	exec(tenant, 6, 11, "C04", "revoked", "")
	exec(`UPDATE external_mail_tenants SET status = 'revoked', encrypted_credential = '' WHERE id = 1`)
	exec(tenant, 7, 11, "C01ABC", "active", "sealed")

	assert.Error(t, db.Exec(`UPDATE mailboxes SET external_mail_tenant_id = 7 WHERE id = 1`).Error, "a Zmail mailbox from a tenant")
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, external_mail_tenant_id, created_at, updated_at)
		VALUES (2, 11, 901, 'alice@acme.example', 'personal', 'connected', 901, 7, now(), now())`)
	connection := `INSERT INTO external_mail_connections (organization_id, mailbox_id, provider, account_address, owner_user_id,
		encrypted_refresh_token, tenant_id, tenant_directory_id, next_sync_at, created_at, updated_at)
		VALUES (11, 2, 'gmail', 'alice@acme.example', 901, ?, 7, 'u-1', now(), now(), now())`
	assert.Error(t, db.Exec(connection, "sealed-token").Error, "a tenant connection with an account token")
	exec(connection, "")
	assignee := `INSERT INTO external_mail_personal_assignments (organization_id, provider, address_key, user_id, tenant_id, assigned_at)
		VALUES (11, 'gmail', ?, 901, 7, now())`
	exec(assignee, "alice@acme.example")
	assert.Error(t, db.Exec(assignee, "alice@acme.example").Error, "one holder per address")
	assert.Error(t, db.Exec(assignee, "Bob@acme.example").Error, "an address key that is not lowercase")
	assert.Error(t, db.Transaction(func(tx *gorm.DB) error { return (&addExternalMailTenants{}).Down(tx) }))

	exec(`DELETE FROM external_mail_connections WHERE tenant_id IS NOT NULL`)
	exec(`DELETE FROM mailboxes WHERE external_mail_tenant_id IS NOT NULL`)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return (&addExternalMailTenants{}).Down(tx) }))
}
