package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_21_090100_backfill_mail_threads", &backfillMailThreads{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type backfillMailThreads struct {
	migration.BaseMigration
}

// Up threads the mail stored so far. Inbound rows hold the sender's RFC Message-ID in message_id,
// so it is copied into rfc_message_id; outbound rows hold a provider id there and get none.
//
// Then every row without a thread gets one, in id order: a row whose In-Reply-To names the
// rfc_message_id of an earlier row of the same mailbox joins that row's thread, any other row
// starts its own thread with its own id. Threads never cross mailboxes and never merge by subject.
// Afterwards no row has a NULL thread_id.
func (m *backfillMailThreads) Up(db *gorm.DB) error {
	return execStatements(db,
		`UPDATE mail_messages SET rfc_message_id = message_id
		 WHERE direction = 'inbound' AND rfc_message_id = '' AND message_id <> ''`,
		`WITH RECURSIVE parents AS (
		   SELECT child.id,
		     (SELECT parent.id FROM mail_messages parent
		      WHERE parent.mailbox_id = child.mailbox_id AND parent.rfc_message_id = child.in_reply_to
		        AND parent.rfc_message_id <> '' AND parent.id < child.id
		      ORDER BY parent.id LIMIT 1) AS parent_id
		   FROM mail_messages child
		   WHERE child.thread_id IS NULL
		 ), threads AS (
		   SELECT parents.id, COALESCE(parent.thread_id, parents.id) AS thread_id
		   FROM parents LEFT JOIN mail_messages parent ON parent.id = parents.parent_id
		   WHERE parents.parent_id IS NULL OR parent.thread_id IS NOT NULL
		   UNION ALL
		   SELECT parents.id, threads.thread_id
		   FROM parents JOIN threads ON parents.parent_id = threads.id
		 )
		 UPDATE mail_messages SET thread_id = threads.thread_id
		 FROM threads WHERE threads.id = mail_messages.id`,
	)
}

// Down clears what Up wrote: every thread_id, and the rfc_message_id copied from message_id.
func (m *backfillMailThreads) Down(db *gorm.DB) error {
	return execStatements(db,
		`UPDATE mail_messages SET thread_id = NULL WHERE thread_id IS NOT NULL`,
		`UPDATE mail_messages SET rfc_message_id = ''
		 WHERE direction = 'inbound' AND rfc_message_id <> '' AND rfc_message_id = message_id`,
	)
}
