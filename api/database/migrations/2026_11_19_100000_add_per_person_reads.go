package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_19_100000_add_per_person_reads", &addPerPersonReads{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addPerPersonReads struct {
	migration.BaseMigration
}

// Up makes read state per person. A read row belongs to one organization membership, so leaving
// the organization removes it and a rejoin starts with everything unread.
//
// The shared read_at column described the person who worked the mailbox until now, its creator:
// every read inbound message of a mailbox the creator still manages becomes a read row of the
// creator's membership. The column is then dropped.
func (m *addPerPersonReads) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_message_reads (
		  message_id bigint NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
		  membership_id bigint NOT NULL REFERENCES organization_memberships(id) ON DELETE CASCADE,
		  read_at timestamptz NOT NULL,
		  PRIMARY KEY (membership_id, message_id))`,
		`INSERT INTO mail_message_reads (message_id, membership_id, read_at)
		 SELECT message.id, member.membership_id, message.read_at
		 FROM mail_messages message
		 JOIN mailboxes ON mailboxes.id = message.mailbox_id
		 JOIN mailbox_members member
		   ON member.mailbox_id = mailboxes.id AND member.user_id = mailboxes.created_by AND member.permission = 'manage'
		 WHERE message.direction = 'inbound' AND message.read_at IS NOT NULL`,
		`ALTER TABLE mail_messages DROP COLUMN read_at`,
	)
}

// Down restores the shared read_at column from the creator's read rows, marks outbound mail read
// as it was when sent, and drops the read rows.
func (m *addPerPersonReads) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS read_at timestamptz`,
		`UPDATE mail_messages message SET read_at = reads.read_at
		 FROM mail_message_reads reads, mailboxes, mailbox_members member
		 WHERE reads.message_id = message.id AND mailboxes.id = message.mailbox_id
		   AND member.mailbox_id = mailboxes.id AND member.user_id = mailboxes.created_by
		   AND member.membership_id = reads.membership_id`,
		`UPDATE mail_messages SET read_at = created_at WHERE direction = 'outbound' AND read_at IS NULL`,
		`DROP TABLE IF EXISTS mail_message_reads`,
	)
}
