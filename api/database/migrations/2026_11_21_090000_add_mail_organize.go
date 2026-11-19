package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_21_090000_add_mail_organize", &addMailOrganize{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailOrganize struct {
	migration.BaseMigration
}

// mailOrganizeTrigramColumns are the searchable columns; each gets a trigram GIN index so ILIKE
// search stays fast on a large mailbox.
var mailOrganizeTrigramColumns = []string{"subject", "from_address", "from_name", "to_addresses", "cc_addresses", "text_body"}

// Up adds what organizing a mailbox needs: a placement for every message (default, archive,
// folder, or trash), mailbox folders and labels, per-person stars, the columns threading reads,
// and the listing, lookup, purge, and search indexes. Every existing message starts in default.
// thread_id stays NULL here; the next migration fills it.
//
// pg_trgm is created in the public schema when absent, so it is shared by every schema of the
// database, and each trigram index names the operator class in whatever schema holds it.
// The trigram indexes cover columns that already exist, so a large installation can build them
// beforehand with CREATE INDEX CONCURRENTLY (see UPGRADING.md); IF NOT EXISTS then skips them
// here instead of holding the write lock on mail_messages while they build.
func (m *addMailOrganize) Up(db *gorm.DB) error {
	statements := []string{
		`CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public`,
		`CREATE TABLE mail_folders (
		  id bigserial PRIMARY KEY,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  name varchar(64) NOT NULL,
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL)`,
		`CREATE UNIQUE INDEX idx_mail_folders_name ON mail_folders (mailbox_id, lower(name))`,
		`CREATE TABLE mail_labels (
		  id bigserial PRIMARY KEY,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  name varchar(64) NOT NULL,
		  color varchar(16) NOT NULL DEFAULT 'gray'
		    CHECK (color IN ('gray','red','orange','yellow','green','teal','blue','purple','pink')),
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL)`,
		`CREATE UNIQUE INDEX idx_mail_labels_name ON mail_labels (mailbox_id, lower(name))`,
		`ALTER TABLE mail_messages
		  ADD COLUMN placement varchar(16) NOT NULL DEFAULT 'default',
		  ADD COLUMN folder_id bigint REFERENCES mail_folders(id) ON DELETE SET NULL,
		  ADD COLUMN trashed_at timestamptz,
		  ADD COLUMN thread_id bigint,
		  ADD COLUMN references_header text NOT NULL DEFAULT '',
		  ADD COLUMN rfc_message_id varchar(998) NOT NULL DEFAULT '',
		  ADD CONSTRAINT chk_mail_messages_placement CHECK (placement IN ('default','archive','folder','trash')),
		  ADD CONSTRAINT chk_mail_messages_folder CHECK (placement <> 'folder' OR folder_id IS NOT NULL)`,
		`CREATE TABLE mail_message_labels (
		  message_id bigint NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
		  label_id bigint NOT NULL REFERENCES mail_labels(id) ON DELETE CASCADE,
		  PRIMARY KEY (message_id, label_id))`,
		`CREATE INDEX idx_mail_message_labels_label ON mail_message_labels (label_id, message_id)`,
		`CREATE TABLE mail_message_stars (
		  membership_id bigint NOT NULL REFERENCES organization_memberships(id) ON DELETE CASCADE,
		  message_id bigint NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
		  starred_at timestamptz NOT NULL,
		  PRIMARY KEY (membership_id, message_id))`,
		`CREATE INDEX idx_mail_message_stars_message ON mail_message_stars (message_id)`,
		`CREATE INDEX idx_mail_messages_threads ON mail_messages (mailbox_id, placement, thread_id, id DESC)`,
		`CREATE INDEX idx_mail_messages_rfc_message_id ON mail_messages (mailbox_id, rfc_message_id)`,
		`CREATE INDEX idx_mail_messages_trash ON mail_messages (placement, trashed_at)`,
		`CREATE INDEX idx_mail_messages_folder ON mail_messages (folder_id) WHERE folder_id IS NOT NULL`,
	}
	for _, column := range mailOrganizeTrigramColumns {
		statements = append(statements,
			`DO $$ BEGIN EXECUTE format('CREATE INDEX IF NOT EXISTS idx_mail_messages_trgm_`+column+` ON mail_messages USING gin (`+column+` %I.gin_trgm_ops)',
			   (SELECT extnamespace::regnamespace::text FROM pg_extension WHERE extname = 'pg_trgm')); END $$`)
	}
	return execStatements(db, statements...)
}

// Down removes placements, folders, labels, stars, and the threading columns. pg_trgm stays: an
// extension is shared by the whole database and other schemas may use it.
func (m *addMailOrganize) Down(db *gorm.DB) error {
	statements := make([]string, 0, len(mailOrganizeTrigramColumns)+8)
	for _, column := range mailOrganizeTrigramColumns {
		statements = append(statements, `DROP INDEX IF EXISTS idx_mail_messages_trgm_`+column)
	}
	statements = append(statements,
		`DROP INDEX IF EXISTS idx_mail_messages_folder`,
		`DROP INDEX IF EXISTS idx_mail_messages_trash`,
		`DROP INDEX IF EXISTS idx_mail_messages_rfc_message_id`,
		`DROP INDEX IF EXISTS idx_mail_messages_threads`,
		`DROP TABLE IF EXISTS mail_message_stars`,
		`DROP TABLE IF EXISTS mail_message_labels`,
		`ALTER TABLE mail_messages
		  DROP CONSTRAINT IF EXISTS chk_mail_messages_folder,
		  DROP CONSTRAINT IF EXISTS chk_mail_messages_placement,
		  DROP COLUMN IF EXISTS rfc_message_id,
		  DROP COLUMN IF EXISTS references_header,
		  DROP COLUMN IF EXISTS thread_id,
		  DROP COLUMN IF EXISTS trashed_at,
		  DROP COLUMN IF EXISTS folder_id,
		  DROP COLUMN IF EXISTS placement`,
		`DROP TABLE IF EXISTS mail_labels`,
		`DROP TABLE IF EXISTS mail_folders`,
	)
	return execStatements(db, statements...)
}
