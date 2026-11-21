package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_06_090000_add_external_mail_sync_ops", &addExternalMailSyncOps{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addExternalMailSyncOps struct {
	migration.BaseMigration
}

// Up makes the connector sync operable:
//
//   - external_mail_sync_runs keeps one row per fetch: when it ran, how it ended, how many
//     messages it fetched and stored, and the error code. No message data. The worker keeps at
//     most the latest 50 rows per connection and 14 days. timed_out marks a fetch that hit its
//     deadline; throttled one whose next page waits the spacing.
//   - external_mail_connections.consecutive_failures sets the backoff of a failing connection, and
//     consecutive_more bounds how many pages in a row one connection fetches at once.
//   - external_mail_connections.revoked_at starts the retention of a revoked connection's copies.
//     Existing revoked rows start it at the upgrade, so no copy is deleted earlier than a full
//     retention after it.
//   - The due index gains the provider, so a pass takes each provider's oldest due connections
//     without scanning the others, and a partial index finds revoked connections by age.
//
// external_mail_connections is bounded per member, so the column defaults and index builds are
// short inside the transaction.
func (m *addExternalMailSyncOps) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE external_mail_connections
		  ADD COLUMN consecutive_failures smallint NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
		  ADD COLUMN consecutive_more smallint NOT NULL DEFAULT 0 CHECK (consecutive_more >= 0),
		  ADD COLUMN revoked_at timestamptz`,
		`UPDATE external_mail_connections SET revoked_at = now() WHERE status = 'revoked'`,
		`DROP INDEX idx_external_mail_connections_due`,
		`CREATE INDEX idx_external_mail_connections_due
		  ON external_mail_connections (provider, next_sync_at, id) WHERE status = 'active'`,
		`CREATE INDEX idx_external_mail_connections_revoked
		  ON external_mail_connections (revoked_at, id) WHERE status = 'revoked'`,
		`CREATE TABLE external_mail_sync_runs (
		  id bigserial PRIMARY KEY,
		  connection_id bigint NOT NULL REFERENCES external_mail_connections(id) ON DELETE CASCADE,
		  started_at timestamptz NOT NULL,
		  finished_at timestamptz NOT NULL,
		  outcome varchar(24) NOT NULL CHECK (outcome IN
		    ('ok','auth_failed','fetch_failed','cursor_expired','provider_unavailable','dropped')),
		  fetched_count integer NOT NULL DEFAULT 0 CHECK (fetched_count >= 0),
		  stored_count integer NOT NULL DEFAULT 0 CHECK (stored_count >= 0),
		  more boolean NOT NULL DEFAULT false,
		  error_code varchar(64),
		  timed_out boolean NOT NULL DEFAULT false,
		  throttled boolean NOT NULL DEFAULT false)`,
		`CREATE INDEX idx_external_mail_sync_runs_connection ON external_mail_sync_runs (connection_id, id DESC)`,
		`CREATE INDEX idx_external_mail_sync_runs_started ON external_mail_sync_runs (started_at, id)`,
	)
}

// Down drops the sync history and the new columns and restores the due index.
func (m *addExternalMailSyncOps) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS external_mail_sync_runs`,
		`DROP INDEX IF EXISTS idx_external_mail_connections_revoked`,
		`DROP INDEX idx_external_mail_connections_due`,
		`CREATE INDEX idx_external_mail_connections_due ON external_mail_connections (next_sync_at) WHERE status = 'active'`,
		`ALTER TABLE external_mail_connections DROP COLUMN revoked_at, DROP COLUMN consecutive_more, DROP COLUMN consecutive_failures`,
	)
}
