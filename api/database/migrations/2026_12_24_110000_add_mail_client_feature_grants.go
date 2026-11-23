package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_24_110000_add_mail_client_feature_grants", &addMailClientFeatureGrants{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailClientFeatureGrants struct {
	migration.BaseMigration
}

// Up adds the fifth member feature, mail_clients. Every membership and invitation has it, existing
// or new: reading one's own mail in a mail app sends nothing at scale.
func (m *addMailClientFeatureGrants) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_memberships ADD COLUMN can_use_mail_clients boolean NOT NULL DEFAULT true`,
		`ALTER TABLE organization_invitations ADD COLUMN can_use_mail_clients boolean NOT NULL DEFAULT true`,
	)
}

// Down removes the grant.
func (m *addMailClientFeatureGrants) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_invitations DROP COLUMN IF EXISTS can_use_mail_clients`,
		`ALTER TABLE organization_memberships DROP COLUMN IF EXISTS can_use_mail_clients`,
	)
}
