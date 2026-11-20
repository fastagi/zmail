package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestAddPlatformOperatorScope keeps every existing grant full, accepts only the two scopes, and on
// rollback removes metrics-only grants so dropping the column never widens one into a full grant.
func TestAddPlatformOperatorScope(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	step := &addPlatformOperatorScope{}
	require.NoError(t, step.Down(db))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES
		(801, 'scope-full', 'scope-full@example.com', 'x', now(), now()),
		(802, 'scope-metrics', 'scope-metrics@example.com', 'x', now(), now())`)
	exec(`INSERT INTO platform_operators (user_id, granted_at) VALUES (801, now())`)

	require.NoError(t, step.Up(db))
	var scope string
	require.NoError(t, db.Raw(`SELECT scope FROM platform_operators WHERE user_id = 801`).Scan(&scope).Error)
	require.Equal(t, "full", scope, "an existing grant stays full")

	exec(`INSERT INTO platform_operators (user_id, granted_at, scope) VALUES (802, now(), 'mail_metrics')`)
	require.Error(t, db.Exec(`UPDATE platform_operators SET scope = 'admin' WHERE user_id = 801`).Error)

	require.NoError(t, step.Down(db))
	var users []int
	require.NoError(t, db.Raw(`SELECT user_id FROM platform_operators ORDER BY user_id`).Scan(&users).Error)
	require.Equal(t, []int{801}, users, "Down must drop metrics-only grants instead of widening them")
	var columns int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'platform_operators' AND column_name = 'scope'`).
		Scan(&columns).Error)
	require.Zero(t, columns)

	require.NoError(t, step.Up(db))
}
