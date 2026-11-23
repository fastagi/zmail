package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_21_090000_add_invitation_feature_grants", &addInvitationFeatureGrants{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addInvitationFeatureGrants struct {
	migration.BaseMigration
}

// Up gives every invitation the four feature grants its membership starts with. Existing
// invitations get the new-member defaults, contacts and templates, which is what accepting them
// gave before.
func (m *addInvitationFeatureGrants) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_invitations
		    ADD COLUMN can_use_broadcasts boolean NOT NULL DEFAULT false,
		    ADD COLUMN can_use_contacts boolean NOT NULL DEFAULT true,
		    ADD COLUMN can_use_templates boolean NOT NULL DEFAULT true,
		    ADD COLUMN can_use_api_keys boolean NOT NULL DEFAULT false`,
	)
}

// Down removes the grants; accepted invitations again start with the membership defaults.
func (m *addInvitationFeatureGrants) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_invitations
		    DROP COLUMN IF EXISTS can_use_broadcasts,
		    DROP COLUMN IF EXISTS can_use_contacts,
		    DROP COLUMN IF EXISTS can_use_templates,
		    DROP COLUMN IF EXISTS can_use_api_keys`,
	)
}
