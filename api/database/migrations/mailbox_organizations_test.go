package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
	"github.com/fastagi/zmail/api/internal/modules/user"
)

// TestMoveMailboxesToOrganizationsKeepsExistingMail proves the ownership change destroys nothing:
// a user's mailbox moves to the organization they joined first, and the mailbox of a user without
// any organization keeps its rows, unowned.
func TestMoveMailboxesToOrganizationsKeepsExistingMail(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	require.NoError(t, db.AutoMigrate(&user.UserPO{}))
	require.NoError(t, (&createOrganizationsTables{}).Up(db))
	require.NoError(t, (&createMailboxTables{}).Up(db))
	require.NoError(t, (&addMailMessageIdempotency{}).Up(db))

	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	for id, name := range map[int]string{1: "member-of-two", 2: "no-organization"} {
		exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (?, ?, ?, 'x', now(), now())`,
			id, name, name+"@example.com")
	}
	for id, slug := range map[int]string{10: "first", 20: "second"} {
		exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (?, ?, ?, 1, now(), now())`,
			id, slug, slug)
	}
	// User 1 joined organization 20 before organization 10.
	exec(`INSERT INTO organization_memberships (organization_id, user_id, role, created_at, updated_at) VALUES (20, 1, 'member', now() - interval '2 days', now())`)
	exec(`INSERT INTO organization_memberships (organization_id, user_id, role, created_at, updated_at) VALUES (10, 1, 'owner', now() - interval '1 day', now())`)
	exec(`INSERT INTO mailboxes (id, user_id, address, created_at, updated_at) VALUES (1, 1, 'team@mail.example', now(), now())`)
	exec(`INSERT INTO mailboxes (id, user_id, address, created_at, updated_at) VALUES (2, 2, 'solo@mail.example', now(), now())`)
	for _, mailboxID := range []int{1, 2} {
		exec(`INSERT INTO mail_messages (mailbox_id, direction, status, from_address, created_at, updated_at) VALUES (?, 'inbound', 'received', 'a@sender.test', now(), now())`, mailboxID)
	}

	migration := &moveMailboxesToOrganizations{}
	require.NoError(t, migration.Up(db))

	type row struct {
		ID             int
		OrganizationID *int
		CreatedBy      int
	}
	var rows []row
	require.NoError(t, db.Raw(`SELECT id, organization_id, created_by FROM mailboxes ORDER BY id`).Scan(&rows).Error)
	require.Len(t, rows, 2)
	require.NotNil(t, rows[0].OrganizationID)
	assert.Equal(t, 20, *rows[0].OrganizationID, "the mailbox follows its creator's earliest membership")
	assert.Equal(t, 1, rows[0].CreatedBy)
	assert.Nil(t, rows[1].OrganizationID, "a creator without an organization leaves the mailbox unowned")
	assert.Equal(t, 2, rows[1].CreatedBy)

	var messages int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mail_messages`).Scan(&messages).Error)
	assert.EqualValues(t, 2, messages, "no mail is deleted by the ownership change")

	// The organization now owns the mailbox: deleting it removes its mailboxes.
	exec(`DELETE FROM organizations WHERE id = 20`)
	var remaining int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mailboxes`).Scan(&remaining).Error)
	assert.EqualValues(t, 1, remaining)

	require.NoError(t, migration.Down(db))
	assert.True(t, db.Migrator().HasColumn("mailboxes", "user_id"))
	assert.False(t, db.Migrator().HasColumn("mailboxes", "organization_id"))
}
