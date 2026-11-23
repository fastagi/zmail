package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestAddBroadcastNoticesBackfillsSoTheDeployTellsNoOne records every existing broadcast's state as
// already told, so the worker sends no notice about broadcasts that ended or paused before it.
func TestAddBroadcastNoticesBackfillsSoTheDeployTellsNoOne(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	require.NoError(t, (&addBroadcastNotices{}).Down(db))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'owner', 'owner@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	broadcast := func(id int, status, stopReason string) {
		t.Helper()
		exec(`INSERT INTO mail_broadcasts (id, organization_id, name, mailbox_id, template_id, list_id, status, stop_reason, created_by, created_at, updated_at)
			VALUES (?, 10, 'News', 1, 1, 1, ?, ?, 901, now(), now())`, id, status, stopReason)
	}
	broadcast(1, "draft", "")
	broadcast(2, "sent", "")
	broadcast(3, "canceled", "all_recipients_failed")
	broadcast(4, "paused", "bounce_rate_high")
	broadcast(5, "sending", "send_limit_reached")
	broadcast(6, "paused", "paused_by_owner")
	broadcast(7, "canceled", "")

	require.NoError(t, (&addBroadcastNotices{}).Up(db))

	type row struct {
		ID          int
		StartedBy   *int
		NoticeState string
		NoticeCount int
	}
	var rows []row
	require.NoError(t, db.Raw(`SELECT id, started_by, notice_state, notice_count FROM mail_broadcasts ORDER BY id`).Scan(&rows).Error)
	require.Len(t, rows, 7)
	require.Nil(t, rows[0].StartedBy, "a draft was never started")
	wantStates := []string{"", "sent", "failed", "bounce_rate_high", "send_limit_reached", "", ""}
	for i, got := range rows {
		require.Equal(t, wantStates[i], got.NoticeState, "broadcast %d", got.ID)
		require.Zero(t, got.NoticeCount, "broadcast %d", got.ID)
		if i > 0 {
			require.NotNil(t, got.StartedBy, "broadcast %d", got.ID)
			require.Equal(t, 901, *got.StartedBy, "broadcast %d", got.ID)
		}
	}

	require.NoError(t, (&addBroadcastNotices{}).Down(db))
	require.NoError(t, (&addBroadcastNotices{}).Up(db))
}
