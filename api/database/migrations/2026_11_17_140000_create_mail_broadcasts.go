package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_17_140000_create_mail_broadcasts", &createMailBroadcasts{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailBroadcasts struct {
	migration.BaseMigration
}

// Up adds broadcasts and their recipients. A broadcast keeps a snapshot of its template from the
// moment sending starts, and its recipients are fixed then, so later edits to the template or
// the list do not change a broadcast in progress. Messages sent to recipients are ordinary
// mail_messages and outlive the broadcast.
func (m *createMailBroadcasts) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_broadcasts (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    name varchar(100) NOT NULL,
		    mailbox_id bigint NOT NULL,
		    template_id bigint NOT NULL,
		    list_id bigint NOT NULL,
		    unsubscribe_label varchar(50) NOT NULL DEFAULT '',
		    status varchar(16) NOT NULL DEFAULT 'draft',
		    subject varchar(998) NOT NULL DEFAULT '',
		    html_body text NOT NULL DEFAULT '',
		    text_body text NOT NULL DEFAULT '',
		    scheduled_at timestamptz,
		    started_at timestamptz,
		    finished_at timestamptz,
		    stop_reason varchar(64) NOT NULL DEFAULT '',
		    created_by bigint NOT NULL,
		    created_at timestamptz,
		    updated_at timestamptz,
		    CONSTRAINT mail_broadcasts_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_mail_broadcasts_status CHECK (status IN ('draft', 'scheduled', 'sending', 'sent', 'canceled'))
		)`,
		`CREATE INDEX idx_mail_broadcasts_listing ON mail_broadcasts (organization_id, id)`,
		`CREATE INDEX idx_mail_broadcasts_due ON mail_broadcasts (status, scheduled_at)`,
		`ALTER TABLE mail_broadcasts ADD CONSTRAINT fk_mail_broadcasts_organization FOREIGN KEY (organization_id) REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE CASCADE`,
		`CREATE TABLE mail_broadcast_recipients (
		    id bigserial NOT NULL,
		    broadcast_id bigint NOT NULL,
		    contact_id bigint NOT NULL,
		    email varchar(320) NOT NULL,
		    status varchar(16) NOT NULL DEFAULT 'pending',
		    message_id bigint,
		    failure_code varchar(120) NOT NULL DEFAULT '',
		    lease_until timestamptz,
		    created_at timestamptz,
		    updated_at timestamptz,
		    CONSTRAINT mail_broadcast_recipients_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_mail_broadcast_recipients_status CHECK (status IN ('pending', 'sent', 'failed', 'skipped', 'canceled'))
		)`,
		`CREATE UNIQUE INDEX idx_mail_broadcast_recipients_contact ON mail_broadcast_recipients (broadcast_id, contact_id)`,
		`CREATE INDEX idx_mail_broadcast_recipients_pending ON mail_broadcast_recipients (broadcast_id, id) WHERE status = 'pending'`,
		`CREATE INDEX idx_mail_broadcast_recipients_message ON mail_broadcast_recipients (message_id) WHERE message_id IS NOT NULL`,
		`ALTER TABLE mail_broadcast_recipients ADD CONSTRAINT fk_mail_broadcast_recipients_broadcast FOREIGN KEY (broadcast_id) REFERENCES mail_broadcasts(id) ON UPDATE CASCADE ON DELETE CASCADE`,
	)
}

// Down removes broadcasts.
func (m *createMailBroadcasts) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS mail_broadcast_recipients CASCADE`,
		`DROP TABLE IF EXISTS mail_broadcasts CASCADE`,
	)
}
