package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_18_090000_add_member_feature_grants", &addMemberFeatureGrants{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMemberFeatureGrants struct {
	migration.BaseMigration
}

// Up gives every membership four feature grants. Existing memberships get all four, which is
// what every member could do before; afterwards a new member starts with contacts and templates
// only, and an owner or admin grants broadcasts and API keys.
func (m *addMemberFeatureGrants) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_memberships
		    ADD COLUMN can_use_broadcasts boolean NOT NULL DEFAULT true,
		    ADD COLUMN can_use_contacts boolean NOT NULL DEFAULT true,
		    ADD COLUMN can_use_templates boolean NOT NULL DEFAULT true,
		    ADD COLUMN can_use_api_keys boolean NOT NULL DEFAULT true`,
		`ALTER TABLE organization_memberships
		    ALTER COLUMN can_use_broadcasts SET DEFAULT false,
		    ALTER COLUMN can_use_api_keys SET DEFAULT false`,
	)
}

// Down removes the grants; every member can again use every feature its role allowed before.
func (m *addMemberFeatureGrants) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_memberships
		    DROP COLUMN IF EXISTS can_use_broadcasts,
		    DROP COLUMN IF EXISTS can_use_contacts,
		    DROP COLUMN IF EXISTS can_use_templates,
		    DROP COLUMN IF EXISTS can_use_api_keys`,
	)
}
