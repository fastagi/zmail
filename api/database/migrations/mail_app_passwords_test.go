package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMailAppPasswordsConstraints: a password needs a scope, a name, a hex digest that no other
// password has, and leaves with its membership; every membership and invitation may use mail apps.
func TestMailAppPasswordsConstraints(t *testing.T) {
	db, exec := mailClientFixture(t)
	insert := `INSERT INTO mail_app_passwords (organization_id, membership_id, user_id, name, scope_imap, scope_smtp,
		secret_hash, hint, revoked_at, revoke_reason, created_at, updated_at)
		VALUES (10, 101, 902, ?, ?, ?, ?, 'mnop', NULL, NULL, now(), now())`
	digest := strings.Repeat("b", 64)
	for name, arguments := range map[string][]any{
		"no scope":       {"Phone", false, false, digest},
		"empty name":     {"", true, true, digest},
		"name too long":  {strings.Repeat("x", 65), true, true, digest},
		"digest not hex": {"Phone", true, false, "zz"},
	} {
		assert.Error(t, db.Exec(insert, arguments...).Error, name)
	}
	exec(insert, "Phone", true, true, digest)
	assert.Error(t, db.Exec(insert, "Laptop", true, false, digest).Error, "the same digest twice")
	assert.Error(t, db.Exec(`UPDATE mail_app_passwords SET revoked_at = now()`).Error, "a revocation needs its reason")
	assert.Error(t, db.Exec(`UPDATE mail_app_passwords SET revoked_at = now(), revoke_reason = 'expired'`).Error, "an unknown reason")
	exec(`UPDATE mail_app_passwords SET revoked_at = now(), revoke_reason = 'admin', revoked_by = 901`)

	var defaults []bool
	require.NoError(t, db.Raw(`SELECT can_use_mail_clients FROM organization_memberships ORDER BY id`).Scan(&defaults).Error)
	assert.Equal(t, []bool{true, true}, defaults)
	exec(`INSERT INTO organization_invitations (organization_id, invited_by, email, role, token_hash, expires_at, created_at, updated_at)
		VALUES (10, 901, 'new@example.com', 'member', ?, now() + interval '1 day', now(), now())`, strings.Repeat("c", 64))
	var invitation bool
	require.NoError(t, db.Raw(`SELECT can_use_mail_clients FROM organization_invitations`).Scan(&invitation).Error)
	assert.True(t, invitation)

	exec(`INSERT INTO mail_client_settings (organization_id, access_enabled, created_at, updated_at) VALUES (10, false, now(), now())`)
	exec(`DELETE FROM organization_memberships WHERE id = 101`)
	var count int
	require.NoError(t, db.Raw(`SELECT count(*) FROM mail_app_passwords`).Scan(&count).Error)
	assert.Zero(t, count, "leaving the organization deletes the member's passwords")
	exec(`DELETE FROM organizations WHERE id = 10`)
	require.NoError(t, db.Raw(`SELECT count(*) FROM mail_client_settings`).Scan(&count).Error)
	assert.Zero(t, count)
}

// TestMailAppPasswordsUpDown rolls the password and grant migrations back and applies them again.
func TestMailAppPasswordsUpDown(t *testing.T) {
	db, _ := mailClientFixture(t)
	migrated := dumpSchema(t, db)
	require.NoError(t, (&addMailAppPasswords{}).Down(db))
	require.NoError(t, (&addMailClientFeatureGrants{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "mail_app_passwords")
	assert.NotContains(t, rolledBack, "mail_client_settings")
	assert.NotContains(t, rolledBack, "can_use_mail_clients")
	require.NoError(t, (&addMailClientFeatureGrants{}).Up(db))
	require.NoError(t, (&addMailAppPasswords{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db))
}
