package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestMailInboxIndexMigrationKeepsPrecreatedIndex is the large-install path in UPGRADING.md: an
// operator builds the inbox index CONCURRENTLY before migrating, the migration then skips it and
// still ends at the golden schema, and Down drops it.
func TestMailInboxIndexMigrationKeepsPrecreatedIndex(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	migrated := dumpSchema(t, db)
	require.Contains(t, migrated, "idx_mail_messages_inbox_time")

	require.NoError(t, (&addMailInboxIndex{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "idx_mail_messages_inbox_time", "Down must drop the index")

	require.NoError(t, db.Exec(`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_inbox_time
		ON mail_messages (mailbox_id, direction, created_at DESC, id DESC)`).Error)
	require.NoError(t, (&addMailInboxIndex{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "a pre-created index must leave the golden schema")

	require.NoError(t, (&addMailInboxIndex{}).Down(db))
	require.NoError(t, (&addMailInboxIndex{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}
