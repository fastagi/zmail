package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemberWelcomeResendColumns: an existing welcome record reads as never resent, and the
// counters cannot go negative.
func TestMemberWelcomeResendColumns(t *testing.T) {
	db, exec := mailClientFixture(t)
	exec(`INSERT INTO member_welcome_mails (organization_id, mailbox_id, recipient, created_at) VALUES (10, 1, 'a@example.com', now())`)
	var record struct {
		LastSentAt            *string
		ResendCount           int
		ResendWindowStartedAt *string
		ResendWindowCount     int
	}
	require.NoError(t, db.Raw(`SELECT last_sent_at, resend_count, resend_window_started_at, resend_window_count
		FROM member_welcome_mails`).Scan(&record).Error)
	assert.Nil(t, record.LastSentAt)
	assert.Nil(t, record.ResendWindowStartedAt)
	assert.Zero(t, record.ResendCount)
	assert.Zero(t, record.ResendWindowCount)
	assert.Error(t, db.Exec(`UPDATE member_welcome_mails SET resend_count = -1`).Error, "a negative resend count")
	exec(`UPDATE member_welcome_mails SET resend_count = 2, resend_window_count = 2, last_sent_at = now(),
		resend_window_started_at = now()`)
}

// TestMemberWelcomeResendUpDown rolls the resend columns back and applies them again.
func TestMemberWelcomeResendUpDown(t *testing.T) {
	db, _ := mailClientFixture(t)
	migrated := dumpSchema(t, db)
	require.NoError(t, (&addMemberWelcomeResends{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "resend_window_count")
	assert.Contains(t, rolledBack, "member_welcome_mails")
	require.NoError(t, (&addMemberWelcomeResends{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db))
}
