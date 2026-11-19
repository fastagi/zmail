package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_23_090000_add_api_key_mode", &addAPIKeyMode{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addAPIKeyMode struct {
	migration.BaseMigration
}

// Up gives every API key an immutable mode. Existing keys become live, which is what they already
// were; test keys are accepted only by routes that declare test support.
func (m *addAPIKeyMode) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE api_keys
		  ADD COLUMN mode varchar(8) NOT NULL DEFAULT 'live'
		  CONSTRAINT chk_api_keys_mode CHECK (mode IN ('live', 'test'))`,
	)
}

// Down forgets the mode. The old code treats every key as live, so revoke test keys before rolling
// back or they gain live access.
func (m *addAPIKeyMode) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE api_keys DROP COLUMN mode`,
	)
}
