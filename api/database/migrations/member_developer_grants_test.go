package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestAddMemberDeveloperGrantsUpDown stores one grant per membership, drops it with the membership,
// refuses a grant with no scope, and rolls back and forward to the golden schema.
func TestAddMemberDeveloperGrantsUpDown(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	migrated := dumpSchema(t, db)

	require.NoError(t, db.Exec(`INSERT INTO users (id, username, email, password, created_at, updated_at)
		VALUES (91, 'grant-owner', 'grant-owner@example.com', 'x', now(), now()),
		       (92, 'grant-dev', 'grant-dev@example.com', 'x', now(), now())`).Error)
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
		VALUES (81, 'Acme', 'acme-grants', 91, now(), now())`).Error)
	require.NoError(t, db.Exec(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at)
		VALUES (71, 81, 91, 'owner', now(), now()), (72, 81, 92, 'member', now(), now())`).Error)

	require.NoError(t, db.Exec(`INSERT INTO member_developer_grants
		(membership_id, organization_id, all_domains, sending_domains, granted_by, created_at, updated_at)
		VALUES (72, 81, false, '["fast-ui.com"]', 91, now(), now())`).Error)
	assert.Error(t, db.Exec(`INSERT INTO member_developer_grants
		(membership_id, organization_id, all_domains, sending_domains, granted_by, created_at, updated_at)
		VALUES (72, 81, true, '[]', 91, now(), now())`).Error, "one grant per membership")
	assert.Error(t, db.Exec(`UPDATE member_developer_grants SET all_domains = false, sending_domains = '[]'
		WHERE membership_id = 72`).Error, "a grant needs a scope")

	require.NoError(t, db.Exec(`DELETE FROM organization_memberships WHERE id = 72`).Error)
	var count int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM member_developer_grants`).Scan(&count).Error)
	assert.Zero(t, count, "removing the member must remove the grant")

	require.NoError(t, (&addMemberDeveloperGrants{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "member_developer_grants")
	require.NoError(t, (&addMemberDeveloperGrants{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}
