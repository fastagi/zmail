package organization

import (
	"github.com/google/wire"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/events"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
)

// ProviderSet wires the optional organization starter.
var ProviderSet = wire.NewSet(
	NewRepository,
	wire.Bind(new(domain.OrganizationRepository), new(*repository)),
	wire.Bind(new(domain.OrganizationInvitationRepository), new(*repository)),
	wire.Bind(new(domain.PlatformOrganizationDirectory), new(*repository)),
	NewInvitationPolicy,
	NewInvitationMailer,
	NewMembershipEventPublisher,
	NewService,
	wire.Bind(new(Service), new(*service)),
	wire.Bind(new(domain.OrganizationAdministrator), new(*service)),
	wire.Bind(new(domain.OrganizationRosterInviter), new(*service)),
	NewContextResolver,
	NewHandler,
	NewOperatorHandler,
)

// NewStarterManifest describes the optional organization starter surfaces.
func NewStarterManifest(handler *Handler) assembly.StarterManifest {
	return assembly.NewStaticStarterManifest(
		config.StarterOrganization,
		assembly.WithStarterSummary("Organization ownership, membership, invitations, active context, and ownership transfer."),
		assembly.WithStarterModule(handler),
		assembly.WithStarterMigrationNames(
			"2026_07_14_000000_create_organizations_tables",
			"2026_07_15_000000_create_organization_invitations_table",
			"2026_12_07_090000_add_organization_invitation_email_status",
			"2026_12_18_090000_add_member_feature_grants",
			"2026_12_21_090000_add_invitation_feature_grants",
			"2026_12_24_110000_add_mail_client_feature_grants",
			"2026_12_27_090000_add_organization_controls",
		),
	)
}

// NewMembershipEventPublisher publishes membership events on the application's event bus.
func NewMembershipEventPublisher(bus *events.EventBus) MembershipEventPublisher {
	return bus
}
