package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestMailboxDirectoryIndexMigrationUpDown covers Down and the large-install path in UPGRADING.md:
// an operator builds the send-count index CONCURRENTLY before migrating, the migration then skips
// it and still ends at the golden schema.
func TestMailboxDirectoryIndexMigrationUpDown(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	migrated := dumpSchema(t, db)
	require.Contains(t, migrated, "idx_mail_messages_mailbox_send_attempted")

	require.NoError(t, (&addMailboxDirectoryIndex{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "idx_mail_messages_mailbox_send_attempted", "Down must drop the index")

	require.NoError(t, (&addMailboxDirectoryIndex{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "Up after Down must restore the golden schema")

	require.NoError(t, (&addMailboxDirectoryIndex{}).Down(db))
	require.NoError(t, db.Exec(`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_mailbox_send_attempted
		ON mail_messages (mailbox_id, send_lane, send_attempted_at)
		WHERE direction = 'outbound'`).Error)
	require.NoError(t, (&addMailboxDirectoryIndex{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "a pre-created index must leave the golden schema")
}
