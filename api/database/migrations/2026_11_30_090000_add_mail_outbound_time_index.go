package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_30_090000_add_mail_outbound_time_index", &addMailOutboundTimeIndex{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailOutboundTimeIndex struct {
	migration.BaseMigration
}

// Up adds the index platform mail metrics read: outbound mail by creation time, with the mailbox,
// status, and source each count needs, so a day or a trend window is an index-only range instead
// of a scan of all of mail_messages. Without it the overview, the per-organization counts, and
// every outbound log page scan the whole table, inbound mail included, and slow as stored mail
// grows (docs/benchmarks/v2-hot-paths.md).
//
// The index covers columns that already exist, so a large installation can build it beforehand
// with CREATE INDEX CONCURRENTLY (see UPGRADING.md); IF NOT EXISTS then skips it here instead of
// holding the write lock on mail_messages while it builds.
func (m *addMailOutboundTimeIndex) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE INDEX IF NOT EXISTS idx_mail_messages_outbound_time
		  ON mail_messages (created_at, id) INCLUDE (mailbox_id, status, source)
		  WHERE direction = 'outbound'`,
	)
}

// Down drops the outbound time index.
func (m *addMailOutboundTimeIndex) Down(db *gorm.DB) error {
	return execStatements(db, `DROP INDEX IF EXISTS idx_mail_messages_outbound_time`)
}
