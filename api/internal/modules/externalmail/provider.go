package externalmail

import "github.com/google/wire"

// ProviderSet wires the top-level externalmail module.
var ProviderSet = wire.NewSet(
	NewHandler,
)
