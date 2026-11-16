package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
	"github.com/fastagi/zmail/api/pkg/env"
)

// seedDefaultUsersInProduction reports whether the process runs in production mode; a variable so
// tests can exercise both branches.
var seedDefaultUsersInProduction = env.IsProduction

func init() {
	register("2025_06_18_000001_seed_default_users", &seedDefaultUsers{})
}

// seedDefaultUsers seeds the default admin user outside production.
type seedDefaultUsers struct {
	migration.BaseMigration
}

// Up creates the admin account (password: secret) only when no active user exists. Production
// (APP_ENV=production) creates nothing: an account with a published password must never exist on a
// reachable instance, so the first production account comes from registration instead.
func (m *seedDefaultUsers) Up(db *gorm.DB) error {
	if seedDefaultUsersInProduction() {
		return nil
	}
	return execStatements(db,
		`INSERT INTO users (created_at, updated_at, username, password, email, nickname, avatar, phone, bio, status)
		SELECT now(), now(), 'admin', '$2a$10$OkAgF/Pm/v3pdzkUhKJEeOhehkbTRZar9Rk3X2nEjCcrlsluiTnay',
		       'admin@example.com', 'Admin User', '', '', '', 1
		WHERE NOT EXISTS (SELECT 1 FROM users WHERE deleted_at IS NULL)`,
	)
}

// Down soft-deletes the seeded admin account, matching the account lifecycle.
func (m *seedDefaultUsers) Down(db *gorm.DB) error {
	return execStatements(db,
		`UPDATE users SET deleted_at = now() WHERE username = 'admin' AND deleted_at IS NULL`,
	)
}
