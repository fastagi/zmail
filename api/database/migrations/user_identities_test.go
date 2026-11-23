package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestUserIdentitiesConstraints: one Google subject links one account, an identity leaves with its
// account, and a sign-in state keeps only hashes and a bounded lifetime.
func TestUserIdentitiesConstraints(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES
		(901, 'google-one', 'one@example.com', '', now(), now()),
		(902, 'google-two', 'two@example.com', 'x', now(), now())`)
	exec(`UPDATE users SET email_verified_at = now() WHERE id = 901`)

	insert := `INSERT INTO user_identities (user_id, provider, subject, email, created_at, updated_at)
		VALUES (?, ?, ?, ?, now(), now())`
	exec(insert, 901, "google", "1001", "one@example.com")
	assert.Error(t, db.Exec(insert, 902, "google", "1001", "two@example.com").Error, "one subject links one account")
	for name, arguments := range map[string][]any{
		"unknown provider": {902, "github", "1002", "two@example.com"},
		"empty subject":    {902, "google", "", "two@example.com"},
		"long subject":     {902, "google", strings.Repeat("9", 256), "two@example.com"},
		"no account":       {999, "google", "1003", "x@example.com"},
	} {
		assert.Error(t, db.Exec(insert, arguments...).Error, name)
	}
	exec(insert, 902, "google", "1002", "two@example.com")
	exec(`DELETE FROM users WHERE id = 902`)
	var count int
	require.NoError(t, db.Raw(`SELECT count(*) FROM user_identities WHERE user_id = 902`).Scan(&count).Error)
	assert.Zero(t, count, "an identity leaves with its account")

	state := `INSERT INTO oauth_sign_in_states (state_hash, provider, nonce_hash, code_verifier, redirect_uri, return_url, expires_at, created_at)
		VALUES (?, 'google', ?, ?, 'https://app.example/api/auth/google/callback', '/console', now() + interval '10 minutes', now())`
	digest := strings.Repeat("a", 64)
	verifier := strings.Repeat("v", 43)
	exec(state, digest, digest, verifier)
	assert.Error(t, db.Exec(state, digest, digest, verifier).Error, "a state is stored once")
	assert.Error(t, db.Exec(state, "plain-state", digest, verifier).Error, "only a state digest is stored")
	assert.Error(t, db.Exec(state, strings.Repeat("b", 64), "plain-nonce", verifier).Error, "only a nonce digest is stored")
}

// TestUserIdentitiesUpDown rolls the identity tables and the verified-email column back over stored
// rows and adds them again: the result is the golden schema.
func TestUserIdentitiesUpDown(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	migrated := dumpSchema(t, db)
	require.NoError(t, db.Exec(`INSERT INTO users (id, username, email, password, email_verified_at, created_at, updated_at)
		VALUES (901, 'google-one', 'one@example.com', '', now(), now(), now())`).Error)
	require.NoError(t, db.Exec(`INSERT INTO user_identities (user_id, provider, subject, email, created_at, updated_at)
		VALUES (901, 'google', '1001', 'one@example.com', now(), now())`).Error)

	require.NoError(t, (&addUserIdentities{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "user_identities")
	assert.NotContains(t, rolledBack, "oauth_sign_in_states")
	assert.NotContains(t, rolledBack, "email_verified_at")

	require.NoError(t, (&addUserIdentities{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}
