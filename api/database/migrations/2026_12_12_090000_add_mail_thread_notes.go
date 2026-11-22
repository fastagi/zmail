package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_12_090000_add_mail_thread_notes", &addMailThreadNotes{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailThreadNotes struct {
	migration.BaseMigration
}

// Up adds mail_thread_notes: internal remarks members leave on a shared mailbox's conversation.
// The new table locks nothing existing; the author index serves the SET NULL on user deletion.
func (m *addMailThreadNotes) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_thread_notes (
		  id bigserial PRIMARY KEY,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  thread_id bigint NOT NULL,
		  author_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  body text NOT NULL,
		  created_at timestamptz NOT NULL)`,
		`CREATE INDEX idx_mail_thread_notes_thread ON mail_thread_notes (mailbox_id, thread_id, id)`,
		`CREATE INDEX idx_mail_thread_notes_author ON mail_thread_notes (author_user_id)`,
	)
}

// Down drops the table and every note with it.
func (m *addMailThreadNotes) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS mail_thread_notes`)
}
