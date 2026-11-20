package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_27_090000_create_registration_email_codes", &createRegistrationEmailCodes{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createRegistrationEmailCodes struct {
	migration.BaseMigration
}

// Up adds the codes that prove a new account's owner reads its email address. A row holds only
// the SHA-256 hash of the address and code, never the code. The newest row of an address is the
// only usable one; the (email, created_at) index serves that lookup and the per-address issue
// limits. It only creates a new table, so it takes no lock on an existing one.
func (m *createRegistrationEmailCodes) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE registration_email_codes (
		  id bigserial PRIMARY KEY,
		  email varchar(100) NOT NULL CHECK (email = lower(email)),
		  code_hash char(64) NOT NULL CHECK (code_hash ~ '^[0-9a-f]{64}$'),
		  expires_at timestamptz NOT NULL,
		  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
		  consumed_at timestamptz,
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL
		)`,
		`CREATE INDEX idx_registration_email_codes_email_created_at
		  ON registration_email_codes (email, created_at DESC)`,
	)
}

// Down drops the registration code table.
func (m *createRegistrationEmailCodes) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS registration_email_codes`)
}
