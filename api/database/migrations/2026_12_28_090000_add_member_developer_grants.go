package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_28_090000_add_member_developer_grants", &addMemberDeveloperGrants{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMemberDeveloperGrants struct {
	migration.BaseMigration
}

// Up creates member_developer_grants: at most one developer grant per membership, removed with the
// membership. The scope is every verified domain of the organization or an explicit JSON list of
// domain names; a later column can hold single addresses. It only creates a table: the foreign
// keys take a brief SHARE ROW EXCLUSIVE lock on organization_memberships, organizations, and users.
// An invitation that carries developer access has a row in organization_invitation_developer_grants,
// removed with the invitation; a separate table leaves organization_invitations untouched.
func (m *addMemberDeveloperGrants) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE member_developer_grants (
		    membership_id bigint PRIMARY KEY,
		    organization_id bigint NOT NULL,
		    all_domains boolean NOT NULL DEFAULT false,
		    scope_domains text NOT NULL DEFAULT '[]',
		    granted_by bigint,
		    created_at timestamptz NOT NULL,
		    updated_at timestamptz NOT NULL,
		    CONSTRAINT fk_member_developer_grants_membership FOREIGN KEY (membership_id)
		        REFERENCES organization_memberships(id) ON DELETE CASCADE,
		    CONSTRAINT fk_member_developer_grants_organization FOREIGN KEY (organization_id)
		        REFERENCES organizations(id) ON DELETE CASCADE,
		    CONSTRAINT fk_member_developer_grants_granted_by FOREIGN KEY (granted_by)
		        REFERENCES users(id) ON DELETE SET NULL,
		    CONSTRAINT chk_member_developer_grants_scope CHECK (all_domains OR scope_domains <> '[]')
		)`,
		`CREATE INDEX idx_member_developer_grants_organization ON member_developer_grants (organization_id)`,
		`CREATE TABLE organization_invitation_developer_grants (
		    invitation_id bigint PRIMARY KEY,
		    created_at timestamptz NOT NULL,
		    CONSTRAINT fk_organization_invitation_developer_grants_invitation FOREIGN KEY (invitation_id)
		        REFERENCES organization_invitations(id) ON DELETE CASCADE
		)`,
	)
}

// Down drops every developer grant; the members keep their other features.
func (m *addMemberDeveloperGrants) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS organization_invitation_developer_grants`,
		`DROP TABLE IF EXISTS member_developer_grants`,
	)
}
