package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_23_100000_add_transactional_templates", &addTransactionalTemplates{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addTransactionalTemplates struct {
	migration.BaseMigration
}

// Up lets a template be transactional: it then has an API key unique in its organization, a
// sender address, an optional sender name, and declared variables. Existing templates stay
// standard, and a standard template carries none of these fields.
func (m *addTransactionalTemplates) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_templates
		  ADD COLUMN kind varchar(16) NOT NULL DEFAULT 'standard'
		    CONSTRAINT chk_mail_templates_kind CHECK (kind IN ('standard', 'transactional')),
		  ADD COLUMN template_key varchar(64),
		  ADD COLUMN from_address varchar(320),
		  ADD COLUMN from_name varchar(100) NOT NULL DEFAULT '',
		  ADD COLUMN variable_definitions jsonb NOT NULL DEFAULT '[]'::jsonb`,
		`ALTER TABLE mail_templates ADD CONSTRAINT chk_mail_templates_transactional_fields CHECK (
		  (kind = 'transactional' AND template_key IS NOT NULL AND from_address IS NOT NULL)
		  OR (kind = 'standard' AND template_key IS NULL AND from_address IS NULL AND from_name = ''
		      AND variable_definitions = '[]'::jsonb))`,
		`CREATE UNIQUE INDEX mail_templates_key_idx ON mail_templates (organization_id, template_key) WHERE template_key IS NOT NULL`,
	)
}

// Down deletes transactional templates, which the old code cannot serve, then drops the columns.
func (m *addTransactionalTemplates) Down(db *gorm.DB) error {
	return execStatements(db,
		`DELETE FROM mail_templates WHERE kind = 'transactional'`,
		`DROP INDEX IF EXISTS mail_templates_key_idx`,
		`ALTER TABLE mail_templates
		  DROP CONSTRAINT IF EXISTS chk_mail_templates_transactional_fields,
		  DROP COLUMN variable_definitions,
		  DROP COLUMN from_name,
		  DROP COLUMN from_address,
		  DROP COLUMN template_key,
		  DROP COLUMN kind`,
	)
}
