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
// domain names; a later column can hold single addresses. The foreign keys take a brief SHARE ROW
// EXCLUSIVE lock on organization_memberships, organizations, and users. It also marks webhook
// endpoints a developer creates; the column has a constant default, so adding it rewrites nothing
// and holds the ACCESS EXCLUSIVE lock on webhook_endpoints only briefly.
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
		`ALTER TABLE webhook_endpoints ADD COLUMN developer_owned boolean NOT NULL DEFAULT false`,
	)
}

// Down drops every developer grant; the members keep their other features.
func (m *addMemberDeveloperGrants) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE webhook_endpoints DROP COLUMN IF EXISTS developer_owned`,
		`DROP TABLE IF EXISTS member_developer_grants`,
	)
}
