package mailbox

import (
	"github.com/google/wire"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
)

// ProviderSet is the provider set for this starter.
var ProviderSet = wire.NewSet(
	NewRepository,
	wire.Bind(new(domain.MailboxRepository), new(*repository)),
	mailtransport.New,
	SettingsFromConfig,
	NewService,
	wire.Bind(new(Service), new(*service)),
	NewHandler,
)

// NewStarterManifest describes the mailbox starter: its dependencies, routes, and migrations.
func NewStarterManifest(handler *Handler) assembly.StarterManifest {
	return assembly.NewStaticStarterManifest(
		config.StarterMailbox,
		assembly.WithStarterSummary("Organization mailboxes that send through Cloudflare Email Service and receive from its Email Worker."),
		assembly.WithStarterDependencies(config.StarterUser, config.StarterAudit, config.StarterAPIKey, config.StarterOrganization),
		assembly.WithStarterModule(handler),
		assembly.WithStarterMigrationNames(
			"2026_11_16_100000_create_mailbox_tables",
			"2026_11_16_110000_add_mail_message_idempotency",
			"2026_11_17_090000_move_mailboxes_to_organizations",
			"2026_11_17_100000_create_mail_message_events",
		),
	)
}
