package migrations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type imapUIDRow struct {
	FolderKey string
	UID       int64
}

func imapUIDOf(t *testing.T, db *gorm.DB, messageID int) *imapUIDRow {
	t.Helper()
	var rows []imapUIDRow
	require.NoError(t, db.Raw(`SELECT folder_key, uid FROM mail_imap_uids WHERE message_id = ?`, messageID).Scan(&rows).Error)
	if len(rows) == 0 {
		return nil
	}
	require.Len(t, rows, 1)
	return &rows[0]
}

// imapFolderOf reads a folder of mailbox 1, the fixture's only mailbox.
func imapFolderOf(t *testing.T, db *gorm.DB, key string) (uidValidity, uidNext int64, found bool) {
	t.Helper()
	var rows []struct {
		Uidvalidity int64
		Uidnext     int64
	}
	require.NoError(t, db.Raw(`SELECT uidvalidity, uidnext FROM mail_imap_folders WHERE mailbox_id = 1 AND folder_key = ?`,
		key).Scan(&rows).Error)
	if len(rows) == 0 {
		return 0, 0, false
	}
	return rows[0].Uidvalidity, rows[0].Uidnext, true
}

const insertIMAPMessage = `INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, created_at, updated_at)
	VALUES (?, 1, ?, ?, 'a@example.com', now(), now())`

// TestMailImapUidTrigger follows messages through every placement change: a UID grows in the order
// a message entered its folder, a message moved back gets a new one, failed outbound mail has none,
// and deleting a message or a folder forgets its rows.
func TestMailImapUidTrigger(t *testing.T) {
	db, exec := mailClientFixture(t)
	exec(insertIMAPMessage, 1, "inbound", "received")
	exec(insertIMAPMessage, 2, "inbound", "received")
	assert.Equal(t, &imapUIDRow{"inbox", 1}, imapUIDOf(t, db, 1))
	assert.Equal(t, &imapUIDRow{"inbox", 2}, imapUIDOf(t, db, 2))
	validity, next, found := imapFolderOf(t, db, "inbox")
	require.True(t, found)
	assert.Equal(t, int64(3), next)
	assert.InDelta(t, time.Now().Unix(), validity, 120)

	exec(`UPDATE mail_messages SET placement = 'archive' WHERE id = 1`)
	assert.Equal(t, &imapUIDRow{"archive", 1}, imapUIDOf(t, db, 1))
	exec(`UPDATE mail_messages SET placement = 'default' WHERE id = 1`)
	assert.Equal(t, &imapUIDRow{"inbox", 3}, imapUIDOf(t, db, 1), "moving back takes a new uid")
	exec(`UPDATE mail_messages SET subject = 'renamed', last_event = 'delivered' WHERE id = 1`)
	assert.Equal(t, &imapUIDRow{"inbox", 3}, imapUIDOf(t, db, 1), "other columns keep the uid")
	exec(`UPDATE mail_messages SET placement = 'trash', trashed_at = now() WHERE id = 2`)
	assert.Equal(t, &imapUIDRow{"trash", 1}, imapUIDOf(t, db, 2))

	exec(insertIMAPMessage, 3, "outbound", "failed")
	assert.Nil(t, imapUIDOf(t, db, 3), "failed outbound mail is in no folder")
	exec(insertIMAPMessage, 4, "outbound", "queued")
	assert.Equal(t, &imapUIDRow{"sent", 1}, imapUIDOf(t, db, 4))
	exec(`UPDATE mail_messages SET status = 'sent' WHERE id = 4`)
	assert.Equal(t, &imapUIDRow{"sent", 1}, imapUIDOf(t, db, 4), "queued to sent stays in Sent")
	exec(`UPDATE mail_messages SET status = 'failed' WHERE id = 4`)
	assert.Nil(t, imapUIDOf(t, db, 4))
	exec(`UPDATE mail_messages SET status = 'sent' WHERE id = 3`)
	assert.Equal(t, &imapUIDRow{"sent", 2}, imapUIDOf(t, db, 3))

	exec(`INSERT INTO mail_folders (id, mailbox_id, name, created_at, updated_at) VALUES (7, 1, 'Invoices', now(), now())`)
	exec(`UPDATE mail_messages SET placement = 'folder', folder_id = 7 WHERE id = 1`)
	assert.Equal(t, &imapUIDRow{"folder:7", 1}, imapUIDOf(t, db, 1))
	exec(`UPDATE mail_messages SET placement = 'default', folder_id = NULL WHERE id = 1`)
	exec(`DELETE FROM mail_folders WHERE id = 7`)
	_, _, found = imapFolderOf(t, db, "folder:7")
	assert.False(t, found, "deleting a folder forgets its uid counter")

	exec(`DELETE FROM mail_messages WHERE id = 1`)
	assert.Nil(t, imapUIDOf(t, db, 1))
	assert.Error(t, db.Exec(`INSERT INTO mail_imap_folders (mailbox_id, folder_key, uidvalidity, uidnext, created_at)
		VALUES (1, 'drafts', 1, 1, now())`).Error, "an unknown folder key")
}

