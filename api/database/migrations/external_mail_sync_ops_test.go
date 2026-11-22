package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExternalMailSyncOpsUpDown rolls the migration back and forth over a revoked connection with
// a sync run: the result is the golden schema again, and an existing revoke starts its retention
// at the upgrade.
func TestExternalMailSyncOpsUpDown(t *testing.T) {
	db, exec := externalMailFixture(t)
	migrated := dumpSchema(t, db)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, created_at, updated_at)
		VALUES (2, 10, 901, 'owner@gmail.example', 'personal', 'connected', 901, now(), now())`)
	exec(`INSERT INTO external_mail_connections (id, organization_id, mailbox_id, provider, account_address, owner_user_id, status, next_sync_at, created_at, updated_at)
		VALUES (1, 10, 2, 'gmail', 'owner@gmail.example', 901, 'revoked', now(), now(), '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO external_mail_sync_runs (connection_id, started_at, finished_at, outcome, fetched_count, stored_count)
		VALUES (1, now(), now(), 'ok', 3, 2)`)

	require.NoError(t, (&addExternalMailPush{}).Down(db))
	require.NoError(t, (&addExternalMailTenants{}).Down(db))
	require.NoError(t, (&addSharedExternalMail{}).Down(db))
	require.NoError(t, (&addExternalMailSyncOps{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "TABLE external_mail_sync_runs")
	assert.NotContains(t, rolledBack, "idx_external_mail_connections_revoked")
	require.NoError(t, (&addExternalMailSyncOps{}).Up(db))
	require.NoError(t, (&addSharedExternalMail{}).Up(db))
	require.NoError(t, (&addExternalMailTenants{}).Up(db))
	require.NoError(t, (&addExternalMailPush{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")

	var backfilled bool
	require.NoError(t, db.Raw(`SELECT revoked_at > now() - interval '1 minute' FROM external_mail_connections WHERE id = 1`).Scan(&backfilled).Error)
	assert.True(t, backfilled, "a connection revoked before the upgrade starts its retention at the upgrade")

	for name, statement := range map[string]string{
		"unknown outcome":    `INSERT INTO external_mail_sync_runs (connection_id, started_at, finished_at, outcome) VALUES (1, now(), now(), 'crashed')`,
		"negative count":     `INSERT INTO external_mail_sync_runs (connection_id, started_at, finished_at, outcome, fetched_count) VALUES (1, now(), now(), 'ok', -1)`,
		"unknown connection": `INSERT INTO external_mail_sync_runs (connection_id, started_at, finished_at, outcome) VALUES (99, now(), now(), 'ok')`,
		"negative failures":  `UPDATE external_mail_connections SET consecutive_failures = -1 WHERE id = 1`,
	} {
		assert.Error(t, db.Exec(statement).Error, name)
	}
	exec(`INSERT INTO external_mail_sync_runs (connection_id, started_at, finished_at, outcome) VALUES (1, now(), now(), 'dropped')`)
	exec(`DELETE FROM external_mail_connections WHERE id = 1`)
	var runs int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM external_mail_sync_runs`).Scan(&runs).Error)
	assert.Zero(t, runs, "deleting a connection deletes its runs")
}
