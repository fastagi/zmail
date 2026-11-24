package roster

import (
	"github.com/google/wire"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ProviderSet wires the roster module and binds its PostgreSQL repository to the shared
// domain.MailContactRepository port for cross-module consumers (such as broadcasts).
var ProviderSet = wire.NewSet(
	NewRepository,
	wire.Bind(new(domain.MailContactRepository), new(*Repository)),
	NewService,
	NewHandler,
)
