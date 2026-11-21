package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestMailboxOriginConvertsExternalMailboxes rolls the migration back over an external mailbox, a
// soft-deleted one, and a Zmail mailbox, then applies it again: external rows become personal and
// connected, Zmail rows keep their type, and the schema is the golden one again.
func TestMailboxOriginConvertsExternalMailboxes(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, owner_user_id, created_at, updated_at)
		VALUES (5, 10, 901, 'riley@mail.example', 'personal', 901, now(), now())`)

	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "chk_mailboxes_origin")
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'external', 901, now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, owner_user_id, created_at, updated_at, deleted_at)
		VALUES (3, 10, 901, 'old@outlook.example', 'external', 901, now(), now(), now())`)

	require.NoError(t, (&addMailboxOrigin{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")

	var rows []struct {
		ID     uint
		Type   string
		Origin string
	}
	require.NoError(t, db.Raw(`SELECT id, type, origin FROM mailboxes ORDER BY id`).Scan(&rows).Error)
	require.Len(t, rows, 4)
	assert.Equal(t, []string{"shared", "personal", "personal", "personal"},
		[]string{rows[0].Type, rows[1].Type, rows[2].Type, rows[3].Type})
	assert.Equal(t, []string{"zmail", "connected", "connected", "zmail"},
		[]string{rows[0].Origin, rows[1].Origin, rows[2].Origin, rows[3].Origin})

	// Down restores the external type for the connected personal mailboxes.
	require.NoError(t, (&addMailboxOrigin{}).Down(db))
	var external int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mailboxes WHERE type = 'external'`).Scan(&external).Error)
	assert.EqualValues(t, 2, external)
}

// TestMailboxOriginConstraints keeps the origin rules in the schema: the external type is gone, an
// unknown origin is refused, a connected address may repeat while a Zmail address stays unique,
// and a personal mailbox never publishes webhooks whatever its origin.
func TestMailboxOriginConstraints(t *testing.T) {
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (902, 'colleague', 'colleague@example.com', 'x', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'team@mail.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (3, 10, 902, 'team@mail.example', 'personal', 'connected', 902, now(), now())`)

	var origin string
	require.NoError(t, db.Raw(`SELECT origin FROM mailboxes WHERE id = 1`).Scan(&origin).Error)
	assert.Equal(t, "zmail", origin, "existing and new mailboxes are Zmail's own by default")

	for name, statement := range map[string]string{
		"external type": `INSERT INTO mailboxes (organization_id, created_by, address, type, owner_user_id, created_at, updated_at)
			VALUES (10, 901, 'x@gmail.example', 'external', 901, now(), now())`,
		"unknown origin": `INSERT INTO mailboxes (organization_id, created_by, address, origin, created_at, updated_at)
			VALUES (10, 901, 'y@mail.example', 'imap', now(), now())`,
		"second Zmail mailbox with the address": `INSERT INTO mailboxes (organization_id, created_by, address, created_at, updated_at)
			VALUES (10, 901, 'team@mail.example', now(), now())`,
		"webhooks on a connected personal mailbox": `UPDATE mailboxes SET webhooks_enabled = true WHERE id = 2`,
	} {
		assert.Error(t, db.Exec(statement).Error, name)
	}
}

// TestMailboxOriginDownRefusesConnectedShared keeps a rollback from dropping the origin of a shared
// mailbox the older schema cannot express.
func TestMailboxOriginDownRefusesConnectedShared(t *testing.T) {
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, created_at, updated_at)
		VALUES (2, 10, 901, 'support@gmail.example', 'shared', 'connected', now(), now())`)
	assert.Error(t, db.Transaction(func(tx *gorm.DB) error { return (&addMailboxOrigin{}).Down(tx) }))
}
