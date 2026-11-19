package migrations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestAddSendLanesBackfillsBroadcastRecipients keeps each lane's usage honest across the upgrade:
// a message a broadcast recipient points at counts in the broadcast lane, every other message
// (outbound or inbound) stays personal, and the migration rolls back and forward cleanly.
func TestAddSendLanesBackfillsBroadcastRecipients(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	require.NoError(t, (&addSendLanes{}).Down(db))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'lane-owner', 'lane-owner@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	sentAt := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		id        int
		direction string
		status    string
	}{{11, "outbound", "sent"}, {12, "outbound", "sent"}, {13, "inbound", "received"}, {14, "outbound", "failed"}} {
		exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, send_attempted_at, created_at, updated_at)
			VALUES (?, 1, ?, ?, 'team@mail.example', ?, ?, ?)`, row.id, row.direction, row.status, sentAt, sentAt, sentAt)
	}
	exec(`INSERT INTO mail_broadcasts (id, organization_id, name, mailbox_id, template_id, list_id, status, created_by, created_at, updated_at)
		VALUES (5, 10, 'News', 1, 1, 1, 'sent', 901, now(), now())`)
	exec(`INSERT INTO mail_broadcast_recipients (broadcast_id, contact_id, email, status, message_id, created_at, updated_at)
		VALUES (5, 1, 'a@example.com', 'sent', 12, now(), now())`)
	exec(`INSERT INTO mail_broadcast_recipients (broadcast_id, contact_id, email, status, message_id, created_at, updated_at)
		VALUES (5, 2, 'b@example.com', 'failed', 14, now(), now())`)
	exec(`INSERT INTO mail_broadcast_recipients (broadcast_id, contact_id, email, status, created_at, updated_at)
		VALUES (5, 3, 'c@example.com', 'pending', now(), now())`)

	require.NoError(t, (&addSendLanes{}).Up(db))

	lanes := func() map[int]string {
		t.Helper()
		type row struct {
			ID       int
			SendLane string
		}
		var rows []row
		require.NoError(t, db.Raw(`SELECT id, send_lane FROM mail_messages ORDER BY id`).Scan(&rows).Error)
		result := make(map[int]string, len(rows))
		for _, r := range rows {
			result[r.ID] = r.SendLane
		}
		return result
	}
	require.Equal(t, map[int]string{11: "personal", 12: "broadcast", 13: "personal", 14: "broadcast"}, lanes())

	// The lane is one of the two stored lanes; transactional mail is counted in its own table.
	require.Error(t, db.Exec(`UPDATE mail_messages SET send_lane = 'transactional' WHERE id = 11`).Error)

	var indexes []string
	require.NoError(t, db.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'mail_messages'
		AND indexname IN ('idx_mail_messages_lane_attempted', 'idx_mail_messages_send_attempted') ORDER BY indexname`).
		Scan(&indexes).Error)
	require.Equal(t, []string{"idx_mail_messages_lane_attempted"}, indexes)

	require.NoError(t, (&addSendLanes{}).Down(db))
	var columns int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'mail_messages' AND column_name = 'send_lane'`).Scan(&columns).Error)
	require.Zero(t, columns, "Down must drop send_lane")
	indexes = nil
	require.NoError(t, db.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'mail_messages'
		AND indexname IN ('idx_mail_messages_lane_attempted', 'idx_mail_messages_send_attempted') ORDER BY indexname`).
		Scan(&indexes).Error)
	require.Equal(t, []string{"idx_mail_messages_send_attempted"}, indexes, "Down must restore the old index")

	require.NoError(t, (&addSendLanes{}).Up(db))
	require.Equal(t, map[int]string{11: "personal", 12: "broadcast", 13: "personal", 14: "broadcast"}, lanes())
}
