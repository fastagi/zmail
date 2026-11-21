package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_05_090000_add_mail_agent_grants", &addMailAgentGrants{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addMailAgentGrants struct {
	migration.BaseMigration
}

// Up adds agent grants and what hangs off them. It only creates new tables, so it takes no lock
// on an existing one beyond the foreign keys' brief share locks.
//
//   - mail_agent_grants lets one API key receive one mailbox's new mail until expires_at, unless
//     revoked first. A grant never outlives its mailbox, organization, or key.
//   - mail_agent_events is the per-grant feed: one message.received row per grant and message,
//     written in the transaction that stores the message. Its id is the feed cursor.
//   - mail_agent_audit records every agent read and draft by id only. It carries no foreign key, so
//     a record outlives the grant, message, mailbox, or key it names.
//   - mail_agent_drafts holds reply drafts awaiting a person, one per grant and message.
func (m *addMailAgentGrants) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE mail_agent_grants (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  api_key_id bigint NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
		  key_user_id bigint NOT NULL,
		  granted_by bigint NOT NULL,
		  can_draft boolean NOT NULL DEFAULT false,
		  reveal_secrets boolean NOT NULL DEFAULT false,
		  sender_filters jsonb NOT NULL DEFAULT '[]',
		  label_ids jsonb NOT NULL DEFAULT '[]',
		  expires_at timestamptz NOT NULL,
		  revoked_at timestamptz,
		  revoked_by bigint,
		  revoke_reason varchar(32) NOT NULL DEFAULT ''
		    CHECK (revoke_reason IN ('', 'revoked', 'left_organization', 'handed_over', 'member_removed', 'connection_revoked')),
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL,
		  CONSTRAINT chk_mail_agent_grants_revoked CHECK ((revoked_at IS NULL) = (revoke_reason = '')))`,
		`CREATE INDEX idx_mail_agent_grants_mailbox ON mail_agent_grants (mailbox_id, id DESC)`,
		`CREATE INDEX idx_mail_agent_grants_live_mailbox ON mail_agent_grants (mailbox_id) WHERE revoked_at IS NULL`,
		`CREATE INDEX idx_mail_agent_grants_live_key ON mail_agent_grants (api_key_id) WHERE revoked_at IS NULL`,
		`CREATE INDEX idx_mail_agent_grants_live_user ON mail_agent_grants (organization_id, key_user_id) WHERE revoked_at IS NULL`,
		`CREATE TABLE mail_agent_events (
		  id bigserial PRIMARY KEY,
		  grant_id bigint NOT NULL REFERENCES mail_agent_grants(id) ON DELETE CASCADE,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  message_id bigint NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
		  type varchar(32) NOT NULL CHECK (type IN ('message.received')),
		  created_at timestamptz NOT NULL)`,
		`CREATE UNIQUE INDEX idx_mail_agent_events_grant_message ON mail_agent_events (grant_id, message_id)`,
		`CREATE INDEX idx_mail_agent_events_grant ON mail_agent_events (grant_id, id)`,
		`CREATE INDEX idx_mail_agent_events_created ON mail_agent_events (created_at)`,
		`CREATE INDEX idx_mail_agent_events_message ON mail_agent_events (message_id)`,
		`CREATE TABLE mail_agent_audit (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL,
		  mailbox_id bigint NOT NULL,
		  grant_id bigint NOT NULL,
		  api_key_id bigint NOT NULL,
		  message_id bigint NOT NULL,
		  action varchar(16) NOT NULL CHECK (action IN ('read', 'read_revealed', 'draft')),
		  created_at timestamptz NOT NULL)`,
		`CREATE INDEX idx_mail_agent_audit_grant ON mail_agent_audit (grant_id, id DESC)`,
		`CREATE TABLE mail_agent_drafts (
		  id bigserial PRIMARY KEY,
		  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		  grant_id bigint NOT NULL REFERENCES mail_agent_grants(id) ON DELETE CASCADE,
		  message_id bigint NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
		  api_key_id bigint NOT NULL,
		  text_body text NOT NULL,
		  html_body text NOT NULL DEFAULT '',
		  status varchar(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending')),
		  created_at timestamptz NOT NULL,
		  updated_at timestamptz NOT NULL)`,
		`CREATE UNIQUE INDEX idx_mail_agent_drafts_grant_message ON mail_agent_drafts (grant_id, message_id)`,
		`CREATE INDEX idx_mail_agent_drafts_mailbox ON mail_agent_drafts (mailbox_id, id DESC)`,
		`CREATE INDEX idx_mail_agent_drafts_message ON mail_agent_drafts (message_id)`,
	)
}

// Down drops the agent tables with every grant, event, audit record, and draft in them.
func (m *addMailAgentGrants) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS mail_agent_drafts`,
		`DROP TABLE IF EXISTS mail_agent_audit`,
		`DROP TABLE IF EXISTS mail_agent_events`,
		`DROP TABLE IF EXISTS mail_agent_grants`,
	)
}
