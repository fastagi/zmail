package migrations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestAddSendQueueBackfillsAttemptsAndDownDropsWaiting keeps the send limit honest across the
// migration: every outbound message stored before it counts as attempted when it was created,
// inbound mail never does, and rolling back removes waiting messages that were never sent.
func TestAddSendQueueBackfillsAttemptsAndDownDropsWaiting(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	require.NoError(t, (&addSendQueue{}).Down(db))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'queue-owner', 'queue-owner@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	sentAt := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, created_at, updated_at)
		VALUES (11, 1, 'outbound', 'sent', 'team@mail.example', ?, ?)`, sentAt, sentAt)
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, created_at, updated_at)
		VALUES (12, 1, 'inbound', 'received', 'alice@sender.test', ?, ?)`, sentAt, sentAt)

	require.NoError(t, (&addSendQueue{}).Up(db))

	type attempt struct {
		ID              int
		SendAttemptedAt *time.Time
	}
	var attempts []attempt
	require.NoError(t, db.Raw(`SELECT id, send_attempted_at FROM mail_messages ORDER BY id`).Scan(&attempts).Error)
	require.Len(t, attempts, 2)
	require.NotNil(t, attempts[0].SendAttemptedAt, "outbound message has no attempt time")
	require.True(t, attempts[0].SendAttemptedAt.Equal(sentAt), "attempt time = %v; want %v", attempts[0].SendAttemptedAt, sentAt)
	require.Nil(t, attempts[1].SendAttemptedAt, "inbound message got an attempt time")

	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, queued_reason, created_at, updated_at)
		VALUES (13, 1, 'outbound', 'queued', 'team@mail.example', 'send_limit', now(), now())`)
	require.NoError(t, (&addSendQueue{}).Down(db))

	var remaining []int
	require.NoError(t, db.Raw(`SELECT id FROM mail_messages ORDER BY id`).Scan(&remaining).Error)
	require.Equal(t, []int{11, 12}, remaining, "the waiting message must not survive the rollback")
	require.NoError(t, (&addSendQueue{}).Up(db))
}
