package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_17_090000_add_organization_controls", &addOrganizationControls{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addOrganizationControls struct {
	migration.BaseMigration
}

// Up adds the platform operator's controls over one organization: a suspension, with its reason,
// and a daily send limit that replaces the instance limit. Nullable columns and a constant default
// add no table rewrite.
func (m *addOrganizationControls) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organizations ADD COLUMN suspended_at timestamptz`,
		`ALTER TABLE organizations ADD COLUMN suspension_reason varchar(500) NOT NULL DEFAULT ''`,
		`ALTER TABLE organizations ADD COLUMN daily_send_limit integer`,
		`ALTER TABLE organizations ADD CONSTRAINT chk_organizations_daily_send_limit
		  CHECK (daily_send_limit IS NULL OR (daily_send_limit >= 1 AND daily_send_limit <= 1000000))`,
	)
}

// Down drops the controls; every organization is active again under the instance limits.
func (m *addOrganizationControls) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organizations DROP CONSTRAINT IF EXISTS chk_organizations_daily_send_limit`,
		`ALTER TABLE organizations DROP COLUMN IF EXISTS daily_send_limit`,
		`ALTER TABLE organizations DROP COLUMN IF EXISTS suspension_reason`,
		`ALTER TABLE organizations DROP COLUMN IF EXISTS suspended_at`,
	)
}
