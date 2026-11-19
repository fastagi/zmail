package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_20_090000_add_send_queue", &addSendQueue{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addSendQueue struct {
	migration.BaseMigration
}

// Up lets a direct send wait for room in the organization's send limit. queued_reason marks a
// waiting message, send_attempted_at is when the provider call started (the send limit counts by
// it), and send_lease_until keeps a waiting message to one worker. Every outbound message stored
// so far was attempted when it was created.
func (m *addSendQueue) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_messages
		  ADD COLUMN queued_reason varchar(32),
		  ADD COLUMN send_attempted_at timestamptz,
		  ADD COLUMN send_lease_until timestamptz`,
		`UPDATE mail_messages SET send_attempted_at = created_at WHERE direction = 'outbound'`,
		`CREATE INDEX idx_mail_messages_waiting ON mail_messages (id) WHERE queued_reason IS NOT NULL`,
		`CREATE INDEX idx_mail_messages_send_attempted ON mail_messages (send_attempted_at) WHERE direction = 'outbound'`,
	)
}

// Down removes the queue. A message still waiting was never attempted, so it is removed too:
// the old schema has no way to say it has not been sent.
func (m *addSendQueue) Down(db *gorm.DB) error {
	return execStatements(db,
		`DELETE FROM mail_messages WHERE queued_reason IS NOT NULL`,
		`DROP INDEX IF EXISTS idx_mail_messages_send_attempted`,
		`DROP INDEX IF EXISTS idx_mail_messages_waiting`,
		`ALTER TABLE mail_messages
		  DROP COLUMN send_lease_until,
		  DROP COLUMN send_attempted_at,
		  DROP COLUMN queued_reason`,
	)
}
