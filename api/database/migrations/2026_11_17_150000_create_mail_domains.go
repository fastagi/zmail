package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_17_150000_create_mail_domains", &createMailDomains{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailDomains struct {
	migration.BaseMigration
}

// Up adds domains an organization connected. A name is unique across the instance, so one domain
// belongs to one organization. mailboxes.routing_rule_id names the provider rule that delivers a
// mailbox's mail on such a domain; it is empty on the shared domain.
func (m *createMailDomains) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_domains (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    name varchar(253) NOT NULL,
		    zone_id varchar(64) NOT NULL,
		    zone_name varchar(253) NOT NULL,
		    sending_tag varchar(64) NOT NULL DEFAULT '',
		    status varchar(20) NOT NULL DEFAULT 'pending',
		    records jsonb NOT NULL DEFAULT '[]',
		    last_error varchar(500) NOT NULL DEFAULT '',
		    checked_at timestamptz,
		    verified_at timestamptz,
		    created_by bigint NOT NULL,
		    created_at timestamptz,
		    updated_at timestamptz,
		    CONSTRAINT mail_domains_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_mail_domains_status CHECK (status IN ('pending', 'verified'))
		)`,
		`CREATE UNIQUE INDEX idx_mail_domains_name ON mail_domains (name)`,
		`CREATE INDEX idx_mail_domains_organization ON mail_domains (organization_id)`,
		`ALTER TABLE mail_domains ADD CONSTRAINT fk_mail_domains_organization FOREIGN KEY (organization_id) REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE CASCADE`,
		`ALTER TABLE mailboxes ADD COLUMN routing_rule_id varchar(64) NOT NULL DEFAULT ''`,
	)
}

// Down removes connected domains.
func (m *createMailDomains) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes DROP COLUMN IF EXISTS routing_rule_id`,
		`DROP TABLE IF EXISTS mail_domains CASCADE`,
	)
}
