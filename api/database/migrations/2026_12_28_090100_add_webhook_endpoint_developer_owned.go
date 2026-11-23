package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_28_090100_add_webhook_endpoint_developer_owned", &addWebhookEndpointDeveloperOwned{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addWebhookEndpointDeveloperOwned struct {
	migration.BaseMigration
}

// Up marks webhook endpoints a developer creates; those follow their creator's developer grant,
// while every existing endpoint stays the organization's. The constant default makes the column
// catalog-only, so ADD COLUMN rewrites nothing and holds its ACCESS EXCLUSIVE lock on
// webhook_endpoints only briefly.
func (m *addWebhookEndpointDeveloperOwned) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE webhook_endpoints ADD COLUMN developer_owned boolean NOT NULL DEFAULT false`)
}

// Down drops the marker; every endpoint then belongs to the organization again.
func (m *addWebhookEndpointDeveloperOwned) Down(db *gorm.DB) error {
	return execStatements(db, `ALTER TABLE webhook_endpoints DROP COLUMN IF EXISTS developer_owned`)
}
