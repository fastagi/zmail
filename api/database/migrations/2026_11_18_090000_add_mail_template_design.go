package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_18_090000_add_mail_template_design", &addMailTemplateDesign{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailTemplateDesign struct {
	migration.BaseMigration
}

// Up stores the visual editor's design of a template next to its rendered bodies. NULL means the
// template is written in HTML.
func (m *addMailTemplateDesign) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_templates ADD COLUMN design jsonb`,
	)
}

// Down drops the designs; the rendered bodies stay.
func (m *addMailTemplateDesign) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_templates DROP COLUMN IF EXISTS design`,
	)
}
