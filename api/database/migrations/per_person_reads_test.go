package migrations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// perPersonReadsFixture migrates everything, then rolls the reads migration back so messages can
// be written with the shared read_at column the migration finds in production.
func perPersonReadsFixture(t *testing.T) (*gorm.DB, func(string, ...any)) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	require.NoError(t, (&addPerPersonReads{}).Down(db))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	for id, name := range map[int]string{901: "reads-creator", 902: "reads-departed", 903: "reads-colleague"} {
		exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (?, ?, ?, 'x', now(), now())`,
			id, name, name+"@example.com")
	}
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES (100, 10, 901, 'owner', now(), now())`)
	exec(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES (103, 10, 903, 'member', now(), now())`)
	// Mailbox 1 was created by a current member, who manages it; mailbox 2 by a user who left.
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (2, 10, 902, 'old@mail.example', now(), now())`)
	exec(`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at) VALUES (1, 100, 901, 'manage', 901, now(), now())`)
	exec(`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at) VALUES (1, 103, 903, 'view', 901, now(), now())`)
	return db, exec
}

func insertMessage(exec func(string, ...any), id, mailboxID int, direction string, readAt *time.Time) {
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, read_at, created_at, updated_at)
		VALUES (?, ?, ?, 'received', 'alice@sender.test', ?, now(), now())`, id, mailboxID, direction, readAt)
}

// TestAddPerPersonReadsBackfillsTheCreatorsReads keeps what the shared read state said for the
// person who worked the mailbox until now: the creator, when they still manage it. Everyone else
// starts with everything unread, and the shared column is gone.
func TestAddPerPersonReadsBackfillsTheCreatorsReads(t *testing.T) {
	db, exec := perPersonReadsFixture(t)
	readAt := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	insertMessage(exec, 11, 1, "inbound", &readAt) // read, creator manages: kept
	insertMessage(exec, 12, 1, "inbound", nil)     // unread
	insertMessage(exec, 13, 1, "outbound", &readAt)
	insertMessage(exec, 21, 2, "inbound", &readAt) // read, but the creator left: nobody keeps it

	require.NoError(t, (&addPerPersonReads{}).Up(db))

	type read struct {
		MessageID    int
		MembershipID int
		ReadAt       time.Time
	}
	var reads []read
	require.NoError(t, db.Raw(`SELECT message_id, membership_id, read_at FROM mail_message_reads ORDER BY message_id`).Scan(&reads).Error)
	require.Len(t, reads, 1)
	assert.Equal(t, 11, reads[0].MessageID)
	assert.Equal(t, 100, reads[0].MembershipID)
	assert.True(t, readAt.Equal(reads[0].ReadAt))

	var columns int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'mail_messages' AND column_name = 'read_at'`).Scan(&columns).Error)
	assert.Zero(t, columns, "the shared read_at column is dropped")

	// Down restores the shared column from the creator's reads; outbound mail counts as read.
	require.NoError(t, (&addPerPersonReads{}).Down(db))
	type restored struct {
		ID     int
		ReadAt *time.Time
	}
	var rows []restored
	require.NoError(t, db.Raw(`SELECT id, read_at FROM mail_messages ORDER BY id`).Scan(&rows).Error)
	require.Len(t, rows, 4)
	require.NotNil(t, rows[0].ReadAt)
	assert.True(t, readAt.Equal(*rows[0].ReadAt))
	assert.Nil(t, rows[1].ReadAt)
	assert.NotNil(t, rows[2].ReadAt, "outbound mail is read")
	assert.Nil(t, rows[3].ReadAt)
}

// TestReadsCascadeWithMembershipAndMessage proves leaving the organization removes the person's
// read rows in the same statement, and deleting a message removes its read rows.
func TestReadsCascadeWithMembershipAndMessage(t *testing.T) {
	db, exec := perPersonReadsFixture(t)
	require.NoError(t, (&addPerPersonReads{}).Up(db))
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, created_at, updated_at) VALUES (11, 1, 'inbound', 'received', 'a@b.test', now(), now())`)
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, created_at, updated_at) VALUES (12, 1, 'inbound', 'received', 'a@b.test', now(), now())`)
	exec(`INSERT INTO mail_message_reads (message_id, membership_id, read_at) VALUES (11, 100, now()), (11, 103, now()), (12, 100, now())`)

	exec(`DELETE FROM organization_memberships WHERE id = 103`)
	exec(`DELETE FROM mail_messages WHERE id = 12`)

	type read struct {
		MessageID    int
		MembershipID int
	}
	var reads []read
	require.NoError(t, db.Raw(`SELECT message_id, membership_id FROM mail_message_reads ORDER BY message_id, membership_id`).Scan(&reads).Error)
	assert.Equal(t, []read{{MessageID: 11, MembershipID: 100}}, reads)
}
