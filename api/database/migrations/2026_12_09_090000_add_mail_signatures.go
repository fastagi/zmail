package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_09_090000_add_mail_signatures", &addMailSignatures{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailSignatures struct {
	migration.BaseMigration
}

// Up adds mail_signatures: one row per member and mailbox with a saved plain-text signature. The
// new table locks nothing existing; the user index serves the cascade when a user is deleted.
func (m *addMailSignatures) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_signatures (
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		  body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL,
		  PRIMARY KEY (mailbox_id, user_id))`,
		`CREATE INDEX idx_mail_signatures_user ON mail_signatures (user_id)`,
	)
}

// Down drops the table and every saved signature with it.
func (m *addMailSignatures) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS mail_signatures`)
}
