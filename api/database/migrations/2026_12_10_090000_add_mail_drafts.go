package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_10_090000_add_mail_drafts", &addMailDrafts{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailDrafts struct {
	migration.BaseMigration
}

// Up adds mail_drafts: messages members saved without sending, each visible to its writer only.
// The new table locks nothing existing; the mailbox and user indexes serve the cascades.
func (m *addMailDrafts) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_drafts (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  to_recipients text NOT NULL CHECK (char_length(to_recipients) <= 4000),
		  cc_recipients text NOT NULL CHECK (char_length(cc_recipients) <= 4000),
		  subject text NOT NULL CHECK (char_length(subject) <= 998),
		  body text NOT NULL CHECK (char_length(body) <= 100000),
		  in_reply_to text NOT NULL CHECK (char_length(in_reply_to) <= 998),
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL)`,
		// The table is new and empty, so its plain index builds lock nothing in use.
		`-- fastbuild: blocking-index-reviewed
		CREATE INDEX idx_mail_drafts_owner ON mail_drafts (organization_id, user_id, updated_at DESC, id DESC)`,
		`CREATE INDEX idx_mail_drafts_user ON mail_drafts (user_id)`,
		`CREATE INDEX idx_mail_drafts_mailbox ON mail_drafts (mailbox_id)`,
	)
}

// Down drops the table and every saved draft with it.
func (m *addMailDrafts) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS mail_drafts`)
}
