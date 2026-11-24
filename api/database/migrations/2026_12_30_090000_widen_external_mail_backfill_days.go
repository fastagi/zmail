package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_30_090000_widen_external_mail_backfill_days", &widenExternalMailBackfillDays{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type widenExternalMailBackfillDays struct {
	migration.BaseMigration
}

// Up widens backfill_days on external_mail_connections and external_mail_authorizations from
// 90 days to 3650 days (10 years / full-history backfill).
func (m *widenExternalMailBackfillDays) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE external_mail_connections
		   DROP CONSTRAINT IF EXISTS external_mail_connections_backfill_days_check,
		   DROP CONSTRAINT IF EXISTS chk_external_mail_connections_backfill_days,
		   ADD CONSTRAINT chk_external_mail_connections_backfill_days CHECK (backfill_days BETWEEN 1 AND 3650)`,
		`ALTER TABLE external_mail_authorizations
		   DROP CONSTRAINT IF EXISTS external_mail_authorizations_backfill_days_check,
		   DROP CONSTRAINT IF EXISTS chk_external_mail_authorizations_backfill_days,
		   ADD CONSTRAINT chk_external_mail_authorizations_backfill_days CHECK (backfill_days BETWEEN 1 AND 3650)`,
	)
}

// Down restores the 90-day CHECK bound (clamping any wider rows first so rollback succeeds cleanly).
func (m *widenExternalMailBackfillDays) Down(db *gorm.DB) error {
	return execStatements(db,
		`UPDATE external_mail_connections SET backfill_days = 90 WHERE backfill_days > 90`,
		`ALTER TABLE external_mail_connections
		   DROP CONSTRAINT IF EXISTS chk_external_mail_connections_backfill_days,
		   ADD CONSTRAINT external_mail_connections_backfill_days_check CHECK (backfill_days BETWEEN 1 AND 90)`,
		`UPDATE external_mail_authorizations SET backfill_days = 90 WHERE backfill_days > 90`,
		`ALTER TABLE external_mail_authorizations
		   DROP CONSTRAINT IF EXISTS chk_external_mail_authorizations_backfill_days,
		   ADD CONSTRAINT external_mail_authorizations_backfill_days_check CHECK (backfill_days BETWEEN 1 AND 90)`,
	)
}
