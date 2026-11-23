package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestRosterImportColumns starts an existing invitation with the new-member grants, lets only a
// personal mailbox without an owner carry a reservation, and rolls back cleanly.
func TestRosterImportColumns(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	mailboxStep, invitationStep := &addMailboxPendingOwner{}, &addInvitationFeatureGrants{}
	require.NoError(t, mailboxStep.Down(db))
	require.NoError(t, invitationStep.Down(db))
	exec := func(statement string, arguments ...any) error {
		t.Helper()
		return db.Exec(statement, arguments...).Error
	}
	require.NoError(t, exec(`INSERT INTO users (id, username, email, password, created_at, updated_at)
		VALUES (911, 'roster-owner', 'roster-owner@example.com', 'x', now(), now())`))
	require.NoError(t, exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
		VALUES (911, 'Roster Org', 'roster-org', 911, now(), now())`))
	require.NoError(t, exec(`INSERT INTO organization_invitations
		(organization_id, invited_by, email, role, token_hash, expires_at, created_at, updated_at)
		VALUES (911, 911, 'old@example.com', 'member', repeat('a', 64), now() + interval '1 day', now(), now())`))

	require.NoError(t, invitationStep.Up(db))
	require.NoError(t, mailboxStep.Up(db))
	var grants memberFeatureRow
	require.NoError(t, db.Raw(`SELECT can_use_broadcasts, can_use_contacts, can_use_templates, can_use_api_keys
		FROM organization_invitations WHERE email = 'old@example.com'`).Scan(&grants).Error)
	require.Equal(t, memberFeatureRow{false, true, true, false}, grants, "an existing invitation keeps the new-member grants")

	require.NoError(t, exec(`INSERT INTO mailboxes (organization_id, created_by, address, type, pending_owner_email, created_at, updated_at)
		VALUES (911, 911, 'alice@roster.example', 'personal', 'alice@gmail.com', now(), now())`))
	require.Error(t, exec(`INSERT INTO mailboxes (organization_id, created_by, address, type, pending_owner_email, created_at, updated_at)
		VALUES (911, 911, 'team@roster.example', 'shared', 'bo@gmail.com', now(), now())`), "a shared mailbox is never reserved")
	require.Error(t, exec(`INSERT INTO mailboxes (organization_id, created_by, address, type, owner_user_id, pending_owner_email, created_at, updated_at)
		VALUES (911, 911, 'owned@roster.example', 'personal', 911, 'bo@gmail.com', now(), now())`), "an owned mailbox is never reserved")

	require.NoError(t, mailboxStep.Down(db))
	require.NoError(t, invitationStep.Down(db))
	var columns int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND ((table_name = 'mailboxes' AND column_name = 'pending_owner_email')
		    OR (table_name = 'organization_invitations' AND column_name LIKE 'can_use_%'))`).Scan(&columns).Error)
	require.Zero(t, columns)
	require.NoError(t, invitationStep.Up(db))
	require.NoError(t, mailboxStep.Up(db))
}
