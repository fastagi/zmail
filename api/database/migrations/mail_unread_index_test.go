package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestMailUnreadIndexMigrationKeepsPrecreatedIndex is the large-install path in UPGRADING.md: an
// operator builds the unread index CONCURRENTLY before migrating, the migration then skips it and
// still ends at the golden schema, and Down drops it.
func TestMailUnreadIndexMigrationKeepsPrecreatedIndex(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	migrated := dumpSchema(t, db)
	require.Contains(t, migrated, "idx_mail_messages_unread")

	require.NoError(t, (&addMailUnreadIndex{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "idx_mail_messages_unread", "Down must drop the index")

	require.NoError(t, db.Exec(`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_unread
		ON mail_messages (mailbox_id, id) INCLUDE (created_at)
		WHERE direction = 'inbound' AND placement = 'default'`).Error)
	require.NoError(t, (&addMailUnreadIndex{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "a pre-created index must leave the golden schema")
}
