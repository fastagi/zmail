package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_23_120000_create_mail_suppressions", &createMailSuppressions{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailSuppressions struct {
	migration.BaseMigration
}

// Up adds the organization's one suppression list of addresses that bounced for good or
// complained. It starts from the contacts suppressed for those reasons; contacts suppressed
// because the provider rejected them at send time stay out of it.
func (m *createMailSuppressions) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_suppressions (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    email varchar(320) NOT NULL,
		    reason varchar(16) NOT NULL,
		    source varchar(16) NOT NULL,
		    created_at timestamptz NOT NULL,
		    CONSTRAINT mail_suppressions_pkey PRIMARY KEY (id),
		    CONSTRAINT fk_mail_suppressions_organization FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE,
		    CONSTRAINT chk_mail_suppressions_reason CHECK (reason IN ('bounced', 'complained')),
		    CONSTRAINT chk_mail_suppressions_source CHECK (source IN ('broadcast', 'transactional', 'backfill'))
		)`,
		`CREATE UNIQUE INDEX mail_suppressions_email_idx ON mail_suppressions (organization_id, email)`,
		`CREATE INDEX mail_suppressions_list_idx ON mail_suppressions (organization_id, created_at DESC, id DESC)`,
		`INSERT INTO mail_suppressions (organization_id, email, reason, source, created_at)
		  SELECT organization_id, lower(email), status_reason, 'backfill', COALESCE(updated_at, created_at, now())
		  FROM mail_contacts
		  WHERE status = 'suppressed' AND status_reason IN ('bounced', 'complained')
		  ORDER BY id
		  ON CONFLICT DO NOTHING`,
	)
}

// Down drops the list. Contacts keep their own suppressed status, so broadcasts still skip them.
func (m *createMailSuppressions) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS mail_suppressions`)
}
