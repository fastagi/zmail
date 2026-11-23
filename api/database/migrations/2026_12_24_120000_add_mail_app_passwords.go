package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_24_120000_add_mail_app_passwords", &addMailAppPasswords{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailAppPasswords struct {
	migration.BaseMigration
}

// Up adds per-device mail client passwords and the organization switch for mail apps. A password
// keeps only a keyed hash of its secret; a missing switch row means mail apps are allowed.
func (m *addMailAppPasswords) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_app_passwords (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    membership_id bigint NOT NULL,
		    user_id bigint NOT NULL,
		    name varchar(64) NOT NULL,
		    scope_imap boolean NOT NULL,
		    scope_smtp boolean NOT NULL,
		    secret_hash varchar(64) NOT NULL,
		    hint varchar(4) NOT NULL,
		    last_used_at timestamptz,
		    last_used_ip varchar(64),
		    last_used_protocol varchar(8),
		    revoked_at timestamptz,
		    revoked_by bigint,
		    revoke_reason varchar(16),
		    created_at timestamptz NOT NULL,
		    updated_at timestamptz NOT NULL,
		    CONSTRAINT mail_app_passwords_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_mail_app_passwords_scope CHECK (scope_imap OR scope_smtp),
		    CONSTRAINT chk_mail_app_passwords_name CHECK (char_length(name) BETWEEN 1 AND 64),
		    CONSTRAINT chk_mail_app_passwords_secret_hash CHECK (secret_hash ~ '^[0-9a-f]{64}$'),
		    CONSTRAINT chk_mail_app_passwords_protocol CHECK (last_used_protocol IN ('imap', 'smtp')),
		    CONSTRAINT chk_mail_app_passwords_revoke_reason CHECK (revoke_reason IN ('user', 'admin')),
		    CONSTRAINT chk_mail_app_passwords_revoked CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
		)`,
		`CREATE UNIQUE INDEX idx_mail_app_passwords_secret ON mail_app_passwords (secret_hash)`,
		`CREATE INDEX idx_mail_app_passwords_membership ON mail_app_passwords (membership_id) WHERE revoked_at IS NULL`,
		`CREATE INDEX idx_mail_app_passwords_organization ON mail_app_passwords (organization_id, id)`,
		`CREATE INDEX idx_mail_app_passwords_revoked ON mail_app_passwords (revoked_at) WHERE revoked_at IS NOT NULL`,
		`ALTER TABLE mail_app_passwords ADD CONSTRAINT fk_mail_app_passwords_organization
		    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE`,
		`ALTER TABLE mail_app_passwords ADD CONSTRAINT fk_mail_app_passwords_membership
		    FOREIGN KEY (membership_id) REFERENCES organization_memberships(id) ON DELETE CASCADE`,
		`ALTER TABLE mail_app_passwords ADD CONSTRAINT fk_mail_app_passwords_user
		    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE`,
		`ALTER TABLE mail_app_passwords ADD CONSTRAINT fk_mail_app_passwords_revoked_by
		    FOREIGN KEY (revoked_by) REFERENCES users(id) ON DELETE SET NULL`,
		`CREATE TABLE mail_client_settings (
		    organization_id bigint NOT NULL,
		    access_enabled boolean DEFAULT true NOT NULL,
		    updated_by bigint,
		    created_at timestamptz NOT NULL,
		    updated_at timestamptz NOT NULL,
		    CONSTRAINT mail_client_settings_pkey PRIMARY KEY (organization_id)
		)`,
		`ALTER TABLE mail_client_settings ADD CONSTRAINT fk_mail_client_settings_organization
		    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE`,
		`ALTER TABLE mail_client_settings ADD CONSTRAINT fk_mail_client_settings_updated_by
		    FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL`,
	)
}

// Down drops every mail client password and the switch; mail apps can no longer sign in.
func (m *addMailAppPasswords) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS mail_client_settings`,
		`DROP TABLE IF EXISTS mail_app_passwords`,
	)
}
