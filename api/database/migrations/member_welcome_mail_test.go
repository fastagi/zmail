package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemberWelcomeMailConstraints: one welcome per mailbox, a known language, bounded copy, and
// both tables leave with their organization; a purged message only clears the link.
func TestMemberWelcomeMailConstraints(t *testing.T) {
	db, exec := mailClientFixture(t)

	exec(`INSERT INTO member_welcome_settings (organization_id, created_at, updated_at) VALUES (10, now(), now())`)
	var defaults struct {
		Enabled  bool
		Language string
		Subject  string
		Body     string
	}
	require.NoError(t, db.Raw(`SELECT enabled, language, subject, body FROM member_welcome_settings`).Scan(&defaults).Error)
	assert.True(t, defaults.Enabled, "a welcome mail is on until an admin turns it off")
	assert.Equal(t, "zh-Hans", defaults.Language)
	assert.Empty(t, defaults.Subject+defaults.Body, "empty copy means the standard text")
	assert.Error(t, db.Exec(`UPDATE member_welcome_settings SET language = 'fr-FR'`).Error, "an unknown language")
	assert.Error(t, db.Exec(`UPDATE member_welcome_settings SET body = ?`, strings.Repeat("x", 5001)).Error, "a body too long")
	exec(`UPDATE member_welcome_settings SET language = 'en-US', updated_by = 901`)

	insert := `INSERT INTO member_welcome_mails (organization_id, mailbox_id, recipient, created_at) VALUES (10, 1, ?, now())`
	exec(insert, "alice@example.com")
	assert.Error(t, db.Exec(insert, "alice@example.com").Error, "a second welcome for one mailbox")

	exec(`INSERT INTO transactional_messages (id, organization_id, mode, created_by, template_key, from_address,
		from_name, to_address, subject_masked, variables_masked, idempotency_fingerprint, status, rfc_message_id,
		created_at, updated_at)
		VALUES (77, 10, 'live', 901, 'member-welcome', 'no-reply@mail.example', 'Acme', 'alice@example.com',
		'Welcome', '{}', '', 'sent', '<x@mail.example>', now(), now())`)
	exec(`UPDATE member_welcome_mails SET transactional_message_id = 77`)
	exec(`DELETE FROM transactional_messages WHERE id = 77`)
	var linked *uint
	require.NoError(t, db.Raw(`SELECT transactional_message_id FROM member_welcome_mails`).Scan(&linked).Error)
	assert.Nil(t, linked, "purging the message keeps the record that the welcome went out")

	exec(`DELETE FROM organizations WHERE id = 10`)
	for _, table := range []string{"member_welcome_settings", "member_welcome_mails"} {
		var count int
		require.NoError(t, db.Raw(`SELECT count(*) FROM `+table).Scan(&count).Error)
		assert.Zero(t, count, table)
	}
}

// TestMemberWelcomeMailUpDown rolls the welcome tables back and applies them again.
func TestMemberWelcomeMailUpDown(t *testing.T) {
	db, _ := mailClientFixture(t)
	migrated := dumpSchema(t, db)
	// The later resend columns live on member_welcome_mails: roll them back first.
	require.NoError(t, (&addMemberWelcomeResends{}).Down(db))
	require.NoError(t, (&addMemberWelcomeMail{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "member_welcome_settings")
	assert.NotContains(t, rolledBack, "member_welcome_mails")
	require.NoError(t, (&addMemberWelcomeMail{}).Up(db))
	require.NoError(t, (&addMemberWelcomeResends{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db))
}
