package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_25_090000_add_mailbox_directory_index", &addMailboxDirectoryIndex{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailboxDirectoryIndex struct {
	migration.BaseMigration
}

// Up adds the index the mailbox directory's send counts read: each mailbox's outbound attempts by
// lane and attempt time, so counting the personal-lane attempts of one page of mailboxes in the
// last 24 hours is an index range per mailbox instead of a scan of the organization's mail.
//
// The index covers columns that already exist, so a large installation can build it beforehand
// with CREATE INDEX CONCURRENTLY (see UPGRADING.md); IF NOT EXISTS then skips it here instead of
// holding the write lock on mail_messages while it builds.
func (m *addMailboxDirectoryIndex) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE INDEX IF NOT EXISTS idx_mail_messages_mailbox_send_attempted
		  ON mail_messages (mailbox_id, send_lane, send_attempted_at)
		  WHERE direction = 'outbound'`,
	)
}

// Down drops the directory index.
func (m *addMailboxDirectoryIndex) Down(db *gorm.DB) error {
	return execStatements(db, `DROP INDEX IF EXISTS idx_mail_messages_mailbox_send_attempted`)
}
