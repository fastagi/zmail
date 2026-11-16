package app

import (
	"gorm.io/gorm"

	"github.com/fastagi/fastmail/api/internal/domain"
	"github.com/fastagi/fastmail/api/internal/infra/config"
	"github.com/fastagi/fastmail/api/internal/infra/email"
	"github.com/fastagi/fastmail/api/internal/infra/events"
	"github.com/fastagi/fastmail/api/internal/infra/migration"
	"github.com/fastagi/fastmail/api/internal/infra/ratelimit"
	"github.com/fastagi/fastmail/api/internal/starter"
)

// Application holds all application dependencies injected via Wire.
// This is the root container for the entire application.
type Application struct {
	Config                 *config.Config
	DB                     *gorm.DB
	EmailService           *email.Service
	EventBus               *events.EventBus
	Migrator               *migration.Migrator
	RateLimitStores        *ratelimit.Stores
	Starters               *starter.Registry
	AuditRecorder          domain.AuditLogRecorder
	AuditMaintainer        domain.AuditLogMaintainer
	AuthenticationSessions domain.AuthenticationSessionMaintainer
	NotificationPublisher  domain.NotificationPublisher
	NotificationDispatcher domain.NotificationDispatcher
	AssetReader            domain.AssetReader
	AssetMaintainer        domain.AssetMaintainer
	SettingReader          domain.SettingReader
	AppSettingWriter       domain.AppSettingWriter
	UsageReader            domain.UsageReader
	UsageRecorder          domain.UsageRecorder
	UsageConsumer          domain.UsageConsumer
	UsageQuotaWriter       domain.UsageQuotaWriter
	UsageMaintainer        domain.UsageMaintainer
	WebhookPublisher       domain.WebhookPublisher
	WebhookDispatcher      domain.WebhookDispatcher
	WebhookTester          domain.WebhookTester
	WebhookMaintainer      domain.WebhookMaintainer
	OperatorGrants         domain.OperatorGrantStore
}
