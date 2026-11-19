package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_22_090000_add_mail_roles", &addMailRoles{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailRoles struct {
	migration.BaseMigration
}

// Up adds mail roles: an organization-level grouping of mailboxes for display, which grants and
// revokes nothing. Names are unique per organization ignoring case; a mailbox belongs to at most
// one role, and deleting a role leaves its mailboxes ungrouped. Every existing mailbox starts
// ungrouped. The foreign key on mailboxes.role_id only proves the role exists; the assignment
// command keeps it within the mailbox's organization.
func (m *addMailRoles) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_roles (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  name varchar(64) NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 64),
		  color varchar(16) NOT NULL DEFAULT 'gray'
		    CHECK (color IN ('gray','red','orange','yellow','green','teal','blue','purple','pink')),
		  description varchar(280) NOT NULL DEFAULT '',
		  owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL)`,
		`CREATE UNIQUE INDEX idx_mail_roles_name ON mail_roles (organization_id, lower(name))`,
		`CREATE INDEX idx_mail_roles_owner ON mail_roles (owner_user_id) WHERE owner_user_id IS NOT NULL`,
		`ALTER TABLE mailboxes ADD COLUMN role_id bigint REFERENCES mail_roles(id) ON DELETE SET NULL`,
		`CREATE INDEX idx_mailboxes_role ON mailboxes (role_id) WHERE role_id IS NOT NULL`,
	)
}

// Down drops the mailbox column and then the roles; the indexes go with them. Stored roles are
// lost, which only ungroups the mailboxes.
func (m *addMailRoles) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes DROP COLUMN IF EXISTS role_id`,
		`DROP TABLE IF EXISTS mail_roles`,
	)
}
