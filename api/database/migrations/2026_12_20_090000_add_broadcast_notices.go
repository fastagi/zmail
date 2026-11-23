package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_20_090000_add_broadcast_notices", &addBroadcastNotices{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addBroadcastNotices struct {
	migration.BaseMigration
}

// Up records who started each broadcast and which of its states that person was last told about,
// so the mail worker sends one notice per finish or automatic pause.
//
// A broadcast that is no longer a draft is backfilled with its creator as its starter, the closest
// record there is. Every existing broadcast starts with its current state as already told, so the
// deploy does not mail anyone about broadcasts that ended or paused before it.
func (m *addBroadcastNotices) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_broadcasts
		  ADD COLUMN started_by bigint,
		  ADD COLUMN notice_state varchar(32) NOT NULL DEFAULT '',
		  ADD COLUMN notice_count integer NOT NULL DEFAULT 0`,
		`UPDATE mail_broadcasts SET started_by = created_by WHERE status <> 'draft'`,
		`UPDATE mail_broadcasts SET notice_state = CASE
		    WHEN status = 'sent' THEN 'sent'
		    WHEN status = 'canceled' AND stop_reason = 'all_recipients_failed' THEN 'failed'
		    WHEN status = 'paused' AND stop_reason IN ('bounce_rate_high', 'complaint_rate_high') THEN stop_reason
		    WHEN status = 'sending' AND stop_reason = 'send_limit_reached' THEN 'send_limit_reached'
		    ELSE '' END`,
	)
}

// Down drops the starter and the notice state; notices stop.
func (m *addBroadcastNotices) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_broadcasts
		  DROP COLUMN IF EXISTS notice_count,
		  DROP COLUMN IF EXISTS notice_state,
		  DROP COLUMN IF EXISTS started_by`,
	)
}
