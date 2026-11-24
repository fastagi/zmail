package triage

import "github.com/google/wire"

// ProviderSet wires the top-level triage module.
var ProviderSet = wire.NewSet(
	NewHandler,
)
