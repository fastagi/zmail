package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/fastmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_16_100000_create_mailbox_tables", &createMailboxTables{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailboxTables struct {
	migration.BaseMigration
}

// Up creates the mailbox tables as frozen SQL; later model changes need a new migration.
func (m *createMailboxTables) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mailboxes (
		    id bigserial NOT NULL,
		    user_id bigint NOT NULL,
		    address varchar(320) NOT NULL,
		    display_name varchar(120) NOT NULL DEFAULT '',
		    created_at timestamptz,
		    updated_at timestamptz,
		    deleted_at timestamptz,
		    CONSTRAINT mailboxes_pkey PRIMARY KEY (id)
		)`,
		// A released address stays reserved so a later owner never receives mail meant for the
		// previous one; the unique index therefore covers soft-deleted rows as well.
		`CREATE UNIQUE INDEX idx_mailboxes_address ON mailboxes (address)`,
		`CREATE INDEX idx_mailboxes_user ON mailboxes (user_id) WHERE deleted_at IS NULL`,
		`ALTER TABLE mailboxes ADD CONSTRAINT fk_mailboxes_user FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE RESTRICT`,
		`CREATE TABLE mail_messages (
		    id bigserial NOT NULL,
		    mailbox_id bigint NOT NULL,
		    direction varchar(16) NOT NULL,
		    status varchar(16) NOT NULL,
		    from_address varchar(320) NOT NULL,
		    from_name varchar(255) NOT NULL DEFAULT '',
		    to_addresses text NOT NULL DEFAULT '[]',
		    cc_addresses text NOT NULL DEFAULT '[]',
		    subject varchar(998) NOT NULL DEFAULT '',
		    text_body text NOT NULL DEFAULT '',
		    html_body text NOT NULL DEFAULT '',
		    message_id varchar(998) NOT NULL DEFAULT '',
		    in_reply_to varchar(998) NOT NULL DEFAULT '',
		    dedupe_key varchar(64),
		    failure_code varchar(120) NOT NULL DEFAULT '',
		    read_at timestamptz,
		    created_at timestamptz,
		    updated_at timestamptz,
		    CONSTRAINT mail_messages_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_mail_messages_direction CHECK (direction IN ('inbound', 'outbound')),
		    CONSTRAINT chk_mail_messages_status CHECK (status IN ('received', 'queued', 'sent', 'failed'))
		)`,
		`CREATE INDEX idx_mail_messages_listing ON mail_messages (mailbox_id, direction, id DESC)`,
		`CREATE UNIQUE INDEX idx_mail_messages_dedupe ON mail_messages (mailbox_id, dedupe_key) WHERE dedupe_key IS NOT NULL`,
		`ALTER TABLE mail_messages ADD CONSTRAINT fk_mail_messages_mailbox FOREIGN KEY (mailbox_id) REFERENCES mailboxes(id) ON UPDATE CASCADE ON DELETE CASCADE`,
	)
}

// Down drops the mailbox tables.
func (m *createMailboxTables) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS mail_messages CASCADE`,
		`DROP TABLE IF EXISTS mailboxes CASCADE`,
	)
}
