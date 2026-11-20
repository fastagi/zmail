package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// transactionalFixture migrates a schema and adds one organization (10) with a user (901).
func transactionalFixture(t *testing.T) (*gorm.DB, func(string, ...any) error) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	exec := func(statement string, arguments ...any) error {
		t.Helper()
		return db.Exec(statement, arguments...).Error
	}
	require.NoError(t, exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'tx-owner', 'tx-owner@example.com', 'x', now(), now())`))
	require.NoError(t, exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`))
	require.NoError(t, exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (11, 'Other', 'other', 901, now(), now())`))
	return db, exec
}

// insertTransactional stores one queued live message with an encrypted payload.
func insertTransactional(exec func(string, ...any) error, id int, mode string, simulated bool) error {
	return exec(`INSERT INTO transactional_messages (id, organization_id, mode, simulated, created_by, template_key,
		from_address, from_name, to_address, subject_masked, variables_masked, payload_ciphertext, idempotency_fingerprint,
		status, next_attempt_at, rfc_message_id, created_at, updated_at)
		VALUES (?, 10, ?, ?, 901, 'signup-code', 'no-reply@acme.test', 'Acme', 'li@example.com', 'Code ••••••', '{}'::jsonb,
		'\x01'::bytea, '\x02'::bytea, 'queued', now(), ?, now(), now())`,
		id, mode, simulated, "m"+string(rune('a'+id))+"@acme.test")
}

// TestTransactionalMigrationsUpDown creates both tables with every index, and rolls them back.
func TestTransactionalMigrationsUpDown(t *testing.T) {
	db, exec := transactionalFixture(t)
	require.NoError(t, insertTransactional(exec, 1, "live", false))
	require.NoError(t, exec(`INSERT INTO transactional_message_events (message_id, type, provider_event_id, occurred_at, created_at)
		VALUES (1, 'accepted', 'evt-1', now(), now())`))
	// A provider event is stored once.
	require.Error(t, exec(`INSERT INTO transactional_message_events (message_id, type, provider_event_id, occurred_at, created_at)
		VALUES (1, 'delivered', 'evt-1', now(), now())`))
	require.Error(t, exec(`INSERT INTO transactional_message_events (message_id, type, occurred_at, created_at)
		VALUES (1, 'opened', now(), now())`))
	// Mode, status and queued reason are closed sets.
	require.Error(t, insertTransactional(exec, 2, "staging", false))
	require.Error(t, exec(`UPDATE transactional_messages SET queued_reason = 'later' WHERE id = 1`))

	var indexes []string
	require.NoError(t, db.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname = current_schema()
		AND tablename IN ('transactional_messages', 'transactional_message_events') ORDER BY indexname`).Scan(&indexes).Error)
	require.Equal(t, []string{
		"transactional_message_events_message_idx",
		"transactional_message_events_pkey",
		"transactional_message_events_provider_idx",
		"transactional_messages_attempted_idx",
		"transactional_messages_created_idx",
		"transactional_messages_due_idx",
		"transactional_messages_idempotency_idx",
		"transactional_messages_log_idx",
		"transactional_messages_pkey",
		"transactional_messages_provider_idx",
		"transactional_messages_rfc_idx",
		"transactional_messages_template_idx",
		"transactional_messages_to_idx",
	}, indexes)

	// Deleting the message removes its events; deleting the organization removes its messages.
	require.NoError(t, exec(`DELETE FROM organizations WHERE id = 10`))
	var events int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM transactional_message_events`).Scan(&events).Error)
	require.Zero(t, events)

	require.NoError(t, (&createMailSuppressions{}).Down(db))
	require.NoError(t, (&createTransactionalMessages{}).Down(db))
	var tables int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema()
		AND table_name IN ('transactional_messages', 'transactional_message_events', 'mail_suppressions')`).Scan(&tables).Error)
	require.Zero(t, tables)
	require.NoError(t, (&createTransactionalMessages{}).Up(db))
	require.NoError(t, (&createMailSuppressions{}).Up(db))
}

