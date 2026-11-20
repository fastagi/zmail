package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_24_090000_add_mail_unread_index", &addMailUnreadIndex{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailUnreadIndex struct {
	migration.BaseMigration
}

// Up adds the index unread counts read: inbound Inbox mail per mailbox, with created_at for the
// seal boundary, so counting a mailbox's unread mail is an index-only range of that mailbox
// anti-joined to the reader's reads. Without it the planner scans all of mail_messages, and the
// count slows as mailboxes the reader cannot read grow (design §14.3).
//
// The index covers columns that already exist, so a large installation can build it beforehand
// with CREATE INDEX CONCURRENTLY (see UPGRADING.md); IF NOT EXISTS then skips it here instead of
// holding the write lock on mail_messages while it builds.
func (m *addMailUnreadIndex) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE INDEX IF NOT EXISTS idx_mail_messages_unread
		  ON mail_messages (mailbox_id, id) INCLUDE (created_at)
		  WHERE direction = 'inbound' AND placement = 'default'`,
	)
}

// Down drops the unread index.
func (m *addMailUnreadIndex) Down(db *gorm.DB) error {
	return execStatements(db, `DROP INDEX IF EXISTS idx_mail_messages_unread`)
}
