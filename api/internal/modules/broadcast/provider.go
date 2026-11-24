package broadcast

import "github.com/google/wire"

// ProviderSet wires the top-level broadcast module.
var ProviderSet = wire.NewSet(
	NewHandler,
)