// TestTerminalRowRejectsCiphertext keeps the payload only while a message is queued, and the
// queue and lease fields only while it waits.
func TestTerminalRowRejectsCiphertext(t *testing.T) {
	_, exec := transactionalFixture(t)
	require.NoError(t, insertTransactional(exec, 1, "live", false))
	require.Error(t, exec(`UPDATE transactional_messages SET status = 'sent', next_attempt_at = NULL WHERE id = 1`),
		"a terminal row must not keep its ciphertext")
	require.Error(t, exec(`UPDATE transactional_messages SET status = 'failed', payload_ciphertext = NULL WHERE id = 1`),
		"a terminal row must not keep its next attempt")
	require.Error(t, exec(`UPDATE transactional_messages SET status = 'failed', payload_ciphertext = NULL, next_attempt_at = NULL,
		lease_until = now() WHERE id = 1`), "a terminal row must not keep its lease")
	require.NoError(t, exec(`UPDATE transactional_messages SET status = 'sent', payload_ciphertext = NULL, next_attempt_at = NULL WHERE id = 1`))
}

// TestSimulatedRowRejectsAttemptTime: only live, real messages record a provider attempt.
func TestSimulatedRowRejectsAttemptTime(t *testing.T) {
	_, exec := transactionalFixture(t)
	require.NoError(t, insertTransactional(exec, 1, "live", false))
	require.NoError(t, insertTransactional(exec, 2, "live", true))
	require.NoError(t, insertTransactional(exec, 3, "test", false))
	require.NoError(t, exec(`UPDATE transactional_messages SET send_attempted_at = now() WHERE id = 1`))
	require.Error(t, exec(`UPDATE transactional_messages SET send_attempted_at = now() WHERE id = 2`))
	require.Error(t, exec(`UPDATE transactional_messages SET send_attempted_at = now() WHERE id = 3`))
}

// TestSuppressionBackfillTakesOnlyBouncedAndComplained copies contacts suppressed by a bounce or a
// complaint, lowercased and once per organization, and leaves contacts rejected at send time out.
func TestSuppressionBackfillTakesOnlyBouncedAndComplained(t *testing.T) {
	db, exec := transactionalFixture(t)
	require.NoError(t, (&createMailSuppressions{}).Down(db))
	contact := func(id, organization int, email, status, reason string) {
		t.Helper()
		require.NoError(t, exec(`INSERT INTO mail_contacts (id, organization_id, email, status, status_reason, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, now(), now())`, id, organization, email, status, reason))
	}
	contact(1, 10, "Bounce@Example.com", "suppressed", "bounced")
	contact(2, 10, "spam@example.com", "suppressed", "complained")
	contact(3, 10, "rejected@example.com", "suppressed", "rejected")
	contact(4, 10, "gone@example.com", "unsubscribed", "")
	contact(5, 10, "ok@example.com", "subscribed", "")
	contact(6, 11, "bounce@example.com", "suppressed", "bounced")

	require.NoError(t, (&createMailSuppressions{}).Up(db))

	var rows []struct {
		OrganizationID int
		Email          string
		Reason         string
		Source         string
	}
	require.NoError(t, db.Raw(`SELECT organization_id, email, reason, source FROM mail_suppressions ORDER BY organization_id, email`).
		Scan(&rows).Error)
	require.Len(t, rows, 3)
	require.Equal(t, 10, rows[0].OrganizationID)
	require.Equal(t, "bounce@example.com", rows[0].Email)
	require.Equal(t, "bounced", rows[0].Reason)
	require.Equal(t, "backfill", rows[0].Source)
	require.Equal(t, "spam@example.com", rows[1].Email)
	require.Equal(t, "complained", rows[1].Reason)
	require.Equal(t, 11, rows[2].OrganizationID)

	// One row per address in an organization; reason and source are closed sets.
	require.Error(t, exec(`INSERT INTO mail_suppressions (organization_id, email, reason, source, created_at)
		VALUES (10, 'bounce@example.com', 'complained', 'transactional', now())`))
	require.Error(t, exec(`INSERT INTO mail_suppressions (organization_id, email, reason, source, created_at)
		VALUES (10, 'x@example.com', 'rejected', 'transactional', now())`))
	require.Error(t, exec(`INSERT INTO mail_suppressions (organization_id, email, reason, source, created_at)
		VALUES (10, 'x@example.com', 'bounced', 'manual', now())`))
}
