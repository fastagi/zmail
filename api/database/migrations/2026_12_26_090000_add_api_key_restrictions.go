package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_26_090000_add_api_key_restrictions", &addAPIKeyRestrictions{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addAPIKeyRestrictions struct {
	migration.BaseMigration
}

// Up lets a key be bound to one organization and to some of its sending domains. Both columns are
// nullable without a default, so existing keys stay unrestricted and no row is rewritten; the check
// keeps a domain list from existing without an organization. organization_id has no foreign key:
// a key of a deleted organization simply stops resolving, like its owner's membership.
func (m *addAPIKeyRestrictions) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE api_keys
		    ADD COLUMN organization_id bigint,
		    ADD COLUMN sending_domains text,
		    ADD CONSTRAINT chk_api_keys_sending_domains_organization
		        CHECK (sending_domains IS NULL OR organization_id IS NOT NULL)`,
	)
}

// Down forgets every restriction. Older code would treat a restricted key as unrestricted, so it
// first revokes every restricted key that is still active.
func (m *addAPIKeyRestrictions) Down(db *gorm.DB) error {
	return execStatements(db,
		`UPDATE api_keys SET revoked_at = now(), updated_at = now()
		    WHERE (organization_id IS NOT NULL OR sending_domains IS NOT NULL) AND revoked_at IS NULL`,
		`ALTER TABLE api_keys
		    DROP CONSTRAINT IF EXISTS chk_api_keys_sending_domains_organization,
		    DROP COLUMN IF EXISTS sending_domains,
		    DROP COLUMN IF EXISTS organization_id`,
	)
}
