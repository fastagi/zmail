package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_19_100000_create_mail_zones", &createMailZones{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailZones struct {
	migration.BaseMigration
}

// Up adds DNS zones Zmail creates for an organization's apex domain, and records which managed
// zone a mail domain lives in and whether it receives mail. A zone name and provider zone are
// unique across the instance. mail_domains.mail_zone_id uses NO ACTION rather than RESTRICT so
// deleting an organization cascades to both tables in one statement.
func (m *createMailZones) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_zones (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    name varchar(253) NOT NULL,
		    cloudflare_zone_id varchar(64) NOT NULL,
		    status varchar(20) NOT NULL DEFAULT 'pending',
		    name_servers jsonb NOT NULL DEFAULT '[]',
		    original_name_servers jsonb NOT NULL DEFAULT '[]',
		    activated_at timestamptz,
		    last_checked_at timestamptz,
		    created_by bigint NOT NULL,
		    created_at timestamptz,
		    updated_at timestamptz,
		    CONSTRAINT mail_zones_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_mail_zones_status CHECK (status IN ('pending', 'active'))
		)`,
		`CREATE UNIQUE INDEX idx_mail_zones_name ON mail_zones (name)`,
		`CREATE UNIQUE INDEX idx_mail_zones_cloudflare_zone ON mail_zones (cloudflare_zone_id)`,
		`CREATE INDEX idx_mail_zones_organization ON mail_zones (organization_id)`,
		`ALTER TABLE mail_zones ADD CONSTRAINT fk_mail_zones_organization FOREIGN KEY (organization_id) REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE CASCADE`,
		`ALTER TABLE mail_domains ADD COLUMN mail_zone_id bigint`,
		`ALTER TABLE mail_domains ADD COLUMN capability varchar(20) NOT NULL DEFAULT 'send_receive'`,
		`ALTER TABLE mail_domains ADD CONSTRAINT chk_mail_domains_capability CHECK (capability IN ('send_receive', 'send_only'))`,
		`ALTER TABLE mail_domains ADD CONSTRAINT fk_mail_domains_mail_zone FOREIGN KEY (mail_zone_id) REFERENCES mail_zones(id) ON DELETE NO ACTION`,
		`CREATE INDEX idx_mail_domains_mail_zone ON mail_domains (mail_zone_id)`,
	)
}

// Down removes managed zones and the domain columns that refer to them.
func (m *createMailZones) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP INDEX IF EXISTS idx_mail_domains_mail_zone`,
		`ALTER TABLE mail_domains DROP CONSTRAINT IF EXISTS fk_mail_domains_mail_zone`,
		`ALTER TABLE mail_domains DROP CONSTRAINT IF EXISTS chk_mail_domains_capability`,
		`ALTER TABLE mail_domains DROP COLUMN IF EXISTS capability`,
		`ALTER TABLE mail_domains DROP COLUMN IF EXISTS mail_zone_id`,
		`DROP TABLE IF EXISTS mail_zones CASCADE`,
	)
}
