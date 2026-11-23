package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_27_140000_add_member_welcome_resends", &addMemberWelcomeResends{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMemberWelcomeResends struct {
	migration.BaseMigration
}

// Up lets an admin send a mailbox's welcome mail again. The record stays one row per mailbox and
// points at the latest welcome: last_sent_at is when that one was stored (null means created_at),
// resend_count numbers the resends for their idempotency keys, and the window columns bound them
// to a few per 24 hours. Every column has a default or is nullable, so existing rows need no
// rewrite and the table is locked only briefly.
func (m *addMemberWelcomeResends) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE member_welcome_mails
		    ADD COLUMN last_sent_at timestamptz,
		    ADD COLUMN resend_count integer DEFAULT 0 NOT NULL,
		    ADD COLUMN resend_window_started_at timestamptz,
		    ADD COLUMN resend_window_count integer DEFAULT 0 NOT NULL`,
		`ALTER TABLE member_welcome_mails ADD CONSTRAINT chk_member_welcome_mails_resends
		    CHECK (resend_count >= 0 AND resend_window_count >= 0)`,
	)
}

// Down drops the resend bookkeeping; the record keeps pointing at the latest welcome sent.
func (m *addMemberWelcomeResends) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE member_welcome_mails DROP CONSTRAINT IF EXISTS chk_member_welcome_mails_resends`,
		`ALTER TABLE member_welcome_mails
		    DROP COLUMN IF EXISTS resend_window_count,
		    DROP COLUMN IF EXISTS resend_window_started_at,
		    DROP COLUMN IF EXISTS resend_count,
		    DROP COLUMN IF EXISTS last_sent_at`,
	)
}
