package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_11_23_110000_create_transactional_messages", &createTransactionalMessages{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type createTransactionalMessages struct {
	migration.BaseMigration
}

// Up stores transactional messages sent through the API or the console test send, and their
// delivery events. The rendered payload is kept encrypted only while a message is queued; every
// terminal state carries none, and test or simulated messages never record a send attempt.
func (m *createTransactionalMessages) Up(db *gorm.DB) error {
	return execStatements(db,
		`CREATE TABLE transactional_messages (
		    id bigserial NOT NULL,
		    organization_id bigint NOT NULL,
		    mode varchar(8) NOT NULL,
		    simulated boolean NOT NULL DEFAULT false,
		    api_key_id bigint,
		    created_by bigint NOT NULL,
		    template_id bigint,
		    template_key varchar(64) NOT NULL,
		    from_address varchar(320) NOT NULL,
		    from_name varchar(100) NOT NULL,
		    to_address varchar(320) NOT NULL,
		    subject_masked varchar(998) NOT NULL,
		    variables_masked jsonb NOT NULL,
		    payload_ciphertext bytea,
		    idempotency_key varchar(255),
		    idempotency_fingerprint bytea NOT NULL,
		    status varchar(16) NOT NULL,
		    queued_reason varchar(16),
		    attempts smallint NOT NULL DEFAULT 0,
		    next_attempt_at timestamptz,
		    lease_until timestamptz,
		    send_attempted_at timestamptz,
		    provider_message_id varchar(255),
		    rfc_message_id varchar(998) NOT NULL,
		    failure_code varchar(120) NOT NULL DEFAULT '',
		    last_event varchar(16),
		    sent_at timestamptz,
		    created_at timestamptz NOT NULL,
		    updated_at timestamptz NOT NULL,
		    CONSTRAINT transactional_messages_pkey PRIMARY KEY (id),
		    CONSTRAINT fk_transactional_messages_organization FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE,
		    CONSTRAINT fk_transactional_messages_template FOREIGN KEY (template_id) REFERENCES mail_templates(id) ON DELETE SET NULL,
		    CONSTRAINT chk_transactional_messages_mode CHECK (mode IN ('live', 'test')),
		    CONSTRAINT chk_transactional_messages_status CHECK (status IN ('queued', 'sent', 'failed', 'suppressed')),
		    CONSTRAINT chk_transactional_messages_queued_reason CHECK (queued_reason IN ('send_limit', 'retry')),
		    CONSTRAINT chk_transactional_messages_terminal_payload CHECK (status = 'queued' OR payload_ciphertext IS NULL),
		    CONSTRAINT chk_transactional_messages_terminal_queue CHECK (status = 'queued'
		      OR (queued_reason IS NULL AND next_attempt_at IS NULL AND lease_until IS NULL)),
		    CONSTRAINT chk_transactional_messages_attempt_lane CHECK ((mode = 'live' AND NOT simulated) OR send_attempted_at IS NULL)
		)`,
		`CREATE UNIQUE INDEX transactional_messages_idempotency_idx ON transactional_messages (organization_id, mode, idempotency_key)
		  WHERE idempotency_key IS NOT NULL`,
		`CREATE UNIQUE INDEX transactional_messages_provider_idx ON transactional_messages (provider_message_id)
		  WHERE provider_message_id IS NOT NULL`,
		`CREATE UNIQUE INDEX transactional_messages_rfc_idx ON transactional_messages (rfc_message_id)`,
		`CREATE INDEX transactional_messages_log_idx ON transactional_messages (organization_id, id DESC)`,
		`CREATE INDEX transactional_messages_to_idx ON transactional_messages (organization_id, to_address, id DESC)`,
		`CREATE INDEX transactional_messages_template_idx ON transactional_messages (organization_id, template_key, id DESC)`,
		`CREATE INDEX transactional_messages_attempted_idx ON transactional_messages (organization_id, send_attempted_at)
		  WHERE send_attempted_at IS NOT NULL`,
		`CREATE INDEX transactional_messages_due_idx ON transactional_messages (next_attempt_at, id) WHERE status = 'queued'`,
		`CREATE INDEX transactional_messages_created_idx ON transactional_messages (created_at)`,
		`CREATE TABLE transactional_message_events (
		    id bigserial NOT NULL,
		    message_id bigint NOT NULL,
		    type varchar(16) NOT NULL,
		    detail varchar(255) NOT NULL DEFAULT '',
		    provider_event_id varchar(128),
		    occurred_at timestamptz NOT NULL,
		    created_at timestamptz NOT NULL,
		    CONSTRAINT transactional_message_events_pkey PRIMARY KEY (id),
		    CONSTRAINT fk_transactional_message_events_message FOREIGN KEY (message_id) REFERENCES transactional_messages(id) ON DELETE CASCADE,
		    CONSTRAINT chk_transactional_message_events_type CHECK (type IN ('accepted', 'delivered', 'deferred', 'bounced', 'failed', 'rejected', 'complained'))
		)`,
		`CREATE UNIQUE INDEX transactional_message_events_provider_idx ON transactional_message_events (provider_event_id)
		  WHERE provider_event_id IS NOT NULL`,
		`CREATE INDEX transactional_message_events_message_idx ON transactional_message_events (message_id, occurred_at)`,
	)
}

// Down drops both tables. Transactional records of the last 30 days are lost with them.
func (m *createTransactionalMessages) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS transactional_message_events`,
		`DROP TABLE IF EXISTS transactional_messages`,
	)
}
