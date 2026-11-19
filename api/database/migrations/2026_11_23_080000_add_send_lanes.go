package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_23_080000_add_send_lanes", &addSendLanes{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addSendLanes struct {
	migration.BaseMigration
}

// Up splits the organization's send limit into lanes. send_lane says which limit a stored message
// counts against: personal (direct sends, replies, API and template test sends) or broadcast (each
// broadcast recipient). Transactional mail is counted in its own table. Every message a broadcast
// recipient points at becomes broadcast, so each lane's usage carries over the upgrade. The lane
// index covers what the shared send-attempt index served, so that index is dropped.
func (m *addSendLanes) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_messages
		  ADD COLUMN send_lane varchar(16) NOT NULL DEFAULT 'personal'
		  CONSTRAINT chk_mail_messages_send_lane CHECK (send_lane IN ('personal', 'broadcast'))`,
		`UPDATE mail_messages m SET send_lane = 'broadcast'
		  FROM mail_broadcast_recipients r WHERE r.message_id = m.id`,
		`CREATE INDEX idx_mail_messages_lane_attempted ON mail_messages (send_lane, send_attempted_at) WHERE direction = 'outbound'`,
		`DROP INDEX IF EXISTS idx_mail_messages_send_attempted`,
	)
}

// Down returns to one shared limit: the old index comes back and the lane is forgotten, which
// loses nothing the old code reads.
func (m *addSendLanes) Down(db *gorm.DB) error {
	return execStatements(db,
		`CREATE INDEX IF NOT EXISTS idx_mail_messages_send_attempted ON mail_messages (send_attempted_at) WHERE direction = 'outbound'`,
		`DROP INDEX IF EXISTS idx_mail_messages_lane_attempted`,
		`ALTER TABLE mail_messages DROP COLUMN send_lane`,
	)
}
