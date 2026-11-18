package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_19_090000_add_mailbox_access", &addMailboxAccess{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailboxAccess struct {
	migration.BaseMigration
}

// Up gives every mailbox a type, adds per-mailbox members keyed by organization membership, and
// records an access history. Member rows cascade with the membership, so leaving the
// organization removes them and a rejoin starts without them.
//
// Existing mailboxes stay shared. Each one owned by an organization gets its creator's
// membership in that organization as a manage member, when the creator is still a member, and
// one created event. Mailboxes with a NULL organization stay invisible and get nothing.
func (m *addMailboxAccess) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes
		  ADD COLUMN type varchar(16) NOT NULL DEFAULT 'shared',
		  ADD COLUMN owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  ADD COLUMN owner_left_at timestamptz,
		  ADD COLUMN sealed_before timestamptz,
		  ADD COLUMN webhooks_enabled boolean NOT NULL DEFAULT false,
		  ADD CONSTRAINT chk_mailboxes_type CHECK (type IN ('personal','shared')),
		  ADD CONSTRAINT chk_mailboxes_personal_webhooks CHECK (type = 'shared' OR NOT webhooks_enabled)`,
		`CREATE INDEX idx_mailboxes_owner ON mailboxes (organization_id, owner_user_id) WHERE deleted_at IS NULL AND type = 'personal'`,
		`CREATE TABLE mailbox_members (
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  membership_id bigint NOT NULL REFERENCES organization_memberships(id) ON DELETE CASCADE,
		  user_id bigint NOT NULL,
		  permission varchar(16) NOT NULL CHECK (permission IN ('view','send','manage')),
		  granted_by bigint NOT NULL,
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL,
		  PRIMARY KEY (mailbox_id, membership_id))`,
		`CREATE INDEX idx_mailbox_members_membership ON mailbox_members (membership_id, mailbox_id)`,
		`CREATE TABLE mailbox_access_events (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  type varchar(32) NOT NULL,
		  actor_user_id bigint,
		  subject_user_id bigint,
		  detail jsonb NOT NULL DEFAULT '{}',
		  created_at timestamptz NOT NULL)`,
		`CREATE INDEX idx_mailbox_access_events_mailbox ON mailbox_access_events (mailbox_id, id DESC)`,
		`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at)
		 SELECT mailboxes.id, membership.id, membership.user_id, 'manage', mailboxes.created_by, now(), now()
		 FROM mailboxes
		 JOIN organization_memberships membership
		   ON membership.organization_id = mailboxes.organization_id AND membership.user_id = mailboxes.created_by
		 WHERE mailboxes.organization_id IS NOT NULL AND mailboxes.deleted_at IS NULL`,
		`INSERT INTO mailbox_access_events (organization_id, mailbox_id, type, actor_user_id, subject_user_id, detail, created_at)
		 SELECT mailboxes.organization_id, mailboxes.id, 'created', NULL, member.user_id,
		   CASE WHEN member.membership_id IS NULL
		     THEN jsonb_build_object('type', 'shared', 'backfilled', true)
		     ELSE jsonb_build_object('type', 'shared', 'backfilled', true,
		       'membership_id', member.membership_id, 'permission', 'manage')
		   END,
		   now()
		 FROM mailboxes
		 LEFT JOIN mailbox_members member ON member.mailbox_id = mailboxes.id
		 WHERE mailboxes.organization_id IS NOT NULL AND mailboxes.deleted_at IS NULL`,
	)
}

// Down drops the access history, the members, and the mailbox type columns.
func (m *addMailboxAccess) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS mailbox_access_events`,
		`DROP TABLE IF EXISTS mailbox_members`,
		`DROP INDEX IF EXISTS idx_mailboxes_owner`,
		`ALTER TABLE mailboxes
		  DROP CONSTRAINT IF EXISTS chk_mailboxes_personal_webhooks,
		  DROP CONSTRAINT IF EXISTS chk_mailboxes_type,
		  DROP COLUMN IF EXISTS webhooks_enabled,
		  DROP COLUMN IF EXISTS sealed_before,
		  DROP COLUMN IF EXISTS owner_left_at,
		  DROP COLUMN IF EXISTS owner_user_id,
		  DROP COLUMN IF EXISTS type`,
	)
}
