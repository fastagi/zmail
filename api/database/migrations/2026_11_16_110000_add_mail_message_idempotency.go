package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_16_110000_add_mail_message_idempotency", &addMailMessageIdempotency{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailMessageIdempotency struct {
	migration.BaseMigration
}

// Up lets a sender name an outbound message so a retry finds it instead of sending again.
func (m *addMailMessageIdempotency) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_messages ADD COLUMN idempotency_key varchar(255)`,
		`ALTER TABLE mail_messages ADD COLUMN idempotency_fingerprint varchar(64) NOT NULL DEFAULT ''`,
		`CREATE UNIQUE INDEX idx_mail_messages_idempotency ON mail_messages (mailbox_id, idempotency_key) WHERE idempotency_key IS NOT NULL`,
	)
}

// Down removes the idempotency columns.
func (m *addMailMessageIdempotency) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP INDEX IF EXISTS idx_mail_messages_idempotency`,
		`ALTER TABLE mail_messages DROP COLUMN IF EXISTS idempotency_fingerprint`,
		`ALTER TABLE mail_messages DROP COLUMN IF EXISTS idempotency_key`,
	)
}
