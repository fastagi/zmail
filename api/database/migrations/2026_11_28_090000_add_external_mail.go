package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_28_090000_add_external_mail", &addExternalMail{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addExternalMail struct {
	migration.BaseMigration
}

// Up adds read-only external mailboxes: the mailbox type external, the source and external id of
// each message, and one external_mail_connections row per external mailbox, which holds the sealed
// refresh token and the sync cursor of the outside account.
//
// mail_messages.source has a constant default, which PostgreSQL 11+ records in the catalog without
// rewriting the table, and its CHECK is added NOT VALID, so no row is scanned: every existing row
// takes the default. external_id is nullable without a default, also catalog only. The unique
// index on (mailbox_id, external_id) is the one statement that reads the whole table while holding
// the write lock; a large installation builds it beforehand with CREATE INDEX CONCURRENTLY (see
// UPGRADING.md), and IF NOT EXISTS then skips it here. Replacing the mailboxes type CHECK scans
// mailboxes, which is bounded per organization.
func (m *addExternalMail) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes DROP CONSTRAINT chk_mailboxes_type,
		  ADD CONSTRAINT chk_mailboxes_type CHECK (type IN ('personal','shared','external'))`,
		`ALTER TABLE mail_messages
		  ADD COLUMN IF NOT EXISTS source varchar(16) NOT NULL DEFAULT 'zmail',
		  ADD COLUMN IF NOT EXISTS external_id varchar(255)`,
		`ALTER TABLE mail_messages
		  ADD CONSTRAINT chk_mail_messages_source CHECK (source IN ('zmail','gmail','outlook')) NOT VALID`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_mail_messages_external
		  ON mail_messages (mailbox_id, external_id) WHERE external_id IS NOT NULL`,
		`CREATE TABLE external_mail_connections (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  mailbox_id bigint NOT NULL UNIQUE REFERENCES mailboxes(id) ON DELETE CASCADE,
		  provider varchar(16) NOT NULL CHECK (provider IN ('gmail','outlook')),
		  account_address varchar(320) NOT NULL,
		  owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  status varchar(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active','needs_reconnect','revoked')),
		  last_synced_at timestamptz,
		  last_error_code varchar(64),
		  encrypted_refresh_token text NOT NULL DEFAULT '',
		  sync_cursor text NOT NULL DEFAULT '',
		  backfill_days smallint NOT NULL DEFAULT 14 CHECK (backfill_days BETWEEN 1 AND 90),
		  next_sync_at timestamptz NOT NULL,
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL)`,
		`CREATE UNIQUE INDEX idx_external_mail_connections_account
		  ON external_mail_connections (organization_id, provider, lower(account_address))`,
		`CREATE INDEX idx_external_mail_connections_owner ON external_mail_connections (organization_id, owner_user_id)`,
		`CREATE INDEX idx_external_mail_connections_due ON external_mail_connections (next_sync_at) WHERE status = 'active'`,
	)
}

// Down drops the connections, the message source columns, and the external mailbox type. It fails
// while an external mailbox row exists, because the restored type CHECK rejects it; delete those
// mailboxes first.
func (m *addExternalMail) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS external_mail_connections`,
		`DROP INDEX IF EXISTS idx_mail_messages_external`,
		`ALTER TABLE mail_messages
		  DROP CONSTRAINT IF EXISTS chk_mail_messages_source,
		  DROP COLUMN IF EXISTS external_id,
		  DROP COLUMN IF EXISTS source`,
		`ALTER TABLE mailboxes DROP CONSTRAINT chk_mailboxes_type,
		  ADD CONSTRAINT chk_mailboxes_type CHECK (type IN ('personal','shared'))`,
	)
}
