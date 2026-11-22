package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_13_090000_add_mail_deleted_sends", &addMailDeletedSends{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailDeletedSends struct {
	migration.BaseMigration
}

// Up adds mail_deleted_sends: the send attempts of messages deleted for good while they still
// count against the organization's rolling send window, so deleting sent mail frees no room in a
// send limit and hides no bounce or complaint. The new table locks nothing existing.
func (m *addMailDeletedSends) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_deleted_sends (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  mailbox_id bigint NOT NULL,
		  message_id varchar(998) NOT NULL DEFAULT '',
		  send_lane varchar(16) NOT NULL,
		  send_attempted_at timestamptz NOT NULL,
		  bounced boolean NOT NULL DEFAULT false,
		  complained boolean NOT NULL DEFAULT false)`,
		`CREATE INDEX idx_mail_deleted_sends_lane ON mail_deleted_sends (organization_id, send_lane, send_attempted_at)`,
		`CREATE INDEX idx_mail_deleted_sends_mailbox ON mail_deleted_sends (mailbox_id, send_attempted_at)`,
		`CREATE INDEX idx_mail_deleted_sends_message_id ON mail_deleted_sends (message_id)`,
		`CREATE INDEX idx_mail_deleted_sends_attempted ON mail_deleted_sends (send_attempted_at)`,
	)
}

// Down drops the table; deleted messages stop counting against the send window.
func (m *addMailDeletedSends) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS mail_deleted_sends`)
}
