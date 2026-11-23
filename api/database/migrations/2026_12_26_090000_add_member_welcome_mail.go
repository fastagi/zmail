package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_26_090000_add_member_welcome_mail", &addMemberWelcomeMail{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMemberWelcomeMail struct {
	migration.BaseMigration
}

// Up adds the organization's welcome mail template and the record of each welcome mail sent. A
// missing settings row means the welcome mail is on with the standard Chinese text; empty copy
// means the standard text of the language. A record exists once per mailbox, so one person is
// welcomed at most once however often a roster is uploaded; it outlives the 30-day transactional
// message it points at.
func (m *addMemberWelcomeMail) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE member_welcome_settings (
		    organization_id bigint NOT NULL,
		    enabled boolean DEFAULT true NOT NULL,
		    language varchar(8) DEFAULT 'zh-Hans' NOT NULL,
		    subject varchar(200) DEFAULT '' NOT NULL,
		    body text DEFAULT '' NOT NULL,
		    updated_by bigint,
		    created_at timestamptz NOT NULL,
		    updated_at timestamptz NOT NULL,
		    CONSTRAINT member_welcome_settings_pkey PRIMARY KEY (organization_id),
		    CONSTRAINT chk_member_welcome_settings_language CHECK (language IN ('en-US', 'zh-Hans')),
		    CONSTRAINT chk_member_welcome_settings_body CHECK (char_length(body) <= 5000)
		)`,
		`ALTER TABLE member_welcome_settings ADD CONSTRAINT fk_member_welcome_settings_organization
		    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE`,
		`ALTER TABLE member_welcome_settings ADD CONSTRAINT fk_member_welcome_settings_updated_by
		    FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL`,
		`CREATE TABLE member_welcome_mails (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    mailbox_id bigint NOT NULL,
		    recipient varchar(320) NOT NULL,
		    transactional_message_id bigint,
		    created_at timestamptz NOT NULL,
		    CONSTRAINT member_welcome_mails_pkey PRIMARY KEY (id)
		)`,
		`CREATE UNIQUE INDEX idx_member_welcome_mails_mailbox ON member_welcome_mails (mailbox_id)`,
		`CREATE INDEX idx_member_welcome_mails_organization ON member_welcome_mails (organization_id)`,
		`CREATE INDEX idx_member_welcome_mails_message ON member_welcome_mails (transactional_message_id)
		    WHERE transactional_message_id IS NOT NULL`,
		`ALTER TABLE member_welcome_mails ADD CONSTRAINT fk_member_welcome_mails_organization
		    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE`,
		`ALTER TABLE member_welcome_mails ADD CONSTRAINT fk_member_welcome_mails_mailbox
		    FOREIGN KEY (mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE`,
		`ALTER TABLE member_welcome_mails ADD CONSTRAINT fk_member_welcome_mails_message
		    FOREIGN KEY (transactional_message_id) REFERENCES transactional_messages(id) ON DELETE SET NULL`,
	)
}

// Down drops the welcome template and the record of sent welcomes; a later roster upload would
// welcome everyone again.
func (m *addMemberWelcomeMail) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS member_welcome_mails`,
		`DROP TABLE IF EXISTS member_welcome_settings`,
	)
}
