package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_23_090000_add_web_push", &addWebPush{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addWebPush struct {
	migration.BaseMigration
}

// Up adds the web_push notification channel: a per-user preference that defaults to on, and the
// browser subscriptions a delivery is sent to. An endpoint is unique, so a browser that subscribes
// again under another account moves to that account.
func (m *addWebPush) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_channel_check`,
		`ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_channel_check
		    CHECK ((channel IN ('in_app', 'email', 'web_push')))`,
		`ALTER TABLE notification_preferences ADD COLUMN web_push_enabled boolean DEFAULT true NOT NULL`,
		`CREATE TABLE notification_web_push_subscriptions (
		    id bigserial NOT NULL,
		    user_id bigint NOT NULL,
		    endpoint varchar(2048) NOT NULL,
		    endpoint_hash varchar(64) NOT NULL,
		    p256dh varchar(256) NOT NULL,
		    auth varchar(64) NOT NULL,
		    device_label varchar(120) DEFAULT ''::varchar NOT NULL,
		    last_success_at timestamptz,
		    failure_count smallint DEFAULT 0 NOT NULL,
		    created_at timestamptz NOT NULL,
		    updated_at timestamptz NOT NULL,
		    CONSTRAINT notification_web_push_subscriptions_pkey PRIMARY KEY (id),
		    CONSTRAINT chk_web_push_endpoint CHECK (endpoint <> ''),
		    CONSTRAINT chk_web_push_endpoint_hash CHECK (endpoint_hash ~ '^[0-9a-f]{64}$'),
		    CONSTRAINT chk_web_push_keys CHECK (p256dh <> '' AND auth <> '')
		)`,
		`CREATE UNIQUE INDEX idx_web_push_subscriptions_endpoint ON notification_web_push_subscriptions (endpoint_hash)`,
		`CREATE INDEX idx_web_push_subscriptions_user ON notification_web_push_subscriptions (user_id)`,
		`ALTER TABLE notification_web_push_subscriptions ADD CONSTRAINT fk_web_push_subscriptions_user
		    FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE`,
	)
}

// Down drops push subscriptions and web_push deliveries; push deliveries are best-effort reminders
// whose in_app record stays.
func (m *addWebPush) Down(db *gorm.DB) error {
	return execStatements(db,
		`DROP TABLE IF EXISTS notification_web_push_subscriptions CASCADE`,
		`ALTER TABLE notification_preferences DROP COLUMN IF EXISTS web_push_enabled`,
		`DELETE FROM notification_deliveries WHERE channel = 'web_push'`,
		`ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_channel_check`,
		`ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_channel_check
		    CHECK ((channel IN ('in_app', 'email')))`,
	)
}
