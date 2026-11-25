package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_31_090000_add_mailbox_pending_members", &addMailboxPendingMembers{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailboxPendingMembers struct {
	migration.BaseMigration
}

// Up stores shared-mailbox member grants assigned to an invited email before the invitee joins.
// When the invitee accepts the organization invitation, MemberJoined converts these rows into
// mailbox_members grants and deletes the claimed pending rows.
func (m *addMailboxPendingMembers) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE IF NOT EXISTS mailbox_pending_members (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  email varchar(320) NOT NULL,
		  permission varchar(16) NOT NULL CHECK (permission IN ('view','send','manage')),
		  granted_by bigint NOT NULL,
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL,
		  UNIQUE (mailbox_id, email)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mailbox_pending_members_org_email
		  ON mailbox_pending_members (organization_id, email)`,
	)
}

// Down drops the pending mailbox member grants table.
func (m *addMailboxPendingMembers) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS mailbox_pending_members`,
	)
}
