package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_25_090000_add_mail_submission_fields", &addMailSubmissionFields{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailSubmissionFields struct {
	migration.BaseMigration
}

// Up adds what mail apps submit beyond a web send: the blind copies of a message, kept apart from
// cc and shown only to the member who sent it, that member, and the Content-ID and inline placement
// of an attachment, so an image an HTML body shows by cid: keeps working. Every new column has a
// constant default, so existing rows are not rewritten. sent_by_membership_id has no foreign key:
// membership ids are never reused, and a key would validate against every stored message.
func (m *addMailSubmissionFields) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_messages
		    ADD COLUMN bcc_addresses text NOT NULL DEFAULT '[]',
		    ADD COLUMN sent_by_membership_id bigint`,
		`ALTER TABLE mail_attachments
		    ADD COLUMN content_id varchar(250) NOT NULL DEFAULT '',
		    ADD COLUMN inline boolean NOT NULL DEFAULT false,
		    ADD CONSTRAINT chk_mail_attachments_content_id CHECK (content_id !~ '[<>[:space:]]')`,
	)
}

// Down drops the columns; blind copies and inline placement are forgotten.
func (m *addMailSubmissionFields) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_attachments
		    DROP CONSTRAINT IF EXISTS chk_mail_attachments_content_id,
		    DROP COLUMN IF EXISTS inline,
		    DROP COLUMN IF EXISTS content_id`,
		`ALTER TABLE mail_messages
		    DROP COLUMN IF EXISTS sent_by_membership_id,
		    DROP COLUMN IF EXISTS bcc_addresses`,
	)
}
