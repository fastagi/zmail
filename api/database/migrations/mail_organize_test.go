package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// mailOrganizeFixture migrates everything and seeds an organization with two mailboxes.
func mailOrganizeFixture(t *testing.T) (*gorm.DB, func(string, ...any)) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'organize-owner', 'organize-owner@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES (100, 10, 901, 'owner', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (2, 10, 901, 'other@mail.example', now(), now())`)
	return db, exec
}

// rollBackMailOrganize rolls back, newest first, the organize migrations and every later
// migration that changes mail_messages, so applying them again rebuilds the same column order.
func rollBackMailOrganize(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, (&addMailboxDirectoryIndex{}).Down(db))
	require.NoError(t, (&addMailUnreadIndex{}).Down(db))
	require.NoError(t, (&addSendLanes{}).Down(db))
	require.NoError(t, (&backfillMailThreads{}).Down(db))
	require.NoError(t, (&addMailOrganize{}).Down(db))
}

func applyMailOrganize(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, (&addMailOrganize{}).Up(db))
	require.NoError(t, (&backfillMailThreads{}).Up(db))
	require.NoError(t, (&addSendLanes{}).Up(db))
	require.NoError(t, (&addMailUnreadIndex{}).Up(db))
	require.NoError(t, (&addMailboxDirectoryIndex{}).Up(db))
}

// TestMailOrganizeMigrationUpDown rolls both organize migrations back over stored folders, labels,
// stars, and placements, then applies them again: the schema is the golden one again, and the
// rollback removed every organize table and column.
func TestMailOrganizeMigrationUpDown(t *testing.T) {
	db, exec := mailOrganizeFixture(t)
	migrated := dumpSchema(t, db)

	exec(`INSERT INTO mail_folders (id, mailbox_id, name, created_at, updated_at) VALUES (4, 1, 'Invoices', now(), now())`)
	exec(`INSERT INTO mail_labels (id, mailbox_id, name, color, created_at, updated_at) VALUES (9, 1, 'Urgent', 'red', now(), now())`)
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, placement, folder_id, thread_id, created_at, updated_at)
		VALUES (11, 1, 'inbound', 'received', 'alice@sender.test', 'folder', 4, 11, now(), now())`)
	exec(`INSERT INTO mail_message_labels (message_id, label_id) VALUES (11, 9)`)
	exec(`INSERT INTO mail_message_stars (membership_id, message_id, starred_at) VALUES (100, 11, now())`)

	rollBackMailOrganize(t, db)
	rolledBack := dumpSchema(t, db)
	for _, gone := range []string{"TABLE mail_folders", "TABLE mail_labels", "TABLE mail_message_labels", "TABLE mail_message_stars", "gin_trgm_ops"} {
		assert.NotContains(t, rolledBack, gone)
	}
	// Other tables (transactional_messages) have an rfc_message_id too, so look at mail_messages only.
	mailMessages := rolledBack[strings.Index(rolledBack, "TABLE mail_messages\n"):]
	mailMessages = mailMessages[:strings.Index(mailMessages, "\n\n")]
	for _, gone := range []string{"COLUMN placement", "COLUMN folder_id", "COLUMN thread_id", "COLUMN rfc_message_id", "COLUMN references_header", "COLUMN trashed_at"} {
		assert.NotContains(t, mailMessages, gone)
	}
	var messages int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mail_messages`).Scan(&messages).Error)
	assert.Equal(t, int64(1), messages, "rolling back keeps the message itself")

	applyMailOrganize(t, db)
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestMailOrganizeMigrationKeepsPrecreatedTrigramIndexes is the large-install path in
// UPGRADING.md: an operator builds the trigram indexes CONCURRENTLY before migrating, and the
// migration then skips them and still ends at the golden schema.
func TestMailOrganizeMigrationKeepsPrecreatedTrigramIndexes(t *testing.T) {
	db, exec := mailOrganizeFixture(t)
	migrated := dumpSchema(t, db)
	rollBackMailOrganize(t, db)

	exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public`)
	for _, column := range mailOrganizeTrigramColumns {
		exec(`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_trgm_` + column +
			` ON mail_messages USING gin (` + column + ` public.gin_trgm_ops)`)
	}
	applyMailOrganize(t, db)
	assert.Equal(t, migrated, dumpSchema(t, db), "pre-created indexes must leave the golden schema")
}

// TestThreadBackfillLinksInReplyTo threads existing mail by In-Reply-To within one mailbox only:
// a reply joins its parent's thread, a reply to a reply joins the root, and a reply naming a
// message of another mailbox starts its own thread. Every row ends with a thread.
func TestThreadBackfillLinksInReplyTo(t *testing.T) {
	db, exec := mailOrganizeFixture(t)
	require.NoError(t, (&backfillMailThreads{}).Down(db))
	insert := func(id, mailboxID int, direction, messageID, inReplyTo string) {
		exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, message_id, in_reply_to, created_at, updated_at)
			VALUES (?, ?, ?, 'received', 'alice@sender.test', ?, ?, now(), now())`, id, mailboxID, direction, messageID, inReplyTo)
	}
	insert(11, 1, "inbound", "root@sender.test", "")
	insert(12, 1, "outbound", "provider-12", "root@sender.test")      // our reply to the root
	insert(13, 1, "inbound", "reply@sender.test", "root@sender.test") // their reply to the root
	insert(14, 1, "inbound", "again@sender.test", "reply@sender.test")
	insert(15, 1, "inbound", "late@sender.test", "provider-12")         // provider ids are not RFC ids
	insert(16, 1, "inbound", "", "")                                    // no ids at all
	insert(21, 2, "inbound", "foreign@sender.test", "root@sender.test") // same id, other mailbox

	require.NoError(t, (&backfillMailThreads{}).Up(db))

	type row struct {
		ID           int
		ThreadID     *int
		RFCMessageID string
	}
	var rows []row
	require.NoError(t, db.Raw(`SELECT id, thread_id, rfc_message_id FROM mail_messages ORDER BY id`).Scan(&rows).Error)
	threads := map[int]int{}
	rfc := map[int]string{}
	for _, r := range rows {
		require.NotNil(t, r.ThreadID, "message %d has no thread after the backfill", r.ID)
		threads[r.ID] = *r.ThreadID
		rfc[r.ID] = r.RFCMessageID
	}
	assert.Equal(t, map[int]int{11: 11, 12: 11, 13: 11, 14: 11, 15: 15, 16: 16, 21: 21}, threads)
	assert.Equal(t, "root@sender.test", rfc[11], "inbound rows copy their Message-ID")
	assert.Empty(t, rfc[12], "an outbound provider id is not an RFC Message-ID")
	assert.Equal(t, "foreign@sender.test", rfc[21])

	// Down clears what the backfill wrote; Up again restores the same threads.
	require.NoError(t, (&backfillMailThreads{}).Down(db))
	var threaded int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM mail_messages WHERE thread_id IS NOT NULL OR rfc_message_id <> ''`).Scan(&threaded).Error)
	assert.Zero(t, threaded)
	require.NoError(t, (&backfillMailThreads{}).Up(db))
}
