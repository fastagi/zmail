package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_01_090000_add_platform_operator_mail_content", &addPlatformOperatorMailContent{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addPlatformOperatorMailContent struct {
	migration.BaseMigration
}

// Up adds the explicit content add-on to operator grants. No existing grant gets it, full grants
// included: reading a broadcast body needs a separate decision by the owner.
func (m *addPlatformOperatorMailContent) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE platform_operators ADD COLUMN mail_content boolean NOT NULL DEFAULT false`,
	)
}

// Down drops the add-on. That only narrows grants: every grant keeps its scope and loses content.
func (m *addPlatformOperatorMailContent) Down(db *gorm.DB) error {
	return execStatements(db, `ALTER TABLE platform_operators DROP COLUMN IF EXISTS mail_content`)
}
