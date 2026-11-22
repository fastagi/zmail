package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestOpenMailConnectorsUpDown rolls the migration back and forth over an external mailbox and its
// connection: the result is the golden schema again.
func TestOpenMailConnectorsUpDown(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)
	// Later migrations that build on these tables roll back first.
	require.NoError(t, (&addExternalMailPush{}).Down(db))
	require.NoError(t, (&addExternalMailTenants{}).Down(db))
	require.NoError(t, (&addConnectedMailboxSend{}).Down(db))
	require.NoError(t, (&addSharedExternalMail{}).Down(db))
	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'external', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'gmail', 'owner@gmail.example', 901, now(), now(), now())`)

	require.NoError(t, (&openMailConnectors{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "TABLE external_mail_authorizations")
	require.NoError(t, (&openMailConnectors{}).Up(db))
	require.NoError(t, (&addMailboxOrigin{}).Up(db))
	require.NoError(t, (&addSharedExternalMail{}).Up(db))
	require.NoError(t, (&addConnectedMailboxSend{}).Up(db))
	require.NoError(t, (&addExternalMailTenants{}).Up(db))
	require.NoError(t, (&addExternalMailPush{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestOpenMailConnectorsAcceptsAnyConnectorID: a new connector needs no migration, while malformed
// ids are still refused.
func TestOpenMailConnectorsAcceptsAnyConnectorID(t *testing.T) {
	db, exec := externalMailFixture(t)
	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@icloud.example', 'external', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'imap', 'owner@icloud.example', 901, now(), now(), now())`)
	exec(`INSERT INTO mail_messages (mailbox_id, direction, status, from_address, source, external_id, created_at, updated_at)
		VALUES (2, 'inbound', 'received', 'bob@sender.test', 'imap', 'uid-1', now(), now())`)

	for name, statement := range map[string]string{
		"upper case provider": `UPDATE external_mail_connections SET provider = 'IMAP' WHERE id = 1`,
		"provider too long":   `UPDATE external_mail_connections SET provider = 'a_connector_id_too_long' WHERE id = 1`,
		"provider with space": `UPDATE external_mail_connections SET provider = 'my mail' WHERE id = 1`,
		"malformed source": `INSERT INTO mail_messages (mailbox_id, direction, status, from_address, source, created_at, updated_at)
			VALUES (2, 'inbound', 'received', 'bob@sender.test', '1mail', now(), now())`,
	} {
		assert.Error(t, db.Exec(statement).Error, name)
	}
	// A rollback refuses connectors the older schema does not know.
	assert.Error(t, db.Transaction(func(tx *gorm.DB) error { return (&openMailConnectors{}).Down(tx) }))
}

// TestOpenMailConnectorsSeparatesExternalAddresses: an external mailbox may share an address with
// another external mailbox or a Zmail mailbox, while Zmail addresses stay unique.
func TestOpenMailConnectorsSeparatesExternalAddresses(t *testing.T) {
	db, exec := externalMailFixture(t)
	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (902, 'colleague', 'colleague@example.com', 'x', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'team@mail.example', 'external', 901, now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, owner_user_id, created_at, updated_at)
		VALUES (3, 10, 902, 'team@mail.example', 'external', 902, now(), now())`)
	assert.Error(t, db.Exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at)
		VALUES (4, 10, 901, 'team@mail.example', now(), now())`).Error, "a second Zmail mailbox with the address")
}

// TestOpenMailConnectorsAuthorizations keeps a pending sign-in's rules in the schema: a state hash
// is used once, and deleting the connection or the member deletes the sign-in.
func TestOpenMailConnectorsAuthorizations(t *testing.T) {
	db, exec := externalMailFixture(t)
	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'external', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'gmail', 'owner@gmail.example', 901, now(), now(), now())`)
	insert := `INSERT INTO external_mail_authorizations (organization_id, user_id, provider, state_hash, sealed_verifier, connection_id, expires_at, created_at)
		VALUES (10, 901, 'gmail', ?, 'sealed', ?, now(), now())`
	exec(insert, repeatHex("a"), 1)
	exec(insert, repeatHex("b"), nil)
	assert.Error(t, db.Exec(insert, repeatHex("a"), nil).Error, "same state twice")
	assert.Error(t, db.Exec(`INSERT INTO external_mail_authorizations (organization_id, user_id, provider, state_hash, sealed_verifier, backfill_days, expires_at, created_at)
		VALUES (10, 901, 'gmail', ?, 'sealed', 91, now(), now())`, repeatHex("c")).Error, "backfill too long")

	exec(`DELETE FROM external_mail_connections WHERE id = 1`)
	var left int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM external_mail_authorizations`).Scan(&left).Error)
	assert.EqualValues(t, 1, left, "deleting the connection deletes its reconnect sign-in only")
}

func repeatHex(digit string) string {
	out := ""
	for range 64 {
		out += digit
	}
	return out
}
