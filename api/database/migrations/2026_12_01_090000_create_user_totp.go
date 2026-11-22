package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_01_090000_create_user_totp", &createUserTOTP{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createUserTOTP struct {
	migration.BaseMigration
}

// Up adds a user's authenticator-app factor and its one-time recovery codes. The factor keeps its
// secret encrypted; confirmed_at stays null until the user proves the app works, and only a
// confirmed factor is asked for at sign-in. last_used_step refuses a code's reuse. Recovery codes
// are stored as SHA-256 hashes. It only creates new tables, so it locks no existing one.
func (m *createUserTOTP) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE user_totp_factors (
		  user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
		  secret text NOT NULL,
		  confirmed_at timestamptz,
		  last_used_step bigint NOT NULL DEFAULT 0 CHECK (last_used_step >= 0),
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL
		)`,
		`CREATE TABLE user_recovery_codes (
		  id bigserial PRIMARY KEY,
		  user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
		  code_hash char(64) NOT NULL CHECK (code_hash ~ '^[0-9a-f]{64}$'),
		  used_at timestamptz,
		  created_at timestamptz NOT NULL,
		  UNIQUE (user_id, code_hash)
		)`,
	)
}

// Down drops the factor and recovery code tables.
func (m *createUserTOTP) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS user_recovery_codes`, `DROP TABLE IF EXISTS user_totp_factors`)
}
