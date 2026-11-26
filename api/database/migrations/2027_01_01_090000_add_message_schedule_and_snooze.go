package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2027_01_01_090000_add_message_schedule_and_snooze", &addMessageScheduleAndSnooze{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMessageScheduleAndSnooze struct {
	migration.BaseMigration
}

// Up adds scheduled_at for single-email scheduled sends and snoozed_until for snoozing inbox
// messages, with partial indexes so workers only scan scheduled or snoozed rows.
func (m *addMessageScheduleAndSnooze) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_messages
		  ADD COLUMN IF NOT EXISTS scheduled_at timestamptz,
		  ADD COLUMN IF NOT EXISTS snoozed_until timestamptz`,
		`CREATE INDEX IF NOT EXISTS idx_mail_messages_scheduled_at
		  ON mail_messages (scheduled_at) WHERE scheduled_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_mail_messages_snoozed_until
		  ON mail_messages (snoozed_until) WHERE snoozed_until IS NOT NULL`,
	)
}

// Down removes the schedule and snooze columns and their partial indexes.
func (m *addMessageScheduleAndSnooze) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP INDEX IF EXISTS idx_mail_messages_snoozed_until`,
		`DROP INDEX IF EXISTS idx_mail_messages_scheduled_at`,
		`ALTER TABLE mail_messages
		  DROP COLUMN IF EXISTS snoozed_until,
		  DROP COLUMN IF EXISTS scheduled_at`,
	)
}
