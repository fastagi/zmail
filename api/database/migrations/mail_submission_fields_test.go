package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMailSubmissionFieldsUpDown: bcc, the sending membership, and inline content ids default to
// nothing, a content id is a bare token, and the columns roll back to the golden schema.
func TestMailSubmissionFieldsUpDown(t *testing.T) {
	db, exec := mailClientFixture(t)
	migrated := dumpSchema(t, db)
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, created_at, updated_at)
		VALUES (1, 1, 'outbound', 'sent', 'team@mail.example', now(), now())`)
	var message struct {
		BccAddresses       string
		SentByMembershipID *uint
	}
	require.NoError(t, db.Raw(`SELECT bcc_addresses, sent_by_membership_id FROM mail_messages WHERE id = 1`).Scan(&message).Error)
	assert.Equal(t, "[]", message.BccAddresses)
	assert.Nil(t, message.SentByMembershipID)

	insert := `INSERT INTO mail_attachments (message_id, filename, content_type, size, content, content_id, inline, created_at)
		VALUES (1, 'logo.png', 'image/png', 1, '\x00', ?, ?, now())`
	for _, invalid := range []string{"<logo@x>", "logo @x", "logo\t@x"} {
		assert.Error(t, db.Exec(insert, invalid, true).Error, invalid)
	}
	exec(insert, "logo@x", true)
	exec(`INSERT INTO mail_attachments (message_id, filename, content_type, size, content, created_at)
		VALUES (1, 'a.pdf', 'application/pdf', 1, '\x00', now())`)
	var attachments []struct {
		ContentID string
		Inline    bool
	}
	require.NoError(t, db.Raw(`SELECT content_id, inline FROM mail_attachments ORDER BY id`).Scan(&attachments).Error)
	require.Len(t, attachments, 2)
	assert.Equal(t, "logo@x", attachments[0].ContentID)
	assert.True(t, attachments[0].Inline)
	assert.Equal(t, "", attachments[1].ContentID, "an attachment without a content id defaults to none")
	assert.False(t, attachments[1].Inline)

	require.NoError(t, (&addMailSubmissionFields{}).Down(db))
	schema := dumpSchema(t, db)
	for _, column := range []string{"bcc_addresses", "sent_by_membership_id", "content_id", "inline"} {
		assert.NotContains(t, schema, column)
	}
	require.NoError(t, (&addMailSubmissionFields{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}
