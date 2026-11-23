package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_29_090000_add_user_identities", &addUserIdentities{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addUserIdentities struct {
	migration.BaseMigration
}

// Up adds "Sign in with Google": the link from a Google subject to an account, the short-lived
// sign-in states (digests of the state and nonce, the PKCE verifier, and where to return), and the
// time an account's email was proven. Adding the nullable column needs only a brief ACCESS
// EXCLUSIVE lock on users; nothing is rewritten.
func (m *addUserIdentities) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE users ADD COLUMN email_verified_at timestamptz`,
		`CREATE TABLE user_identities (
		    id bigserial NOT NULL,
		    user_id bigint NOT NULL,
		    provider varchar(16) NOT NULL,
		    subject varchar(255) NOT NULL,
		    email varchar(100) NOT NULL,
		    last_sign_in_at timestamptz,
		    created_at timestamptz NOT NULL,
		    updated_at timestamptz NOT NULL,
		    CONSTRAINT user_identities_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_user_identities_provider CHECK (provider IN ('google')),
		    CONSTRAINT chk_user_identities_subject CHECK (char_length(subject) BETWEEN 1 AND 255)
		)`,
		`CREATE UNIQUE INDEX idx_user_identities_subject ON user_identities (provider, subject)`,
		`CREATE INDEX idx_user_identities_user ON user_identities (user_id)`,
		`ALTER TABLE user_identities ADD CONSTRAINT fk_user_identities_user
		    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE`,
		`CREATE TABLE oauth_sign_in_states (
		    state_hash varchar(64) NOT NULL,
		    provider varchar(16) NOT NULL,
		    nonce_hash varchar(64) NOT NULL,
		    code_verifier varchar(128) NOT NULL,
		    redirect_uri varchar(2048) NOT NULL,
		    return_url varchar(2048) NOT NULL,
		    expires_at timestamptz NOT NULL,
		    created_at timestamptz NOT NULL,
		    CONSTRAINT oauth_sign_in_states_pkey PRIMARY KEY (state_hash),
		    CONSTRAINT chk_oauth_sign_in_states_provider CHECK (provider IN ('google')),
		    CONSTRAINT chk_oauth_sign_in_states_state_hash CHECK (state_hash ~ '^[0-9a-f]{64}$'),
		    CONSTRAINT chk_oauth_sign_in_states_nonce_hash CHECK (nonce_hash ~ '^[0-9a-f]{64}$')
		)`,
		`CREATE INDEX idx_oauth_sign_in_states_expires ON oauth_sign_in_states (expires_at)`,
	)
}

// Down drops every Google link and pending sign-in; linked accounts keep their email and password
// and can still sign in with a password (or set one through password reset).
func (m *addUserIdentities) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS oauth_sign_in_states`,
		`DROP TABLE IF EXISTS user_identities`,
		`ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at`,
	)
}
