package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_08_090000_add_external_mail_tenants", &addExternalMailTenants{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addExternalMailTenants struct {
	migration.BaseMigration
}

// Up lets an organization owner authorize a whole Google Workspace or Microsoft 365 tenant once:
//
//   - external_mail_tenants holds one sealed tenant credential per authorization. A tenant identifier
//     is live in at most one organization of the instance (idx_external_mail_tenants_live); revoked
//     rows stay for the record, without their credential.
//   - external_mail_connections.tenant_id marks a connection that reads through a tenant; it never
//     stores a per-account token, which the CHECK keeps. tenant_directory_id is the mailbox's stable
//     id in the tenant's directory. idx_external_mail_connections_tenant serves the per-tenant count,
//     the stop of every connection when the credential is lost, and the revocation.
//   - mailboxes.external_mail_tenant_id marks a connected mailbox assigned from a tenant: its address
//     is the organization's, so the organization administers it and a personal one awaits handover
//     when its owner leaves. idx_mailboxes_external_mail_tenant finds a tenant's mailboxes.
//   - external_mail_address_records remembers, per address (and directory user), every reader it
//     ever had in the organization, by any kind of binding: who holds it now, its privacy floor,
//     which only ever rises, the one member the floor does not apply to (the first holder, for as long
//     as nobody else has held it), and whether a tenant ever assigned it. It outlives revocation,
//     purge, departure, and handover, so a tenant assignment never gives someone mail from another
//     reader's tenure. Existing connections are recorded as their first holders.
//   - external_mail_connections.received_not_before is that boundary on a connection: no copy of
//     mail received before it is ever stored.
//
// The new columns are nullable or have constant defaults, which PostgreSQL 11+ records without
// rewriting the tables.
func (m *addExternalMailTenants) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE external_mail_tenants (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  provider varchar(16) NOT NULL,
		  tenant_identifier varchar(255) NOT NULL,
		  display_name varchar(255) NOT NULL DEFAULT '',
		  status varchar(24) NOT NULL DEFAULT 'active',
		  encrypted_credential text NOT NULL DEFAULT '',
		  authorized_by_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  last_error_code varchar(64),
		  authorized_at timestamptz NOT NULL,
		  revoked_at timestamptz,
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL,
		  CONSTRAINT chk_external_mail_tenants_provider CHECK (provider ~ '^[a-z][a-z0-9_]{1,15}$'),
		  CONSTRAINT chk_external_mail_tenants_status CHECK (status IN ('active','needs_reauthorization','revoked')),
		  CONSTRAINT chk_external_mail_tenants_identifier CHECK (tenant_identifier <> ''),
		  CONSTRAINT chk_external_mail_tenants_credential CHECK (status = 'revoked' OR encrypted_credential <> '')
		)`,
		`CREATE UNIQUE INDEX idx_external_mail_tenants_live ON external_mail_tenants (provider, lower(tenant_identifier))
		  WHERE status <> 'revoked'`,
		`CREATE INDEX idx_external_mail_tenants_organization ON external_mail_tenants (organization_id, id)`,
		`ALTER TABLE external_mail_connections
		  ADD COLUMN tenant_id bigint REFERENCES external_mail_tenants(id),
		  ADD COLUMN tenant_directory_id varchar(255) NOT NULL DEFAULT '',
		  ADD COLUMN received_not_before timestamptz,
		  ADD CONSTRAINT chk_external_mail_connections_tenant_token CHECK (tenant_id IS NULL OR encrypted_refresh_token = '')`,
		`CREATE INDEX idx_external_mail_connections_tenant ON external_mail_connections (tenant_id, status)
		  WHERE tenant_id IS NOT NULL`,
		`ALTER TABLE mailboxes
		  ADD COLUMN external_mail_tenant_id bigint REFERENCES external_mail_tenants(id),
		  ADD CONSTRAINT chk_mailboxes_tenant_origin CHECK (external_mail_tenant_id IS NULL OR origin = 'connected')`,
		`CREATE INDEX idx_mailboxes_external_mail_tenant ON mailboxes (external_mail_tenant_id)
		  WHERE external_mail_tenant_id IS NOT NULL`,
		`CREATE TABLE external_mail_address_records (
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  provider varchar(16) NOT NULL,
		  address_key varchar(320) NOT NULL,
		  directory_id varchar(255) NOT NULL DEFAULT '',
		  user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  tenant_id bigint REFERENCES external_mail_tenants(id),
		  tenant_held boolean NOT NULL DEFAULT false,
		  assigned_at timestamptz NOT NULL,
		  floor_at timestamptz,
		  exempt_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
		  released boolean NOT NULL DEFAULT false,
		  PRIMARY KEY (organization_id, provider, address_key),
		  CONSTRAINT chk_external_mail_address_records_key CHECK (address_key = lower(address_key) AND address_key <> '')
		)`,
		`CREATE INDEX idx_external_mail_address_records_directory ON external_mail_address_records
		  (organization_id, provider, directory_id) WHERE directory_id <> ''`,
		`INSERT INTO external_mail_address_records (organization_id, provider, address_key, user_id, exempt_user_id, assigned_at)
		  SELECT organization_id, provider, lower(account_address), owner_user_id, CASE WHEN shared THEN NULL ELSE owner_user_id END, created_at
		  FROM external_mail_connections
		  ON CONFLICT DO NOTHING`,
	)
}

// Down drops tenants. It fails while a connection or mailbox still comes from one, since the older
// schema would read a tenant's connection as a member's own without a token; revoke the tenants and
// purge their copies first. Tenant rows themselves hold nothing else and are dropped.
func (m *addExternalMailTenants) Down(db *gorm.DB) error {
	return execStatements(db,
		`DO $$
		BEGIN
		  IF EXISTS (SELECT 1 FROM external_mail_connections WHERE tenant_id IS NOT NULL)
		     OR EXISTS (SELECT 1 FROM mailboxes WHERE external_mail_tenant_id IS NOT NULL) THEN
		    RAISE EXCEPTION 'a mailbox assigned from an external mail tenant exists; the older schema cannot express it';
		  END IF;
		END $$`,
		`DROP TABLE IF EXISTS external_mail_address_records`,
		`DROP INDEX IF EXISTS idx_mailboxes_external_mail_tenant`,
		`ALTER TABLE mailboxes
		  DROP CONSTRAINT IF EXISTS chk_mailboxes_tenant_origin,
		  DROP COLUMN IF EXISTS external_mail_tenant_id`,
		`DROP INDEX IF EXISTS idx_external_mail_connections_tenant`,
		`ALTER TABLE external_mail_connections
		  DROP CONSTRAINT IF EXISTS chk_external_mail_connections_tenant_token,
		  DROP COLUMN IF EXISTS received_not_before,
		  DROP COLUMN IF EXISTS tenant_directory_id,
		  DROP COLUMN IF EXISTS tenant_id`,
		`DROP TABLE IF EXISTS external_mail_tenants`,
	)
}
