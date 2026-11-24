package maildomain

import "github.com/google/wire"

// ProviderSet wires the top-level maildomain module.
var ProviderSet = wire.NewSet(
	NewHandler,
)
