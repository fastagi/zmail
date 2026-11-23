package webhook

import (
	"github.com/google/wire"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
)

// ProviderSet wires the optional durable outbound webhook starter.
// The event catalog is provided by the product (mailbox.NewWebhookCatalog), which extends
// DefaultDefinitions with its own event types.
var ProviderSet = wire.NewSet(
	NewRepository,
	wire.Bind(new(webhookStore), new(*repository)),
	NewSecretProtector,
	NewTargetPolicy,
	NewSender,
	NewService,
	wire.Bind(new(Service), new(*service)),
	wire.Bind(new(domain.WebhookPublisher), new(*service)),
	wire.Bind(new(domain.WebhookDispatcher), new(*service)),
	wire.Bind(new(domain.WebhookTester), new(*service)),
	wire.Bind(new(domain.WebhookMaintainer), new(*service)),
	NewHandler,
	NewOperatorHandler,
)

// NewStarterManifest describes webhook dependencies, routes, and persistence ownership.
func NewStarterManifest(handler *Handler) assembly.StarterManifest {
	return assembly.NewStaticStarterManifest(
		config.StarterWebhook,
		assembly.WithStarterSummary("Organization-owned outbound endpoints with signed durable delivery, retries, and replay."),
		assembly.WithStarterDependencies(config.StarterUser, config.StarterAudit, config.StarterOrganization),
		assembly.WithStarterModule(handler),
		assembly.WithStarterMigrationNames(
			"2026_07_15_060000_create_webhook_tables",
			"2026_12_28_090100_add_webhook_endpoint_developer_owned",
		),
	)
}
