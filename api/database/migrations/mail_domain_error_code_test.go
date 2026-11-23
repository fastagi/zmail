package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestAddMailDomainErrorCode gives existing domains an empty code and rolls back cleanly.
func TestAddMailDomainErrorCode(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	step := &addMailDomainErrorCode{}
	require.NoError(t, step.Down(db))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at)
		VALUES (911, 'domain-code-owner', 'domain-code@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
		VALUES (911, 'Domain Code Org', 'domain-code-org', 911, now(), now())`)
	exec(`INSERT INTO mail_domains (organization_id, name, zone_id, zone_name, last_error, created_by, created_at, updated_at)
		VALUES (911, 'mail.domain-code.test', 'z1', 'domain-code.test', 'cloudflare create record failed: x', 911, now(), now())`)

	require.NoError(t, step.Up(db))
	var code string
	require.NoError(t, db.Raw(`SELECT last_error_code FROM mail_domains WHERE organization_id = 911`).Scan(&code).Error)
	require.Equal(t, "", code, "an existing domain keeps its text and gets no code until its next check")

	require.NoError(t, step.Down(db))
	var columns int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'mail_domains'
		AND column_name = 'last_error_code'`).Scan(&columns).Error)
	require.Zero(t, columns)

	require.NoError(t, step.Up(db))
}
