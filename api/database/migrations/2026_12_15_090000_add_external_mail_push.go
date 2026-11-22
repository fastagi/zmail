package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_15_090000_add_external_mail_push", &addExternalMailPush{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addExternalMailPush struct {
	migration.BaseMigration
}

// Up adds push subscriptions, which only ever mark a connection due:
//
//   - external_mail_subscriptions holds at most one subscription per connection and goes with it
//     (ON DELETE CASCADE, so the retention purge removes it). lookup_key finds the rows a
//     notification names: the lowercased Gmail address (one account may be bound by several
//     connections) or the Graph subscription id. secret_hash is the SHA-256 of the Graph
//     clientState, never the secret itself. pending_at marks a notification no sync has answered
//     yet; scheduled_sync_at is the next_sync_at the latest sync saved, against which a
//     notification compares before it lowers next_sync_at, so a claim lease is never shortened.
//     A stopped row keeps no key or secret.
//   - idx_external_mail_subscriptions_lookup serves the notification routes;
//     idx_external_mail_subscriptions_pending the worker's promotion of notifications that arrived
//     during a fetch.
//   - live_since is when the subscription last became active; degraded_at marks one that a poll
//     caught missing new mail, so the connection polls at the regular interval until a notification
//     arrives again. unsupported marks a connection whose provider cannot deliver push for it (a
//     Gmail mailbox assigned from a Workspace tenant, whose tokens come from the customer's project).
//   - external_mail_sync_runs.triggered_by tells a push-triggered sync from a poll. The constant default
//     is recorded without rewriting the table. Its check is a plain one: NOT VALID followed by
//     VALIDATE gains nothing inside this migration's single transaction, which holds the lock to
//     the end either way, and the table is small by design (at most 50 runs per connection, 14
//     days), so validating it holds the lock only briefly.
func (m *addExternalMailPush) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE external_mail_subscriptions (
		  id bigserial PRIMARY KEY,
		  connection_id bigint NOT NULL REFERENCES external_mail_connections(id) ON DELETE CASCADE,
		  provider varchar(16) NOT NULL,
		  lookup_key varchar(320) NOT NULL DEFAULT '',
		  provider_subscription_id varchar(255) NOT NULL DEFAULT '',
		  secret_hash varchar(64) NOT NULL DEFAULT '',
		  status varchar(16) NOT NULL DEFAULT 'pending',
		  expires_at timestamptz,
		  renew_after timestamptz NOT NULL,
		  pending_at timestamptz,
		  last_notified_at timestamptz,
		  last_push_sync_at timestamptz,
		  scheduled_sync_at timestamptz,
		  live_since timestamptz,
		  degraded_at timestamptz,
		  consecutive_failures smallint NOT NULL DEFAULT 0,
		  last_error_code varchar(64),
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL,
		  CONSTRAINT uq_external_mail_subscriptions_connection UNIQUE (connection_id),
		  CONSTRAINT chk_external_mail_subscriptions_provider CHECK (provider ~ '^[a-z][a-z0-9_]{1,15}$'),
		  CONSTRAINT chk_external_mail_subscriptions_status CHECK (status IN ('pending','active','unsupported','stopped')),
		  CONSTRAINT chk_external_mail_subscriptions_active CHECK (status <> 'active' OR (lookup_key <> '' AND expires_at IS NOT NULL)),
		  CONSTRAINT chk_external_mail_subscriptions_stopped CHECK (status <> 'stopped' OR (lookup_key = '' AND secret_hash = '' AND pending_at IS NULL)),
		  CONSTRAINT chk_external_mail_subscriptions_secret CHECK (secret_hash = '' OR secret_hash ~ '^[0-9a-f]{64}$'),
		  CONSTRAINT chk_external_mail_subscriptions_failures CHECK (consecutive_failures >= 0)
		)`,
		`CREATE INDEX idx_external_mail_subscriptions_lookup ON external_mail_subscriptions (provider, lookup_key)
		  WHERE status <> 'stopped'`,
		`CREATE INDEX idx_external_mail_subscriptions_pending ON external_mail_subscriptions (pending_at, id)
		  WHERE pending_at IS NOT NULL`,
		`CREATE INDEX idx_external_mail_subscriptions_open ON external_mail_subscriptions (connection_id)
		  WHERE status <> 'stopped'`,
		`ALTER TABLE external_mail_sync_runs
		  ADD COLUMN triggered_by varchar(8) NOT NULL DEFAULT 'poll',
		  ADD CONSTRAINT chk_external_mail_sync_runs_trigger CHECK (triggered_by IN ('poll','push'))`,
	)
}

// Down drops the subscriptions and the trigger. Provider subscriptions still open lapse at their
// expiry; their notifications reach a route the older schema does not have.
func (m *addExternalMailPush) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE external_mail_sync_runs
		  DROP CONSTRAINT IF EXISTS chk_external_mail_sync_runs_trigger,
		  DROP COLUMN IF EXISTS triggered_by`,
		`DROP TABLE IF EXISTS external_mail_subscriptions`,
	)
}
