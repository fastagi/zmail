package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_19_100000_add_mailbox_pending_owner", &addMailboxPendingOwner{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailboxPendingOwner struct {
	migration.BaseMigration
}

// Up lets a personal mailbox without an owner be reserved for an invited email. Only a personal
// mailbox without an owner may carry a reservation, and the claim on joining looks it up by
// organization and email.
func (m *addMailboxPendingOwner) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes ADD COLUMN pending_owner_email varchar(320)`,
		`ALTER TABLE mailboxes ADD CONSTRAINT chk_mailboxes_pending_owner
		    CHECK (pending_owner_email IS NULL OR (type = 'personal' AND owner_user_id IS NULL))`,
		`CREATE INDEX idx_mailboxes_pending_owner ON mailboxes (organization_id, pending_owner_email)
		    WHERE pending_owner_email IS NOT NULL AND deleted_at IS NULL`,
	)
}

// Down drops the reservations; reserved mailboxes stay as personal mailboxes without an owner.
func (m *addMailboxPendingOwner) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP INDEX IF EXISTS idx_mailboxes_pending_owner`,
		`ALTER TABLE mailboxes DROP CONSTRAINT IF EXISTS chk_mailboxes_pending_owner`,
		`ALTER TABLE mailboxes DROP COLUMN IF EXISTS pending_owner_email`,
	)
}
