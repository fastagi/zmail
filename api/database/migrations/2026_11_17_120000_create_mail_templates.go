package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_17_120000_create_mail_templates", &createMailTemplates{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailTemplates struct {
	migration.BaseMigration
}

// Up adds reusable message templates of an organization. mail_messages.template_id records which
// template a message was rendered from; it has no foreign key, so deleting a template keeps the
// messages sent with it.
func (m *createMailTemplates) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_templates (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    name varchar(100) NOT NULL,
		    subject varchar(998) NOT NULL,
		    html_body text NOT NULL DEFAULT '',
		    text_body text NOT NULL DEFAULT '',
		    sample_variables jsonb NOT NULL DEFAULT '{}',
		    created_by bigint NOT NULL,
		    created_at timestamptz,
		    updated_at timestamptz,
		    CONSTRAINT mail_templates_pkey PRIMARY KEY (id)
		)`,
		`CREATE UNIQUE INDEX idx_mail_templates_name ON mail_templates (organization_id, name)`,
		`ALTER TABLE mail_templates ADD CONSTRAINT fk_mail_templates_organization FOREIGN KEY (organization_id) REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE CASCADE`,
		`ALTER TABLE mail_messages ADD COLUMN template_id bigint`,
	)
}

// Down removes templates.
func (m *createMailTemplates) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_messages DROP COLUMN IF EXISTS template_id`,
		`DROP TABLE IF EXISTS mail_templates CASCADE`,
	)
}
