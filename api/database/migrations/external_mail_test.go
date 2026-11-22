package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// externalMailFixture migrates everything and seeds an organization, a user, a mailbox, and one
// inbound message stored before external mail existed.
func externalMailFixture(t *testing.T) (*gorm.DB, func(string, ...any)) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'external-owner', 'external-owner@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, thread_id, created_at, updated_at)
		VALUES (11, 1, 'inbound', 'received', 'alice@sender.test', 11, now(), now())`)
	return db, exec
}

// TestExternalMailMigrationUpDown adds external mail to a database with mail in it, rolls it back
// over a stored connection and synced mail, and adds it again: the result is the golden schema,
// and existing mail reads as Zmail's own.
func TestExternalMailMigrationUpDown(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)

	var source string
	require.NoError(t, db.Raw(`SELECT source FROM mail_messages WHERE id = 11`).Scan(&source).Error)
	assert.Equal(t, "zmail", source, "existing mail is Zmail's own")

	exec(`INSERT INTO external_mail_connections (organization_id, mailbox_id, provider, account_address, owner_user_id, next_sync_at, created_at, updated_at)
		VALUES (10, 1, 'gmail', 'owner@gmail.example', 901, now(), now(), now())`)
	exec(`INSERT INTO mail_messages (mailbox_id, direction, status, from_address, source, external_id, created_at, updated_at)
		VALUES (1, 'inbound', 'received', 'bob@sender.test', 'gmail', 'g-1', now(), now())`)

	// Later migrations that build on these tables roll back first.
	require.NoError(t, (&addExternalMailTenants{}).Down(db))
	require.NoError(t, (&addSharedExternalMail{}).Down(db))
	require.NoError(t, (&addExternalMailSyncOps{}).Down(db))
	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	require.NoError(t, (&openMailConnectors{}).Down(db))
	require.NoError(t, (&addMailOutboundTimeIndex{}).Down(db))
	require.NoError(t, (&addExternalMail{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "TABLE external_mail_connections")
	assert.NotContains(t, rolledBack, "external_id")

	require.NoError(t, (&addExternalMail{}).Up(db))
	require.NoError(t, (&addMailOutboundTimeIndex{}).Up(db))
	require.NoError(t, (&openMailConnectors{}).Up(db))
	require.NoError(t, (&addMailboxOrigin{}).Up(db))
	require.NoError(t, (&addExternalMailSyncOps{}).Up(db))
	require.NoError(t, (&addSharedExternalMail{}).Up(db))
	require.NoError(t, (&addExternalMailTenants{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestExternalMailDownRefusesExternalMailboxes keeps a rollback from leaving mailboxes of a type
// the older schema does not know.
func TestExternalMailDownRefusesExternalMailboxes(t *testing.T) {
	db, exec := externalMailFixture(t)
	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'external', 901, now(), now())`)
	assert.Error(t, db.Transaction(func(tx *gorm.DB) error { return (&addExternalMail{}).Down(tx) }))
}

// TestExternalMailConstraints keeps the external mail rules in the schema, so a write that skips
// the service still cannot break them.
func TestExternalMailConstraints(t *testing.T) {
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'gmail', 'Owner@Gmail.example', 901, now(), now(), now())`)
	exec(`INSERT INTO mail_messages (mailbox_id, direction, status, from_address, source, external_id, created_at, updated_at)
		VALUES (2, 'inbound', 'received', 'bob@sender.test', 'gmail', 'g-1', now(), now())`)

	for name, statement := range map[string]string{
		"unknown mailbox type": `INSERT INTO mailboxes (organization_id, created_by, address, type, created_at, updated_at)
			VALUES (10, 901, 'x@mail.example', 'imap', now(), now())`,
		"malformed message source": `INSERT INTO mail_messages (mailbox_id, direction, status, from_address, source, created_at, updated_at)
			VALUES (2, 'inbound', 'received', 'bob@sender.test', 'Imap!', now(), now())`,
		"same external id twice in a mailbox": `INSERT INTO mail_messages (mailbox_id, direction, status, from_address, source, external_id, created_at, updated_at)
			VALUES (2, 'inbound', 'received', 'bob@sender.test', 'gmail', 'g-1', now(), now())`,
		"malformed provider": `UPDATE external_mail_connections SET provider = 'IMAP Mail' WHERE id = 1`,
		"unknown status":     `UPDATE external_mail_connections SET status = 'paused' WHERE id = 1`,
		"backfill too long":  `UPDATE external_mail_connections SET backfill_days = 91 WHERE id = 1`,
		"second connection for the mailbox": `INSERT INTO external_mail_connections (organization_id, mailbox_id, provider, account_address, next_sync_at, created_at, updated_at)
			VALUES (10, 2, 'outlook', 'other@outlook.example', now(), now(), now())`,
		"same account twice ignoring case": `INSERT INTO external_mail_connections (organization_id, mailbox_id, provider, account_address, next_sync_at, created_at, updated_at)
			VALUES (10, 1, 'gmail', 'owner@gmail.EXAMPLE', now(), now(), now())`,
	} {
		assert.Error(t, db.Exec(statement).Error, name)
	}

	// The same external id in another mailbox is another message.
	exec(`INSERT INTO mail_messages (mailbox_id, direction, status, from_address, source, external_id, created_at, updated_at)
		VALUES (1, 'inbound', 'received', 'bob@sender.test', 'gmail', 'g-1', now(), now())`)

	var connection struct {
		Status       string
		BackfillDays int
	}
	require.NoError(t, db.Raw(`SELECT status, backfill_days FROM external_mail_connections WHERE id = 1`).Scan(&connection).Error)
	assert.Equal(t, "active", connection.Status)
	assert.Equal(t, 14, connection.BackfillDays, "the first sync looks back 14 days by default")

	exec(`DELETE FROM mailboxes WHERE id = 2`)
	var left int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM external_mail_connections`).Scan(&left).Error)
	assert.Zero(t, left, "deleting the mailbox deletes its connection")
}
