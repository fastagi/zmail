package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestAddPlatformOperatorMailContent gives no existing grant content, full grants included, and
// rolls back by narrowing grants only.
func TestAddPlatformOperatorMailContent(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	step := &addPlatformOperatorMailContent{}
	require.NoError(t, step.Down(db))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES
		(811, 'content-full', 'content-full@example.com', 'x', now(), now()),
		(812, 'content-metrics', 'content-metrics@example.com', 'x', now(), now())`)
	exec(`INSERT INTO platform_operators (user_id, granted_at, scope) VALUES (811, now(), 'full'), (812, now(), 'mail_metrics')`)

	require.NoError(t, step.Up(db))
	var withContent int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM platform_operators WHERE mail_content`).Scan(&withContent).Error)
	require.Zero(t, withContent, "no existing grant, full included, reads bodies after the upgrade")

	exec(`UPDATE platform_operators SET mail_content = true WHERE user_id = 812`)
	require.NoError(t, step.Down(db))
	var users []int
	require.NoError(t, db.Raw(`SELECT user_id FROM platform_operators ORDER BY user_id`).Scan(&users).Error)
	require.Equal(t, []int{811, 812}, users, "rolling back keeps every grant and its scope")
	require.NoError(t, step.Up(db))
}

// TestCreateMailContentAccessAudits keeps reasons non-blank and bounded and rolls back cleanly.
func TestCreateMailContentAccessAudits(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	step := &createMailContentAccessAudits{}
	insert := func(reason string) error {
		return db.Exec(`INSERT INTO mail_content_access_audits
			(viewer_user_id, organization_id, message_id, reason, accessed_at) VALUES (1, 2, 3, ?, now())`, reason).Error
	}
	require.NoError(t, insert("Complaint ticket 42"))
	require.Error(t, insert("   "), "a blank reason is rejected")
	long := make([]byte, 501)
	for index := range long {
		long[index] = 'a'
	}
	require.Error(t, insert(string(long)), "a reason over 500 characters is rejected")

	require.NoError(t, step.Down(db))
	var tables int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'mail_content_access_audits'`).Scan(&tables).Error)
	require.Zero(t, tables)
	require.NoError(t, step.Up(db))
}
