package permission

import (
	"github.com/google/wire"

	"github.com/fastagi/fastmail/api/internal/domain"
	"github.com/fastagi/fastmail/api/internal/infra/config"
	"github.com/fastagi/fastmail/api/internal/starter/assembly"
)

// ProviderSet wires the optional permission starter and its replaceable authorizer.
var ProviderSet = wire.NewSet(
	NewRepository,
	wire.Bind(new(domain.PermissionRepository), new(*repository)),
	NewDefaultCatalog,
	NewService,
	wire.Bind(new(Service), new(*service)),
	wire.Bind(new(domain.PermissionAuthorizer), new(*service)),
	NewGuard,
	NewHandler,
)

// NewStarterManifest describes permission runtime and migration ownership.
func NewStarterManifest(handler *Handler) assembly.StarterManifest {
	return assembly.NewStaticStarterManifest(
		config.StarterPermission,
		assembly.WithStarterSummary("Organization-scoped access roles, exact permission keys, member assignments, and fail-closed checks."),
		assembly.WithStarterDependencies(config.StarterOrganization),
		assembly.WithStarterModule(handler),
		assembly.WithStarterMigrationNames("2026_07_15_010000_create_permission_tables"),
	)
}
