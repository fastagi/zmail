package migrations

import (
	"github.com/fastagi/zmail/api/internal/infra/migration"
	"gorm.io/gorm"
)

type organizationMailCloudflare struct{ migration.BaseMigration }

func init() {
	register("2026_12_02_090000_mail_cloudflare", &organizationMailCloudflare{migration.BaseMigration{UseTransaction: true}})
}
func (*organizationMailCloudflare) Up(db *gorm.DB) error {
	return execStatements(db, `CREATE TABLE mail_cloudflare_connections (
 id uuid PRIMARY KEY, organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE, account_id text NOT NULL, zone_id text NOT NULL,
 zone_name text NOT NULL, worker text NOT NULL, token_cipher text NOT NULL, signing_cipher text NOT NULL,
 active boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now())`,
		`CREATE UNIQUE INDEX idx_mail_cloudflare_active ON mail_cloudflare_connections(organization_id) WHERE active`,
		`ALTER TABLE mail_domains ADD COLUMN cloudflare_connection_id uuid REFERENCES mail_cloudflare_connections(id)`)
}
func (*organizationMailCloudflare) Down(db *gorm.DB) error {
	return execStatements(db, `ALTER TABLE mail_domains DROP COLUMN cloudflare_connection_id`, `DROP TABLE mail_cloudflare_connections`)
}
