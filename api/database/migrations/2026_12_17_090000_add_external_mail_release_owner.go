package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_17_090000_add_external_mail_release_owner", &addExternalMailReleaseOwner{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addExternalMailReleaseOwner struct {
	migration.BaseMigration
}

// Up records on each provider release whose pending sign-in holds it back, so the rule still holds
// once the purge deleted the connection row: owner_scope 'member' with owner_user_id for a member's
// own connection (only that member's sign-in counts; none once the user is deleted, which sets the id
// to NULL), 'shared' for a shared connection (anyone's in the organization counts).
//
// Existing rows are backfilled from their connection when it still exists; a row whose connection
// is gone, or whose member was deleted before this migration, stays 'unknown' and keeps the earlier,
// conservative rule. Every release expires within a day, so 'unknown' rows disappear on their own.
func (m *addExternalMailReleaseOwner) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE external_mail_provider_releases
		  ADD COLUMN owner_scope varchar(16) NOT NULL DEFAULT 'unknown',
		  ADD COLUMN owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  ADD CONSTRAINT chk_external_mail_provider_releases_owner_scope CHECK (owner_scope IN ('unknown', 'member', 'shared')),
		  ADD CONSTRAINT chk_external_mail_provider_releases_owner CHECK (owner_scope = 'member' OR owner_user_id IS NULL)`,
		`UPDATE external_mail_provider_releases AS r SET owner_scope = 'shared'
		  FROM external_mail_connections AS c
		  WHERE c.id = r.connection_id AND c.organization_id = r.organization_id AND c.shared`,
		`UPDATE external_mail_provider_releases AS r SET owner_scope = 'member', owner_user_id = c.owner_user_id
		  FROM external_mail_connections AS c
		  WHERE c.id = r.connection_id AND c.organization_id = r.organization_id AND NOT c.shared AND c.owner_user_id IS NOT NULL`,
		`CREATE INDEX idx_external_mail_provider_releases_owner ON external_mail_provider_releases (owner_user_id)
		  WHERE owner_user_id IS NOT NULL`,
	)
}

// Down drops the owner; the releases then fall back to the conservative rule.
func (m *addExternalMailReleaseOwner) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP INDEX IF EXISTS idx_external_mail_provider_releases_owner`,
		`ALTER TABLE external_mail_provider_releases
		  DROP CONSTRAINT IF EXISTS chk_external_mail_provider_releases_owner,
		  DROP CONSTRAINT IF EXISTS chk_external_mail_provider_releases_owner_scope,
		  DROP COLUMN IF EXISTS owner_user_id,
		  DROP COLUMN IF EXISTS owner_scope`,
	)
}
