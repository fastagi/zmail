package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_08_090000_add_connected_mailbox_send", &addConnectedMailboxSend{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addConnectedMailboxSend struct {
	migration.BaseMigration
}

// Up adds mailboxes.connected_send: a connected mailbox whose account was bound together with its
// SMTP server sends through that server. The constant default is recorded by PostgreSQL 11+
// without rewriting the table.
func (m *addConnectedMailboxSend) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes ADD COLUMN connected_send boolean NOT NULL DEFAULT false`,
	)
}

// Down drops the column; connected mailboxes are read-only again.
func (m *addConnectedMailboxSend) Down(db *gorm.DB) error {
	return execStatements(db, `ALTER TABLE mailboxes DROP COLUMN IF EXISTS connected_send`)
}
