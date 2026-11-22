package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_11_090000_add_mail_thread_assignments", &addMailThreadAssignments{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailThreadAssignments struct {
	migration.BaseMigration
}

// Up adds mail_thread_assignments: who works a shared mailbox's conversation and whether it is
// done. The new table locks nothing existing; the user indexes serve the SET NULL on user deletion.
func (m *addMailThreadAssignments) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_thread_assignments (
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  thread_id bigint NOT NULL,
		  assignee_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  status text NOT NULL CHECK (status IN ('open', 'closed')),
		  updated_by_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  updated_at timestamptz NOT NULL,
		  PRIMARY KEY (mailbox_id, thread_id))`,
		`CREATE INDEX idx_mail_thread_assignments_assignee ON mail_thread_assignments (assignee_user_id)`,
		`CREATE INDEX idx_mail_thread_assignments_updated_by ON mail_thread_assignments (updated_by_user_id)`,
	)
}

// Down drops the table and every assignment with it.
func (m *addMailThreadAssignments) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS mail_thread_assignments`)
}
