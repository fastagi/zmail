package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestAddAPIKeyModeMakesExistingKeysLive keeps every key issued before modes existed acting for
// real, refuses unknown modes, revokes test keys on rollback, and rolls back and forward cleanly.
func TestAddAPIKeyModeMakesExistingKeysLive(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	require.NoError(t, (&addAPIKeyMode{}).Down(db))
	require.NoError(t, db.Exec(`INSERT INTO api_keys (user_id, name, key_prefix, key_hash, scopes, created_at, updated_at)
		VALUES (7, 'deploy', 'fastbuild_ab12cd34', 'hash-old', '["mail:send"]', now(), now())`).Error)

	require.NoError(t, (&addAPIKeyMode{}).Up(db))

	var mode string
	require.NoError(t, db.Raw(`SELECT mode FROM api_keys WHERE key_hash = 'hash-old'`).Scan(&mode).Error)
	require.Equal(t, "live", mode)
	require.NoError(t, db.Exec(`INSERT INTO api_keys (user_id, name, key_prefix, key_hash, scopes, mode, created_at, updated_at)
		VALUES (7, 'sandbox', 'fastbuild_test_ab12cd34', 'hash-test', '[]', 'test', now(), now())`).Error)
	require.Error(t, db.Exec(`INSERT INTO api_keys (user_id, name, key_prefix, key_hash, scopes, mode, created_at, updated_at)
		VALUES (7, 'bad', 'fastbuild_zz', 'hash-bad', '[]', 'staging', now(), now())`).Error)

	require.NoError(t, (&addAPIKeyMode{}).Down(db))
	// Down revokes the test key, so older code that treats every key as live cannot use it.
	var revoked struct{ Test, Live bool }
	require.NoError(t, db.Raw(`SELECT
		(SELECT revoked_at IS NOT NULL FROM api_keys WHERE key_hash = 'hash-test') AS test,
		(SELECT revoked_at IS NOT NULL FROM api_keys WHERE key_hash = 'hash-old') AS live`).Scan(&revoked).Error)
	require.True(t, revoked.Test, "a test key survived the rollback unrevoked")
	require.False(t, revoked.Live, "the rollback revoked a live key")
	require.NoError(t, (&addAPIKeyMode{}).Up(db))
}
