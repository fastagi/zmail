package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_24_100000_add_mail_imap_uids", &addMailImapUIDs{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailImapUIDs struct {
	migration.BaseMigration
}

// mailImapFolderKeyFunction maps a message's placement to its IMAP folder; NULL means no folder.
const mailImapFolderKeyFunction = `CREATE FUNCTION mail_imap_folder_key(direction text, status text, placement text, folder_id bigint)
RETURNS text LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE
        WHEN placement = 'trash' THEN 'trash'
        WHEN placement = 'archive' THEN 'archive'
        WHEN placement = 'folder' AND folder_id IS NOT NULL THEN 'folder:' || folder_id
        WHEN placement = 'default' AND direction = 'inbound' THEN 'inbox'
        WHEN placement = 'default' AND direction = 'outbound' AND status <> 'failed' THEN 'sent'
        ELSE NULL
    END
$$`

// mailImapAssignUIDFunction gives a message a new UID whenever it enters a folder. The upsert of
// the folder row holds its lock until the transaction commits, so two transactions filing mail
// into one folder take numbers one after the other and a UID never commits before a smaller one.
const mailImapAssignUIDFunction = `CREATE FUNCTION mail_imap_assign_uid() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    old_key text;
    new_key text;
    next_uid bigint;
BEGIN
    new_key := mail_imap_folder_key(NEW.direction, NEW.status, NEW.placement, NEW.folder_id);
    IF TG_OP = 'UPDATE' THEN
        old_key := mail_imap_folder_key(OLD.direction, OLD.status, OLD.placement, OLD.folder_id);
        IF old_key IS NOT DISTINCT FROM new_key AND OLD.mailbox_id = NEW.mailbox_id THEN
            RETURN NULL;
        END IF;
        DELETE FROM mail_imap_uids WHERE message_id = NEW.id;
    END IF;
    IF new_key IS NULL THEN
        RETURN NULL;
    END IF;
    INSERT INTO mail_imap_folders (mailbox_id, folder_key, uidvalidity, uidnext, created_at)
    VALUES (NEW.mailbox_id, new_key, extract(epoch FROM clock_timestamp())::bigint, 2, clock_timestamp())
    ON CONFLICT (mailbox_id, folder_key) DO UPDATE SET uidnext = mail_imap_folders.uidnext + 1
    RETURNING uidnext - 1 INTO next_uid;
    INSERT INTO mail_imap_uids (mailbox_id, folder_key, uid, message_id) VALUES (NEW.mailbox_id, new_key, next_uid, NEW.id);
    RETURN NULL;
END
$$`

// mailImapForgetFolderFunction drops the UID counter of a deleted folder; a folder made again
// under the same name has another id and so starts under a new UIDVALIDITY.
const mailImapForgetFolderFunction = `CREATE FUNCTION mail_imap_forget_folder() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM mail_imap_folders WHERE mailbox_id = OLD.mailbox_id AND folder_key = 'folder:' || OLD.id;
    RETURN NULL;
END
$$`

// Up numbers every message of every IMAP folder. UIDs grow in the order mail entered a folder and
// are kept by triggers on mail_messages, so every write path (inbound, sending, organizing, the
// trash purge, folder deletion, handover) is covered. Existing mail is numbered by id once.
func (m *addMailImapUIDs) Up(db *gorm.DB) error {
	// LOCK TABLE needs a transaction; inside the runner's own transaction this is a savepoint.
	return db.Transaction(func(tx *gorm.DB) error { return m.up(tx) })
}

