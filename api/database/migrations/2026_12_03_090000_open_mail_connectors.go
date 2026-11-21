package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_03_090000_open_mail_connectors", &openMailConnectors{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type openMailConnectors struct {
	migration.BaseMigration
}

// providerPattern is the shape of a connector id: the provider list itself is code, not schema,
// so adding a connector needs no migration.
const providerPattern = `'^[a-z][a-z0-9_]{1,15}$'`

// Up opens the connector ids and moves external mailboxes out of the delivery namespace:
//
//   - external_mail_connections.provider and mail_messages.source accept any connector id instead
//     of a fixed list. The source CHECK is added NOT VALID, so no message row is scanned; every
//     existing value already matches the pattern.
//   - The global unique address index skips external mailboxes, whose address is an outside
//     account's: two members may each bind their own copy, and inbound delivery, which looks up
//     the same index, can never resolve one. The connection keeps an outside account unique per
//     organization and provider. mailboxes is bounded per organization, so rebuilding the index
//     inside the transaction is short.
//   - external_mail_authorizations holds one pending sign-in: the hash of its single-use state,
//     the sealed PKCE verifier, who started it, and for which connection when it reconnects.
func (m *openMailConnectors) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE external_mail_connections DROP CONSTRAINT IF EXISTS external_mail_connections_provider_check,
		  ADD CONSTRAINT chk_external_mail_connections_provider CHECK (provider ~ `+providerPattern+`)`,
		`ALTER TABLE mail_messages DROP CONSTRAINT chk_mail_messages_source,
		  ADD CONSTRAINT chk_mail_messages_source CHECK (source = 'zmail' OR source ~ `+providerPattern+`) NOT VALID`,
		`DROP INDEX idx_mailboxes_address`,
		`CREATE UNIQUE INDEX idx_mailboxes_address ON mailboxes (address) WHERE type <> 'external'`,
		`CREATE TABLE external_mail_authorizations (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		  provider varchar(16) NOT NULL CHECK (provider ~ `+providerPattern+`),
		  state_hash char(64) NOT NULL UNIQUE,
		  sealed_verifier text NOT NULL,
		  connection_id bigint REFERENCES external_mail_connections(id) ON DELETE CASCADE,
		  backfill_days smallint NOT NULL DEFAULT 14 CHECK (backfill_days BETWEEN 1 AND 90),
		  expires_at timestamptz NOT NULL,
		  created_at timestamptz NOT NULL)`,
		`CREATE INDEX idx_external_mail_authorizations_expiry ON external_mail_authorizations (expires_at)`,
	)
}

// Down restores the fixed provider list and the global address index. It fails while a connector
// other than gmail or outlook, or two external mailboxes with one address, exist.
func (m *openMailConnectors) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS external_mail_authorizations`,
		`DROP INDEX idx_mailboxes_address`,
		`CREATE UNIQUE INDEX idx_mailboxes_address ON mailboxes (address)`,
		`ALTER TABLE mail_messages DROP CONSTRAINT chk_mail_messages_source,
		  ADD CONSTRAINT chk_mail_messages_source CHECK (source IN ('zmail','gmail','outlook')) NOT VALID`,
		`ALTER TABLE external_mail_connections DROP CONSTRAINT chk_external_mail_connections_provider,
		  ADD CONSTRAINT external_mail_connections_provider_check CHECK (provider IN ('gmail','outlook'))`,
	)
}
