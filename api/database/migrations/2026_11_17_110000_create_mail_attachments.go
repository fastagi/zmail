package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_17_110000_create_mail_attachments", &createMailAttachments{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailAttachments struct {
	migration.BaseMigration
}

// Up adds the files attached to a message. The content is stored in the row: attachments are
// bounded to a few megabytes per message, and deleting a message removes them with it.
// mail_messages.attachment_count lets listings show that a message has files without a join.
func (m *createMailAttachments) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_attachments (
		    id bigserial NOT NULL,
		    message_id bigint NOT NULL,
		    filename varchar(255) NOT NULL,
		    content_type varchar(127) NOT NULL,
		    size bigint NOT NULL,
		    content bytea NOT NULL,
		    created_at timestamptz,
		    CONSTRAINT mail_attachments_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_mail_attachments_size CHECK (size >= 0 AND size = octet_length(content))
		)`,
		`CREATE INDEX idx_mail_attachments_message ON mail_attachments (message_id)`,
		`ALTER TABLE mail_attachments ADD CONSTRAINT fk_mail_attachments_message FOREIGN KEY (message_id) REFERENCES mail_messages(id) ON UPDATE CASCADE ON DELETE CASCADE`,
		`ALTER TABLE mail_messages ADD COLUMN attachment_count bigint NOT NULL DEFAULT 0`,
	)
}

// Down removes attachments.
func (m *createMailAttachments) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_messages DROP COLUMN IF EXISTS attachment_count`,
		`DROP TABLE IF EXISTS mail_attachments CASCADE`,
	)
}
