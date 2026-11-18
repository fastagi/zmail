package migrations

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// mailboxAccessFixture migrates everything, then rolls the access migration back so rows can be
// written in the shape the migration finds in production.
func mailboxAccessFixture(t *testing.T) (*gorm.DB, func(string, ...any)) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	require.NoError(t, (&addMailboxAccess{}).Down(db))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	for id, name := range map[int]string{901: "access-creator", 902: "access-departed", 903: "access-colleague"} {
		exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (?, ?, ?, 'x', now(), now())`,
			id, name, name+"@example.com")
	}
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES (100, 10, 901, 'owner', now(), now())`)
	exec(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES (103, 10, 903, 'member', now(), now())`)
	return db, exec
}

// TestAddMailboxAccessBackfillsCreatorsAsManagers proves the existing mailboxes stay usable: each
// owned mailbox stays shared, its creator becomes a manage member while still in the organization,
// and each gets one created event.
func TestAddMailboxAccessBackfillsCreatorsAsManagers(t *testing.T) {
	db, exec := mailboxAccessFixture(t)
	// Mailbox 1 was created by a current member; mailbox 2 by a user who has since left.
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (2, 10, 902, 'old@mail.example', now(), now())`)

	require.NoError(t, (&addMailboxAccess{}).Up(db))

	var types []string
	require.NoError(t, db.Raw(`SELECT type FROM mailboxes ORDER BY id`).Scan(&types).Error)
	assert.Equal(t, []string{"shared", "shared"}, types)

	type member struct {
		MailboxID    int
		MembershipID int
		UserID       int
		Permission   string
		GrantedBy    int
	}
	var members []member
	require.NoError(t, db.Raw(`SELECT mailbox_id, membership_id, user_id, permission, granted_by FROM mailbox_members ORDER BY mailbox_id`).Scan(&members).Error)
	assert.Equal(t, []member{{MailboxID: 1, MembershipID: 100, UserID: 901, Permission: "manage", GrantedBy: 901}}, members)

	type event struct {
		MailboxID      int
		OrganizationID int
		Type           string
		Detail         string
	}
	var events []event
	require.NoError(t, db.Raw(`SELECT mailbox_id, organization_id, type, detail::text AS detail FROM mailbox_access_events ORDER BY mailbox_id`).Scan(&events).Error)
	require.Len(t, events, 2, "one created event per mailbox")
	for _, e := range events {
		assert.Equal(t, "created", e.Type)
		assert.Equal(t, 10, e.OrganizationID)
	}
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(events[0].Detail), &detail))
	assert.EqualValues(t, 100, detail["membership_id"], "the backfilled member's membership is recorded for departure tracking")
}

// TestAddMailboxAccessSkipsMailboxesWithoutOrganization keeps unowned mailboxes untouched: they
// have no organization to grant access in.
func TestAddMailboxAccessSkipsMailboxesWithoutOrganization(t *testing.T) {
	db, exec := mailboxAccessFixture(t)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (5, NULL, 901, 'orphan@mail.example', now(), now())`)

	require.NoError(t, (&addMailboxAccess{}).Up(db))

	var members, events int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mailbox_members`).Scan(&members).Error)
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mailbox_access_events`).Scan(&events).Error)
	assert.Zero(t, members)
	assert.Zero(t, events)
}

// TestMembershipDeleteCascadesMembers proves leaving the organization removes the member rows of
// that membership in the same statement, and that a rejoin with a new membership has none.
func TestMembershipDeleteCascadesMembers(t *testing.T) {
	db, exec := mailboxAccessFixture(t)
	require.NoError(t, (&addMailboxAccess{}).Up(db))
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	exec(`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at) VALUES (1, 100, 901, 'manage', 901, now(), now())`)
	exec(`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at) VALUES (1, 103, 903, 'view', 901, now(), now())`)

	exec(`DELETE FROM organization_memberships WHERE id = 103`)
	exec(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES (104, 10, 903, 'member', now(), now())`)

	var remaining []int
	require.NoError(t, db.Raw(`SELECT membership_id FROM mailbox_members ORDER BY membership_id`).Scan(&remaining).Error)
	assert.Equal(t, []int{100}, remaining, "the departed membership's row is gone and the rejoin has none")

	// Constraints: permissions are a closed set and personal mailboxes never publish webhooks.
	assert.Error(t, db.Exec(`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at) VALUES (1, 104, 903, 'owner', 901, now(), now())`).Error)
	assert.Error(t, db.Exec(`UPDATE mailboxes SET type = 'personal', webhooks_enabled = true WHERE id = 1`).Error)
	assert.Error(t, db.Exec(`UPDATE mailboxes SET type = 'team' WHERE id = 1`).Error)

	// Deleting the user who owns a personal mailbox keeps the mailbox without an owner.
	exec(`UPDATE mailboxes SET type = 'personal', owner_user_id = 903 WHERE id = 1`)
	exec(`DELETE FROM organization_memberships WHERE user_id = 903`)
	exec(`DELETE FROM users WHERE id = 903`)
	var owner *int
	require.NoError(t, db.Raw(`SELECT owner_user_id FROM mailboxes WHERE id = 1`).Scan(&owner).Error)
	assert.Nil(t, owner)
}
