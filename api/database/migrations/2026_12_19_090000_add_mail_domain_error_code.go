package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_19_090000_add_mail_domain_error_code", &addMailDomainErrorCode{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailDomainErrorCode struct {
	migration.BaseMigration
}

// Up adds the stable code of a domain's last failed setup or check. Existing domains start with no
// code; their next check fills it from the failure it meets.
func (m *addMailDomainErrorCode) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_domains ADD COLUMN last_error_code varchar(64) NOT NULL DEFAULT ''`)
}

// Down removes the code; last_error keeps the provider's words.
func (m *addMailDomainErrorCode) Down(db *gorm.DB) error {
	return execStatements(db, `ALTER TABLE mail_domains DROP COLUMN IF EXISTS last_error_code`)
}
