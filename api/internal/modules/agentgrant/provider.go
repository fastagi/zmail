package agentgrant

import "github.com/google/wire"

// ProviderSet wires the top-level agentgrant module.
var ProviderSet = wire.NewSet(
	NewHandler,
)
