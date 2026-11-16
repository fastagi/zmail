package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/fastmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_17_090000_move_mailboxes_to_organizations", &moveMailboxesToOrganizations{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type moveMailboxesToOrganizations struct {
	migration.BaseMigration
}

// Up makes the organization the owner of a mailbox. The former owner becomes created_by, kept
// for the record only. An existing mailbox moves to the organization its creator joined first;
// one whose creator belongs to no organization keeps its rows with a NULL organization, so no
// mail is destroyed, and stays invisible until an operator assigns it.
func (m *moveMailboxesToOrganizations) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes ADD COLUMN organization_id bigint`,
		`UPDATE mailboxes SET organization_id = (
		    SELECT membership.organization_id
		    FROM organization_memberships membership
		    WHERE membership.user_id = mailboxes.user_id
		    ORDER BY membership.created_at, membership.id
		    LIMIT 1
		)`,
		`ALTER TABLE mailboxes DROP CONSTRAINT fk_mailboxes_user`,
		`DROP INDEX idx_mailboxes_user`,
		`ALTER TABLE mailboxes RENAME COLUMN user_id TO created_by`,
		`CREATE INDEX idx_mailboxes_organization ON mailboxes (organization_id, id) WHERE deleted_at IS NULL`,
		`ALTER TABLE mailboxes ADD CONSTRAINT fk_mailboxes_organization FOREIGN KEY (organization_id) REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE CASCADE`,
	)
}

// Down returns ownership to the creating user.
func (m *moveMailboxesToOrganizations) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes DROP CONSTRAINT fk_mailboxes_organization`,
		`DROP INDEX idx_mailboxes_organization`,
		`ALTER TABLE mailboxes RENAME COLUMN created_by TO user_id`,
		`CREATE INDEX idx_mailboxes_user ON mailboxes (user_id) WHERE deleted_at IS NULL`,
		`ALTER TABLE mailboxes ADD CONSTRAINT fk_mailboxes_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE RESTRICT`,
		`ALTER TABLE mailboxes DROP COLUMN organization_id`,
	)
}
