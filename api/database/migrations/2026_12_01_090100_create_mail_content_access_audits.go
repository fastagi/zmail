package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_01_090100_create_mail_content_access_audits", &createMailContentAccessAudits{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailContentAccessAudits struct {
	migration.BaseMigration
}

// Up adds the record of every operator read of a broadcast body: who, when, why, and which
// organization and message. It never holds a body, subject, code, or recipient address. The IDs
// carry no foreign key so a record outlives the message, mailbox, organization, or account it
// names. It only creates a new table, so it takes no lock on an existing one.
func (m *createMailContentAccessAudits) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_content_access_audits (
		  id bigserial PRIMARY KEY,
		  viewer_user_id bigint NOT NULL,
		  organization_id bigint NOT NULL,
		  message_id bigint NOT NULL,
		  reason varchar(500) NOT NULL CHECK (btrim(reason) <> ''),
		  accessed_at timestamptz NOT NULL
		)`,
		`CREATE INDEX idx_mail_content_access_audits_time
		  ON mail_content_access_audits (accessed_at DESC, id DESC)`,
		`CREATE INDEX idx_mail_content_access_audits_organization
		  ON mail_content_access_audits (organization_id, accessed_at DESC, id DESC)`,
		`CREATE INDEX idx_mail_content_access_audits_viewer
		  ON mail_content_access_audits (viewer_user_id, accessed_at DESC, id DESC)`,
	)
}

// Down drops the table and the access records in it.
func (m *createMailContentAccessAudits) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS mail_content_access_audits`)
}
