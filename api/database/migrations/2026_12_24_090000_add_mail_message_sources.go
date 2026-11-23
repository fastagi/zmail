package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_24_090000_add_mail_message_sources", &addMailMessageSources{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailMessageSources struct {
	migration.BaseMigration
}

// Up adds the raw RFC 5322 source of a message, kept byte for byte for mail apps. Inbound mail
// stores what the mail edge delivered (at most 5 MiB); a message without a row is rebuilt from its
// stored fields when a mail app asks for it.
func (m *addMailMessageSources) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_message_sources (
		    message_id bigint NOT NULL,
		    raw bytea NOT NULL,
		    size integer NOT NULL,
		    sha256 varchar(64) NOT NULL,
		    origin varchar(16) NOT NULL,
		    created_at timestamptz NOT NULL,
		    CONSTRAINT mail_message_sources_pkey PRIMARY KEY (message_id),
		    CONSTRAINT chk_mail_message_sources_size CHECK (size = octet_length(raw) AND size BETWEEN 1 AND 5242880),
		    CONSTRAINT chk_mail_message_sources_sha256 CHECK (sha256 ~ '^[0-9a-f]{64}$'),
		    CONSTRAINT chk_mail_message_sources_origin CHECK (origin IN ('inbound', 'smtp', 'composed'))
		)`,
		`ALTER TABLE mail_message_sources ADD CONSTRAINT fk_mail_message_sources_message
		    FOREIGN KEY (message_id) REFERENCES mail_messages(id) ON DELETE CASCADE`,
	)
}

// Down drops the stored sources; mail apps then receive rebuilt messages.
func (m *addMailMessageSources) Down(db *gorm.DB) error {
	return execStatements(db, `DROP TABLE IF EXISTS mail_message_sources`)
}
