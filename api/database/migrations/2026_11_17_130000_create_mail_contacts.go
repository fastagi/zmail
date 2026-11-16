package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_17_130000_create_mail_contacts", &createMailContacts{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createMailContacts struct {
	migration.BaseMigration
}

// Up adds the contacts of an organization and the lists that group them. A contact is one
// address per organization; deleting a list keeps its contacts, and deleting a contact or a list
// removes the memberships between them.
func (m *createMailContacts) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_contacts (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    email varchar(320) NOT NULL,
		    first_name varchar(100) NOT NULL DEFAULT '',
		    last_name varchar(100) NOT NULL DEFAULT '',
		    properties jsonb NOT NULL DEFAULT '{}',
		    status varchar(16) NOT NULL DEFAULT 'subscribed',
		    status_reason varchar(64) NOT NULL DEFAULT '',
		    created_at timestamptz,
		    updated_at timestamptz,
		    CONSTRAINT mail_contacts_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_mail_contacts_status CHECK (status IN ('subscribed', 'unsubscribed', 'suppressed'))
		)`,
		`CREATE UNIQUE INDEX idx_mail_contacts_email ON mail_contacts (organization_id, email)`,
		`CREATE INDEX idx_mail_contacts_listing ON mail_contacts (organization_id, id)`,
		`ALTER TABLE mail_contacts ADD CONSTRAINT fk_mail_contacts_organization FOREIGN KEY (organization_id) REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE CASCADE`,
		`CREATE TABLE mail_contact_lists (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    name varchar(100) NOT NULL,
		    created_at timestamptz,
		    updated_at timestamptz,
		    CONSTRAINT mail_contact_lists_pkey PRIMARY KEY (id)
		)`,
		`CREATE UNIQUE INDEX idx_mail_contact_lists_name ON mail_contact_lists (organization_id, name)`,
		`ALTER TABLE mail_contact_lists ADD CONSTRAINT fk_mail_contact_lists_organization FOREIGN KEY (organization_id) REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE CASCADE`,
		`CREATE TABLE mail_contact_list_members (
		    list_id bigint NOT NULL,
		    contact_id bigint NOT NULL,
		    created_at timestamptz,
		    CONSTRAINT mail_contact_list_members_pkey PRIMARY KEY (list_id, contact_id)
		)`,
		`CREATE INDEX idx_mail_contact_list_members_contact ON mail_contact_list_members (contact_id)`,
		`ALTER TABLE mail_contact_list_members ADD CONSTRAINT fk_mail_contact_list_members_list FOREIGN KEY (list_id) REFERENCES mail_contact_lists(id) ON UPDATE CASCADE ON DELETE CASCADE`,
		`ALTER TABLE mail_contact_list_members ADD CONSTRAINT fk_mail_contact_list_members_contact FOREIGN KEY (contact_id) REFERENCES mail_contacts(id) ON UPDATE CASCADE ON DELETE CASCADE`,
	)
}

// Down removes contacts and lists.
func (m *createMailContacts) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS mail_contact_list_members CASCADE`,
		`DROP TABLE IF EXISTS mail_contact_lists CASCADE`,
		`DROP TABLE IF EXISTS mail_contacts CASCADE`,
	)
}
