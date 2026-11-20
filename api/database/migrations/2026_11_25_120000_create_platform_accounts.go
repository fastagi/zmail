package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_25_120000_create_platform_accounts", &createPlatformAccounts{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createPlatformAccounts struct {
	migration.BaseMigration
}

// Up adds the platform account ledger: which mailbox registered an account on an outside
// platform, which mail role it belongs to, who owns it, and its status. Platform and handle are
// unique per organization ignoring case. Deleting a role, mailbox, or user clears the reference.
//
// platform_account_events is the account's handover and status history. account_id carries no
// foreign key on purpose: deleting an account records a final event and the history outlives the
// row. It only creates new tables, so it takes no lock on an existing one.
func (m *createPlatformAccounts) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE platform_accounts (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  platform varchar(64) NOT NULL CHECK (char_length(btrim(platform)) BETWEEN 1 AND 64),
		  handle varchar(200) NOT NULL CHECK (char_length(btrim(handle)) BETWEEN 1 AND 200),
		  profile_url varchar(2048),
		  role_id bigint REFERENCES mail_roles(id) ON DELETE SET NULL,
		  mailbox_id bigint REFERENCES mailboxes(id) ON DELETE SET NULL,
		  owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  status varchar(32) NOT NULL DEFAULT 'active'
		    CHECK (status IN ('invited','awaiting_confirmation','active','timed_out','needs_review','retired')),
		  notes varchar(2000) NOT NULL DEFAULT '',
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL)`,
		`CREATE UNIQUE INDEX idx_platform_accounts_identity
		  ON platform_accounts (organization_id, lower(platform), lower(handle))`,
		`CREATE INDEX idx_platform_accounts_mailbox ON platform_accounts (mailbox_id) WHERE mailbox_id IS NOT NULL`,
		`CREATE INDEX idx_platform_accounts_owner ON platform_accounts (organization_id, owner_user_id) WHERE owner_user_id IS NOT NULL`,
		`CREATE INDEX idx_platform_accounts_role ON platform_accounts (role_id) WHERE role_id IS NOT NULL`,
		`CREATE TABLE platform_account_events (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  account_id bigint NOT NULL,
		  kind varchar(32) NOT NULL
		    CHECK (kind IN ('created','status_changed','owner_changed','mailbox_changed','deleted')),
		  actor_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  from_value varchar(200),
		  to_value varchar(200),
		  created_at timestamptz NOT NULL)`,
		`CREATE INDEX idx_platform_account_events_account ON platform_account_events (account_id, id)`,
	)
}

// Down drops the ledger and its history; stored accounts and events are lost.
func (m *createPlatformAccounts) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS platform_account_events`,
		`DROP TABLE IF EXISTS platform_accounts`,
	)
}
