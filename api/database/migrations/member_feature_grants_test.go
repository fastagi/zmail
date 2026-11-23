package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

type memberFeatureRow struct {
	CanUseBroadcasts bool
	CanUseContacts   bool
	CanUseTemplates  bool
	CanUseAPIKeys    bool `gorm:"column:can_use_api_keys"`
}

// TestAddMemberFeatureGrants keeps every existing member able to do what it did before, starts a
// new member with contacts and templates only, and rolls back cleanly.
func TestAddMemberFeatureGrants(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	step := &addMemberFeatureGrants{}
	require.NoError(t, step.Down(db))
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES
		(901, 'feature-owner', 'feature-owner@example.com', 'x', now(), now()),
		(902, 'feature-old', 'feature-old@example.com', 'x', now(), now()),
		(903, 'feature-new', 'feature-new@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
		VALUES (901, 'Feature Org', 'feature-org', 901, now(), now())`)
	exec(`INSERT INTO organization_memberships (organization_id, user_id, role, created_at, updated_at)
		VALUES (901, 901, 'owner', now(), now()), (901, 902, 'member', now(), now())`)

	require.NoError(t, step.Up(db))
	read := func(userID int) memberFeatureRow {
		t.Helper()
		var row memberFeatureRow
		require.NoError(t, db.Raw(`SELECT can_use_broadcasts, can_use_contacts, can_use_templates, can_use_api_keys
			FROM organization_memberships WHERE user_id = ?`, userID).Scan(&row).Error)
		return row
	}
	require.Equal(t, memberFeatureRow{true, true, true, true}, read(902), "an existing member keeps every feature")

	exec(`INSERT INTO organization_memberships (organization_id, user_id, role, created_at, updated_at)
		VALUES (901, 903, 'member', now(), now())`)
	require.Equal(t, memberFeatureRow{false, true, true, false}, read(903),
		"a new member waits for broadcasts and API keys")

	require.NoError(t, step.Down(db))
	var columns int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'organization_memberships'
		AND column_name LIKE 'can_use_%'`).Scan(&columns).Error)
	require.Zero(t, columns)

	require.NoError(t, step.Up(db))
}
