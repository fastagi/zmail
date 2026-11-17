package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_17_160000_add_broadcast_safety", &addBroadcastSafety{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addBroadcastSafety struct {
	migration.BaseMigration
}

// Up adds staged sending and automatic pausing to broadcasts. test_percent of the recipients form
// wave 1; the rest wait in wave 2 until wave 1 is done, test_wait_minutes have passed, and its
// bounce and complaint rates are acceptable. A broadcast the worker paused, or an owner paused,
// is in the new paused status; safety_override records that an owner resumed one that the health
// check stopped. Existing broadcasts and recipients keep sending in one wave.
func (m *addBroadcastSafety) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mail_broadcasts ADD COLUMN test_percent smallint NOT NULL DEFAULT 0`,
		`ALTER TABLE mail_broadcasts ADD COLUMN test_wait_minutes integer NOT NULL DEFAULT 30`,
		`ALTER TABLE mail_broadcasts ADD COLUMN current_wave smallint NOT NULL DEFAULT 1`,
		`ALTER TABLE mail_broadcasts ADD COLUMN resume_at timestamptz`,
		`ALTER TABLE mail_broadcasts ADD COLUMN safety_override boolean NOT NULL DEFAULT false`,
		`ALTER TABLE mail_broadcasts DROP CONSTRAINT chk_mail_broadcasts_status`,
		`ALTER TABLE mail_broadcasts ADD CONSTRAINT chk_mail_broadcasts_status CHECK (status IN ('draft', 'scheduled', 'sending', 'paused', 'sent', 'canceled'))`,
		`ALTER TABLE mail_broadcasts ADD CONSTRAINT chk_mail_broadcasts_test_percent CHECK (test_percent BETWEEN 0 AND 50)`,
		`ALTER TABLE mail_broadcast_recipients ADD COLUMN wave smallint NOT NULL DEFAULT 1`,
	)
}

// Down removes staged sending; paused broadcasts become canceled first.
func (m *addBroadcastSafety) Down(db *gorm.DB) error {
	return execStatements(db,
		`UPDATE mail_broadcasts SET status = 'canceled' WHERE status = 'paused'`,
		`ALTER TABLE mail_broadcast_recipients DROP COLUMN IF EXISTS wave`,
		`ALTER TABLE mail_broadcasts DROP CONSTRAINT IF EXISTS chk_mail_broadcasts_test_percent`,
		`ALTER TABLE mail_broadcasts DROP CONSTRAINT chk_mail_broadcasts_status`,
		`ALTER TABLE mail_broadcasts ADD CONSTRAINT chk_mail_broadcasts_status CHECK (status IN ('draft', 'scheduled', 'sending', 'sent', 'canceled'))`,
		`ALTER TABLE mail_broadcasts DROP COLUMN IF EXISTS safety_override`,
		`ALTER TABLE mail_broadcasts DROP COLUMN IF EXISTS resume_at`,
		`ALTER TABLE mail_broadcasts DROP COLUMN IF EXISTS current_wave`,
		`ALTER TABLE mail_broadcasts DROP COLUMN IF EXISTS test_wait_minutes`,
		`ALTER TABLE mail_broadcasts DROP COLUMN IF EXISTS test_percent`,
	)
}