// TestMailImapUidTriggerSerializesConcurrentInserts: a second transaction filing mail into the
// same folder waits for the first to commit, so a UID never becomes visible before a smaller one.
func TestMailImapUidTriggerSerializesConcurrentInserts(t *testing.T) {
	db, exec := mailClientFixture(t)
	exec(insertIMAPMessage, 1, "inbound", "received")

	first := db.Begin()
	require.NoError(t, first.Error)
	require.NoError(t, first.Exec(insertIMAPMessage, 2, "inbound", "received").Error)

	done := make(chan error, 1)
	go func() {
		done <- db.Exec(insertIMAPMessage, 3, "inbound", "received").Error
	}()
	select {
	case err := <-done:
		t.Fatalf("the second insert must wait for the first transaction, got %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, first.Commit().Error)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the second insert never finished")
	}
	firstUID, secondUID := imapUIDOf(t, db, 2), imapUIDOf(t, db, 3)
	require.NotNil(t, firstUID)
	require.NotNil(t, secondUID)
	assert.Equal(t, firstUID.UID+1, secondUID.UID)
}

// TestMailImapUidsBackfillAndUpDown numbers existing mail by id when the migration runs, and a
// rollback and reapply numbers it again under a new UIDVALIDITY with the golden schema.
func TestMailImapUidsBackfillAndUpDown(t *testing.T) {
	db, exec := mailClientFixture(t)
	migrated := dumpSchema(t, db)
	rollBackMailImapUIDs(t, db)
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "mail_imap_")

	exec(`INSERT INTO mail_folders (id, mailbox_id, name, created_at, updated_at) VALUES (7, 1, 'Invoices', now(), now())`)
	exec(insertIMAPMessage, 5, "inbound", "received")
	exec(insertIMAPMessage, 3, "inbound", "received")
	exec(insertIMAPMessage, 4, "outbound", "sent")
	exec(insertIMAPMessage, 6, "outbound", "failed")
	exec(insertIMAPMessage, 8, "inbound", "received")
	exec(`UPDATE mail_messages SET placement = 'folder', folder_id = 7 WHERE id = 8`)

	applyMailImapUIDs(t, db)
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
	assert.Equal(t, &imapUIDRow{"inbox", 1}, imapUIDOf(t, db, 3))
	assert.Equal(t, &imapUIDRow{"inbox", 2}, imapUIDOf(t, db, 5))
	assert.Equal(t, &imapUIDRow{"sent", 1}, imapUIDOf(t, db, 4))
	assert.Nil(t, imapUIDOf(t, db, 6))
	assert.Equal(t, &imapUIDRow{"folder:7", 1}, imapUIDOf(t, db, 8))
	firstValidity, next, found := imapFolderOf(t, db, "inbox")
	require.True(t, found)
	assert.Equal(t, int64(3), next)
	exec(insertIMAPMessage, 9, "inbound", "received")
	assert.Equal(t, &imapUIDRow{"inbox", 3}, imapUIDOf(t, db, 9), "new mail continues after the backfill")

	time.Sleep(1100 * time.Millisecond)
	rollBackMailImapUIDs(t, db)
	applyMailImapUIDs(t, db)
	secondValidity, _, found := imapFolderOf(t, db, "inbox")
	require.True(t, found)
	assert.NotEqual(t, firstValidity, secondValidity, "renumbering must change UIDVALIDITY")
}

// rollBackMailImapUIDs rolls back the UID migration and every later one, newest first.
func rollBackMailImapUIDs(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, (&addMailAppPasswords{}).Down(db))
	require.NoError(t, (&addMailClientFeatureGrants{}).Down(db))
	require.NoError(t, (&addMailImapUIDs{}).Down(db))
}

func applyMailImapUIDs(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, (&addMailImapUIDs{}).Up(db))
	require.NoError(t, (&addMailClientFeatureGrants{}).Up(db))
	require.NoError(t, (&addMailAppPasswords{}).Up(db))
}
