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
	wire.Bind(new(domain.MailTemplateRepository), new(*repository)),
	wire.Bind(new(domain.MailContactRepository), new(*repository)),
	wire.Bind(new(domain.MailBroadcastRepository), new(*repository)),
	wire.Bind(new(domain.MailDomainRepository), new(*repository)),
	wire.Bind(new(domain.MailOrganizeRepository), new(*repository)),
	wire.Bind(new(domain.MailZoneRepository), new(*repository)),
	wire.Bind(new(domain.TransactionalMailRepository), new(*repository)),
	wire.Bind(new(domain.MailBroadcastDispatcher), new(*service)),
	wire.Bind(new(domain.MailZoneChecker), new(*service)),
	// Owners and members are validated against the organization starter's memberships.
	NewMembershipDirectory,
	mailtransport.New,
	mailtransport.NewDomainProvider,
	SettingsFromConfig,
	// The product webhook catalog: the webhook starter's definitions plus the mail events.
	NewWebhookCatalog,
	NewWebhookNotifier,
	wire.Bind(new(mailNotifier), new(*webhookNotifier)),
	NewService,
	wire.Bind(new(Service), new(*service)),
	NewHandler,
)

// NewMembershipDirectory reads memberships through the organization starter's repository.
func NewMembershipDirectory(organizations domain.OrganizationRepository) membershipDirectory {
	return organizations
}

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
			"2026_11_17_110000_create_mail_attachments",
			"2026_11_17_120000_create_mail_templates",
			"2026_11_17_130000_create_mail_contacts",
			"2026_11_17_140000_create_mail_broadcasts",
			"2026_11_17_150000_create_mail_domains",
			"2026_11_17_160000_add_broadcast_safety",
			"2026_11_18_090000_add_mail_template_design",
			"2026_11_18_100000_add_mail_broadcast_variables",
			"2026_11_19_090000_add_mailbox_access",
			"2026_11_19_100000_add_per_person_reads",
			"2026_11_19_100000_create_mail_zones",
			"2026_11_20_090000_add_send_queue",
			"2026_11_21_090000_add_mail_organize",
			"2026_11_21_090100_backfill_mail_threads",
			"2026_11_22_090000_add_mail_roles",
			"2026_11_22_090100_add_mail_inbox_index",
			"2026_11_23_080000_add_send_lanes",
			"2026_11_23_100000_add_transactional_templates",
			"2026_11_23_110000_create_transactional_messages",
			"2026_11_23_120000_create_mail_suppressions",
		),
	)
}
