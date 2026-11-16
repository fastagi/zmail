package mailbox

import (
	"github.com/google/wire"

	"github.com/fastagi/fastmail/api/internal/domain"
	"github.com/fastagi/fastmail/api/internal/infra/config"
	"github.com/fastagi/fastmail/api/internal/infra/mailtransport"
	"github.com/fastagi/fastmail/api/internal/starter/assembly"
)

// ProviderSet is the provider set for this starter.
var ProviderSet = wire.NewSet(
	NewRepository,
	wire.Bind(new(domain.MailboxRepository), new(*repository)),
	mailtransport.New,
	SettingsFromConfig,
	NewService,
	NewHandler,
)

// NewStarterManifest describes the mailbox starter: its dependencies, routes, and migrations.
func NewStarterManifest(handler *Handler) assembly.StarterManifest {
	return assembly.NewStaticStarterManifest(
		config.StarterMailbox,
		assembly.WithStarterSummary("User-owned mailboxes that send through Cloudflare Email Service and receive from its Email Worker."),
		assembly.WithStarterDependencies(config.StarterUser, config.StarterAudit),
		assembly.WithStarterModule(handler),
		assembly.WithStarterMigrationNames("2026_11_16_100000_create_mailbox_tables"),
	)
}
