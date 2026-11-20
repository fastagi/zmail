package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_29_090000_add_platform_operator_scope", &addPlatformOperatorScope{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addPlatformOperatorScope struct {
	migration.BaseMigration
}

// Up gives every grant a scope. Existing grants become full, which is what they allowed before.
func (m *addPlatformOperatorScope) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE platform_operators ADD COLUMN scope varchar(16) NOT NULL DEFAULT 'full'`,
		`ALTER TABLE platform_operators ADD CONSTRAINT platform_operators_scope_check
		    CHECK (scope IN ('full', 'mail_metrics'))`,
	)
}

// Down removes the scope. Metrics-only grants are deleted first: without the column they would
// silently become full grants.
func (m *addPlatformOperatorScope) Down(db *gorm.DB) error {
	return execStatements(db,
		`DELETE FROM platform_operators WHERE scope <> 'full'`,
		`ALTER TABLE platform_operators DROP CONSTRAINT IF EXISTS platform_operators_scope_check`,
		`ALTER TABLE platform_operators DROP COLUMN IF EXISTS scope`,
	)
}
