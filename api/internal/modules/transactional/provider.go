package transactional

import "github.com/google/wire"

// ProviderSet wires the top-level transactional module.
var ProviderSet = wire.NewSet(
	NewHandler,
)
