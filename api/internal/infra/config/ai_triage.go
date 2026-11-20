package config

import "fmt"

// AI triage model providers. Real providers are added once one is confirmed; until then no
// provider key is read.
const (
	// AITriageProviderDisabled is the default: no model is called and the feature stays hidden.
	AITriageProviderDisabled = "disabled"
	// AITriageProviderFake classifies with fixed rules for tests and local demos. Production
	// refuses to start with it.
	AITriageProviderFake = "fake"

	// DefaultAITriageDailyCap is the instance ceiling of model calls per organization per UTC day.
	DefaultAITriageDailyCap = 500
	// MaxAITriageDailyCap is the highest AI_TRIAGE_DAILY_CAP accepted.
	MaxAITriageDailyCap = 100000
	// maxAITriageModelLength matches mail_triage.model.
	maxAITriageModelLength = 64
)

// AITriageConfig selects the model behind mailbox AI triage and bounds its use.
type AITriageConfig struct {
	// Provider is "disabled" (default) or "fake".
	Provider string
	// Model labels stored results; empty takes the provider name.
	Model string
	// DailyCap bounds the model calls one organization may make per UTC day; an organization's
	// own cap can only be lower.
	DailyCap int
}

// Available reports whether a model is configured, so the feature may be offered at all.
func (c AITriageConfig) Available() bool {
	return c.Provider != "" && c.Provider != AITriageProviderDisabled
}

// ModelLabel is the model name stored with each result.
func (c AITriageConfig) ModelLabel() string {
	if c.Model != "" {
		return c.Model
	}
	if c.Provider == "" {
		return AITriageProviderDisabled
	}
	return c.Provider
}

// validateAITriageConfig refuses unknown providers, the fake provider in production, and caps
// outside 1..MaxAITriageDailyCap. A zero value (a config built in code) is the disabled default.
func validateAITriageConfig(c AITriageConfig, production bool) error {
	if c == (AITriageConfig{}) {
		return nil
	}
	switch c.Provider {
	case "", AITriageProviderDisabled:
	case AITriageProviderFake:
		if production {
			return fmt.Errorf("AI_TRIAGE_PROVIDER=fake is for tests and local demos only and is refused in production")
		}
	default:
		return fmt.Errorf("AI_TRIAGE_PROVIDER must be %q or %q", AITriageProviderDisabled, AITriageProviderFake)
	}
	if c.DailyCap < 1 || c.DailyCap > MaxAITriageDailyCap {
		return fmt.Errorf("AI_TRIAGE_DAILY_CAP must be between 1 and %d", MaxAITriageDailyCap)
	}
	if len(c.Model) > maxAITriageModelLength {
		return fmt.Errorf("AI_TRIAGE_MODEL must be at most %d characters", maxAITriageModelLength)
	}
	return nil
}
