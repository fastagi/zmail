package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_26_090000_add_mail_triage", &addMailTriage{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailTriage struct {
	migration.BaseMigration
}

// Up adds AI triage: one mail_triage row per inbound message queued for or given a triage result,
// one mail_triage_settings row per organization that saved its AI triage settings, and the
// mailbox switch. Everything starts off.
//
// mail_triage.category is null until a result exists; a deadline alone carries due_at. The
// partial pending index serves the worker's claim, and the result index serves the unified inbox
// counts. usage_day and usage_count on the settings row hold the organization's model calls of
// one UTC day, so the worker reserves a call against the daily cap in one statement.
//
// The new tables lock nothing existing. The mailboxes column has a constant default, which
// PostgreSQL 11+ records in the catalog without rewriting the table.
func (m *addMailTriage) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE mailboxes ADD COLUMN ai_triage_enabled boolean NOT NULL DEFAULT false`,
		`CREATE TABLE mail_triage_settings (
		  organization_id bigint PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
		  enabled boolean NOT NULL DEFAULT false,
		  daily_message_cap integer NOT NULL DEFAULT 500 CHECK (daily_message_cap BETWEEN 1 AND 100000),
		  draft_enabled boolean NOT NULL DEFAULT false,
		  usage_day date,
		  usage_count integer NOT NULL DEFAULT 0 CHECK (usage_count >= 0),
		  updated_by bigint REFERENCES users(id) ON DELETE SET NULL,
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL)`,
		`CREATE TABLE mail_triage (
		  message_id bigint PRIMARY KEY REFERENCES mail_messages(id) ON DELETE CASCADE,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  category varchar(16) CHECK (category IN ('reply','deadline','fyi','review')),
		  reason varchar(120) NOT NULL DEFAULT '',
		  due_at timestamptz,
		  source varchar(16) NOT NULL DEFAULT 'ai' CHECK (source IN ('ai','manual')),
		  status varchar(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','done','failed','skipped')),
		  attempts smallint NOT NULL DEFAULT 0 CHECK (attempts >= 0),
		  last_error_code varchar(64),
		  model varchar(64) NOT NULL DEFAULT '',
		  decided_by bigint REFERENCES users(id) ON DELETE SET NULL,
		  next_attempt_at timestamptz NOT NULL,
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL,
		  CONSTRAINT mail_triage_result_has_category CHECK (status NOT IN ('done','failed') OR category IS NOT NULL),
		  CONSTRAINT mail_triage_due_only_for_deadline CHECK (due_at IS NULL OR category = 'deadline'))`,
		`CREATE INDEX idx_mail_triage_pending ON mail_triage (next_attempt_at, message_id) WHERE status = 'pending'`,
		`CREATE INDEX idx_mail_triage_results ON mail_triage (mailbox_id, category) WHERE status IN ('done','failed')`,
		`CREATE INDEX idx_mail_triage_overrides ON mail_triage (mailbox_id, updated_at) WHERE source = 'manual'`,
	)
}

// Down drops AI triage; stored results and settings are lost.
func (m *addMailTriage) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS mail_triage`,
		`DROP TABLE IF EXISTS mail_triage_settings`,
		`ALTER TABLE mailboxes DROP COLUMN IF EXISTS ai_triage_enabled`,
	)
}
