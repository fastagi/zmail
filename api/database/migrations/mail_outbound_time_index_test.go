package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestMailOutboundTimeIndexMigrationKeepsPrecreatedIndex is the large-install path in
// UPGRADING.md: an operator builds the outbound time index CONCURRENTLY before migrating, the
// migration then skips it and still ends at the golden schema, and Down drops it.
func TestMailOutboundTimeIndexMigrationKeepsPrecreatedIndex(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	migrated := dumpSchema(t, db)
	require.Contains(t, migrated, "idx_mail_messages_outbound_time")

	require.NoError(t, (&addMailOutboundTimeIndex{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "idx_mail_messages_outbound_time", "Down must drop the index")

	require.NoError(t, db.Exec(`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_outbound_time
		ON mail_messages (created_at, id) INCLUDE (mailbox_id, status, source)
		WHERE direction = 'outbound'`).Error)
	require.NoError(t, (&addMailOutboundTimeIndex{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "a pre-created index must leave the golden schema")
}
