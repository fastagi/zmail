package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_22_090100_add_mail_inbox_index", &addMailInboxIndex{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailInboxIndex struct {
	migration.BaseMigration
}

// Up adds the index the unified inbox reads: per mailbox and direction, newest first by
// (created_at, id). It serves All mail and Sent (placement filtered during the scan) and Today (a
// created_at range across placements). The single-mailbox listing keeps idx_mail_messages_listing.
//
// The index covers columns that already exist, so a large installation can build it beforehand
// with CREATE INDEX CONCURRENTLY (see UPGRADING.md); IF NOT EXISTS then skips it here instead of
// holding the write lock on mail_messages while it builds.
func (m *addMailInboxIndex) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE INDEX IF NOT EXISTS idx_mail_messages_inbox_time
		  ON mail_messages (mailbox_id, direction, created_at DESC, id DESC)`,
	)
}

// Down drops the inbox index.
func (m *addMailInboxIndex) Down(db *gorm.DB) error {
	return execStatements(db, `DROP INDEX IF EXISTS idx_mail_messages_inbox_time`)
}
