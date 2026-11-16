package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_17_100000_create_mail_message_events", &createMailMessageEvents{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailMessageEvents struct {
	migration.BaseMigration
}

// Up adds the delivery history of a message: what the provider reported for each recipient,
// at send time and afterwards. mail_messages.last_event summarizes it for listings.
func (m *createMailMessageEvents) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_message_events (
		    id bigserial NOT NULL,
		    message_id bigint NOT NULL,
		    type varchar(16) NOT NULL,
		    recipient varchar(320) NOT NULL DEFAULT '',
		    detail varchar(255) NOT NULL DEFAULT '',
		    provider_event_id varchar(128),
		    occurred_at timestamptz NOT NULL,
		    created_at timestamptz,
		    CONSTRAINT mail_message_events_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_mail_message_events_type CHECK (type IN ('accepted', 'delivered', 'deferred', 'bounced', 'failed', 'rejected', 'complained'))
		)`,
		`CREATE INDEX idx_mail_message_events_message ON mail_message_events (message_id, occurred_at, id)`,
		`CREATE UNIQUE INDEX idx_mail_message_events_provider ON mail_message_events (provider_event_id) WHERE provider_event_id IS NOT NULL`,
		`ALTER TABLE mail_message_events ADD CONSTRAINT fk_mail_message_events_message FOREIGN KEY (message_id) REFERENCES mail_messages(id) ON UPDATE CASCADE ON DELETE CASCADE`,
		`ALTER TABLE mail_messages ADD COLUMN last_event varchar(16) NOT NULL DEFAULT ''`,
		`CREATE INDEX idx_mail_messages_provider_id ON mail_messages (message_id) WHERE direction = 'outbound' AND message_id <> ''`,
	)
}

// Down removes the delivery history.
func (m *createMailMessageEvents) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP INDEX IF EXISTS idx_mail_messages_provider_id`,
		`ALTER TABLE mail_messages DROP COLUMN IF EXISTS last_event`,
		`DROP TABLE IF EXISTS mail_message_events CASCADE`,
	)
}
