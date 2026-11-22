package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_16_090000_add_external_mail_provider_releases", &addExternalMailProviderReleases{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addExternalMailProviderReleases struct {
	migration.BaseMigration
}

// Up adds external_mail_provider_releases: what is left to do at the provider after a connection
// was revoked (stop its push subscription, hand its grant back), written in the revocation's
// transaction so it survives a process that dies after the commit. The worker performs and deletes
// each row, retrying with backoff; a row past expires_at is deleted undone. encrypted_grant is the
// connection's sealed refresh token, copied before the revocation erased it, and lives only as long
// as its row (at most a day). A row goes with its connection.
func (m *addExternalMailProviderReleases) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE external_mail_provider_releases (
		  id bigserial PRIMARY KEY,
		  connection_id bigint NOT NULL REFERENCES external_mail_connections(id) ON DELETE CASCADE,
		  organization_id bigint NOT NULL,
		  provider varchar(16) NOT NULL,
		  encrypted_grant text NOT NULL DEFAULT '',
		  stop_push boolean NOT NULL DEFAULT false,
		  stop_subscription_id varchar(255) NOT NULL DEFAULT '',
		  attempts smallint NOT NULL DEFAULT 0,
		  next_attempt_at timestamptz NOT NULL,
		  expires_at timestamptz NOT NULL,
		  created_at timestamptz NOT NULL,
		  CONSTRAINT chk_external_mail_provider_releases_provider CHECK (provider ~ '^[a-z][a-z0-9_]{1,15}$'),
		  CONSTRAINT chk_external_mail_provider_releases_work CHECK (stop_push OR encrypted_grant <> ''),
		  CONSTRAINT chk_external_mail_provider_releases_attempts CHECK (attempts >= 0),
		  CONSTRAINT chk_external_mail_provider_releases_deadline CHECK (expires_at > created_at)
		)`,
		`CREATE INDEX idx_external_mail_provider_releases_due ON external_mail_provider_releases (next_attempt_at, id)`,
		`CREATE INDEX idx_external_mail_provider_releases_connection ON external_mail_provider_releases (connection_id)`,
	)
}

// Down drops the releases; grants still waiting to be handed back then lapse at the provider.
func (m *addExternalMailProviderReleases) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS external_mail_provider_releases`)
}
