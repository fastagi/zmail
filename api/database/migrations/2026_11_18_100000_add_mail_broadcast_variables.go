package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_18_100000_add_mail_broadcast_variables", &addMailBroadcastVariables{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailBroadcastVariables struct {
	migration.BaseMigration
}

// Up stores the template values a broadcast shares with every recipient.
func (m *addMailBroadcastVariables) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_broadcasts ADD COLUMN variables jsonb NOT NULL DEFAULT '{}'`,
	)
}

// Down drops the shared values; broadcasts then fill templates from contacts only.
func (m *addMailBroadcastVariables) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_broadcasts DROP COLUMN IF EXISTS variables`,
	)
}