func (m *addMailImapUIDs) up(db *gorm.DB) error {
	return execStatements(db,
		// Writers wait until the triggers exist, so no message is filed between the backfill's
		// read and the trigger's creation without a UID, and no move leaves a stale one.
		`LOCK TABLE mail_messages IN SHARE ROW EXCLUSIVE MODE`,
		`CREATE TABLE mail_imap_folders (
		    mailbox_id bigint NOT NULL,
		    folder_key varchar(32) NOT NULL,
		    uidvalidity bigint NOT NULL,
		    uidnext bigint DEFAULT 1 NOT NULL,
		    created_at timestamptz NOT NULL,
		    CONSTRAINT mail_imap_folders_pkey PRIMARY KEY (mailbox_id, folder_key),
		    CONSTRAINT chk_mail_imap_folders_key CHECK (folder_key ~ '^(inbox|sent|archive|trash|folder:[1-9][0-9]*)$'),
		    CONSTRAINT chk_mail_imap_folders_uidvalidity CHECK (uidvalidity BETWEEN 1 AND 4294967295),
		    CONSTRAINT chk_mail_imap_folders_uidnext CHECK (uidnext BETWEEN 1 AND 4294967295)
		)`,
		`ALTER TABLE mail_imap_folders ADD CONSTRAINT fk_mail_imap_folders_mailbox
		    FOREIGN KEY (mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE`,
		`CREATE TABLE mail_imap_uids (
		    mailbox_id bigint NOT NULL,
		    folder_key varchar(32) NOT NULL,
		    uid bigint NOT NULL,
		    message_id bigint NOT NULL,
		    CONSTRAINT mail_imap_uids_pkey PRIMARY KEY (mailbox_id, folder_key, uid)
		)`,
		`CREATE UNIQUE INDEX idx_mail_imap_uids_message ON mail_imap_uids (message_id)`,
		`ALTER TABLE mail_imap_uids ADD CONSTRAINT fk_mail_imap_uids_message
		    FOREIGN KEY (message_id) REFERENCES mail_messages(id) ON DELETE CASCADE`,
		mailImapFolderKeyFunction,
		`WITH keyed AS (
		    SELECT id, mailbox_id, mail_imap_folder_key(direction, status, placement, folder_id) AS folder_key
		    FROM mail_messages
		)
		INSERT INTO mail_imap_uids (mailbox_id, folder_key, uid, message_id)
		SELECT mailbox_id, folder_key, row_number() OVER (PARTITION BY mailbox_id, folder_key ORDER BY id), id
		FROM keyed WHERE folder_key IS NOT NULL`,
		`INSERT INTO mail_imap_folders (mailbox_id, folder_key, uidvalidity, uidnext, created_at)
		SELECT mailbox_id, folder_key, extract(epoch FROM clock_timestamp())::bigint, max(uid) + 1, clock_timestamp()
		FROM mail_imap_uids GROUP BY mailbox_id, folder_key`,
		mailImapAssignUIDFunction,
		`CREATE TRIGGER mail_imap_assign_uid AFTER INSERT OR UPDATE OF placement, folder_id, status, mailbox_id
		    ON mail_messages FOR EACH ROW EXECUTE FUNCTION mail_imap_assign_uid()`,
		mailImapForgetFolderFunction,
		`CREATE TRIGGER mail_imap_forget_folder AFTER DELETE ON mail_folders
		    FOR EACH ROW EXECUTE FUNCTION mail_imap_forget_folder()`,
	)
}

// Down drops the UID tables and their triggers; applying Up again numbers every folder afresh
// under a new UIDVALIDITY, so mail apps resynchronize instead of trusting stale UIDs.
func (m *addMailImapUIDs) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TRIGGER IF EXISTS mail_imap_forget_folder ON mail_folders`,
		`DROP TRIGGER IF EXISTS mail_imap_assign_uid ON mail_messages`,
		`DROP FUNCTION IF EXISTS mail_imap_forget_folder()`,
		`DROP FUNCTION IF EXISTS mail_imap_assign_uid()`,
		`DROP TABLE IF EXISTS mail_imap_uids`,
		`DROP TABLE IF EXISTS mail_imap_folders`,
		`DROP FUNCTION IF EXISTS mail_imap_folder_key(text, text, text, bigint)`,
	)
}
