package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_27_090000_add_mail_domain_default", &addMailDomainDefault{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailDomainDefault struct {
	migration.BaseMigration
}

// Up adds the mark an owner or admin sets on the domain new mailboxes use. Existing domains start
// unmarked, so each organization's oldest verified domain stays its default until someone chooses
// one. The partial unique index allows one marked domain per organization. A constant default
// only changes the catalog, and the index covers at most ten rows per organization.
func (m *addMailDomainDefault) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_domains ADD COLUMN is_default boolean DEFAULT false NOT NULL`,
		`CREATE UNIQUE INDEX idx_mail_domains_default ON mail_domains (organization_id) WHERE is_default`,
	)
}

// Down drops the mark; every organization falls back to its oldest verified domain.
func (m *addMailDomainDefault) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP INDEX IF EXISTS idx_mail_domains_default`,
		`ALTER TABLE mail_domains DROP COLUMN IF EXISTS is_default`,
	)
}
