package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_04_090000_add_mailbox_origin", &addMailboxOrigin{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailboxOrigin struct {
	migration.BaseMigration
}

// Up separates who may use a mailbox (type: personal or shared) from where its mail comes from
// (origin: zmail, or connected for a read-only copy of an outside account):
//
//   - mailboxes.origin defaults to zmail, which PostgreSQL 11+ records in the catalog without
//     rewriting the table.
//   - Every external mailbox becomes personal and connected, soft-deleted rows included, and the
//     type CHECK is back to personal and shared.
//   - The global unique address index covers Zmail's own addresses only: a connected mailbox's
//     address is an outside account's, never Zmail's to deliver to.
//
// idx_mailboxes_owner (type = 'personal') now also serves connected personal mailboxes, which the
// owner's own lookups want, and chk_mailboxes_personal_webhooks keeps webhooks off every personal
// mailbox, connected or not. mailboxes is bounded per organization, so the CHECK scans and the index
// rebuild inside the transaction are short.
func (m *addMailboxOrigin) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes DROP CONSTRAINT chk_mailboxes_type,
		  ADD COLUMN origin varchar(16) NOT NULL DEFAULT 'zmail',
		  ADD CONSTRAINT chk_mailboxes_origin CHECK (origin IN ('zmail','connected'))`,
		`UPDATE mailboxes SET type = 'personal', origin = 'connected' WHERE type = 'external'`,
		`ALTER TABLE mailboxes ADD CONSTRAINT chk_mailboxes_type CHECK (type IN ('personal','shared'))`,
		`DROP INDEX idx_mailboxes_address`,
		`CREATE UNIQUE INDEX idx_mailboxes_address ON mailboxes (address) WHERE origin = 'zmail'`,
	)
}

// Down turns connected personal mailboxes back into external ones and drops the origin. It fails
// while a connected shared mailbox exists, which the older schema cannot express.
func (m *addMailboxOrigin) Down(db *gorm.DB) error {
	return execStatements(db,
		`DO $$
		BEGIN
		  IF EXISTS (SELECT 1 FROM mailboxes WHERE origin = 'connected' AND type <> 'personal') THEN
		    RAISE EXCEPTION 'a connected shared mailbox exists; the older schema cannot express it';
		  END IF;
		END $$`,
		`DROP INDEX idx_mailboxes_address`,
		`ALTER TABLE mailboxes DROP CONSTRAINT chk_mailboxes_type,
		  ADD CONSTRAINT chk_mailboxes_type CHECK (type IN ('personal','shared','external'))`,
		`UPDATE mailboxes SET type = 'external' WHERE origin = 'connected'`,
		`ALTER TABLE mailboxes DROP CONSTRAINT chk_mailboxes_origin, DROP COLUMN origin`,
		`CREATE UNIQUE INDEX idx_mailboxes_address ON mailboxes (address) WHERE type <> 'external'`,
	)
}
