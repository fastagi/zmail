package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// mailTriageFixture migrates everything and seeds an organization, a user, a mailbox, and one
// inbound message.
func mailTriageFixture(t *testing.T) (*gorm.DB, func(string, ...any)) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'triage-owner', 'triage-owner@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, thread_id, created_at, updated_at)
		VALUES (11, 1, 'inbound', 'received', 'alice@sender.test', 11, now(), now())`)
	return db, exec
}

// TestMailTriageMigrationUpDown adds triage to a database with mail in it, rolls it back over
// stored results and settings, and adds it again: the result is the golden schema, and every
// existing mailbox starts with the switch off.
func TestMailTriageMigrationUpDown(t *testing.T) {
	db, exec := mailTriageFixture(t)
	migrated := dumpSchema(t, db)

	var enabled bool
	require.NoError(t, db.Raw(`SELECT ai_triage_enabled FROM mailboxes WHERE id = 1`).Scan(&enabled).Error)
	assert.False(t, enabled, "an existing mailbox starts with AI triage off")

	exec(`INSERT INTO mail_triage_settings (organization_id, enabled, daily_message_cap, draft_enabled, created_at, updated_at)
		VALUES (10, true, 500, true, now(), now())`)
	exec(`INSERT INTO mail_triage (message_id, mailbox_id, category, reason, source, status, next_attempt_at, created_at, updated_at)
		VALUES (11, 1, 'reply', 'Asks a question', 'ai', 'done', now(), now(), now())`)

	require.NoError(t, (&addMailTriage{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "TABLE mail_triage")
	assert.NotContains(t, rolledBack, "ai_triage_enabled")

	require.NoError(t, (&addMailTriage{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestMailTriageConstraints keeps the triage rules in the schema, so a write that skips the
// service still cannot break them.
func TestMailTriageConstraints(t *testing.T) {
	db, exec := mailTriageFixture(t)
	exec(`INSERT INTO mail_triage (message_id, mailbox_id, status, next_attempt_at, created_at, updated_at)
		VALUES (11, 1, 'pending', now(), now(), now())`)

	for name, statement := range map[string]string{
		"unknown category":        `UPDATE mail_triage SET category = 'spam', status = 'done' WHERE message_id = 11`,
		"unknown status":          `UPDATE mail_triage SET status = 'lost' WHERE message_id = 11`,
		"unknown source":          `UPDATE mail_triage SET source = 'robot' WHERE message_id = 11`,
		"done without a category": `UPDATE mail_triage SET status = 'done' WHERE message_id = 11`,
		"due date outside deadline": `UPDATE mail_triage SET status = 'done', category = 'fyi', due_at = now()
			WHERE message_id = 11`,
		"reason longer than 120": `UPDATE mail_triage SET status = 'done', category = 'fyi', reason = repeat('x', 121)
			WHERE message_id = 11`,
		"cap below one": `INSERT INTO mail_triage_settings (organization_id, daily_message_cap, created_at, updated_at)
			VALUES (10, 0, now(), now())`,
	} {
		assert.Error(t, db.Exec(statement).Error, name)
	}

	var settings struct {
		Enabled, DraftEnabled bool
		DailyMessageCap       int
	}
	exec(`INSERT INTO mail_triage_settings (organization_id, created_at, updated_at) VALUES (10, now(), now())`)
	require.NoError(t, db.Raw(`SELECT enabled, draft_enabled, daily_message_cap FROM mail_triage_settings WHERE organization_id = 10`).
		Scan(&settings).Error)
	assert.False(t, settings.Enabled, "the organization switch defaults off")
	assert.False(t, settings.DraftEnabled, "drafts default off")
	assert.Equal(t, 500, settings.DailyMessageCap)

	exec(`DELETE FROM mail_messages WHERE id = 11`)
	var left int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mail_triage`).Scan(&left).Error)
	assert.Zero(t, left, "deleting a message deletes its triage")
}
