package config

import (
	"strings"
	"testing"
)

// TestAITriageConfigDefaultsOff: with nothing set, AI triage is disabled and the instance cap is
// the documented default.
func TestAITriageConfigDefaultsOff(t *testing.T) {
	withoutEnv(t, "AI_TRIAGE_PROVIDER", "AI_TRIAGE_MODEL", "AI_TRIAGE_DAILY_CAP")
	t.Setenv("APP_ENV", "development")
	t.Setenv("DB_ENABLED", "false")

	cfg, err := LoadFresh()
	if err != nil {
		t.Fatalf("LoadFresh() error = %v", err)
	}
	if cfg.AITriage.Provider != AITriageProviderDisabled {
		t.Fatalf("provider = %q, want %q", cfg.AITriage.Provider, AITriageProviderDisabled)
	}
	if cfg.AITriage.Available() {
		t.Fatal("AI triage must be unavailable by default")
	}
	if cfg.AITriage.DailyCap != DefaultAITriageDailyCap {
		t.Fatalf("daily cap = %d, want %d", cfg.AITriage.DailyCap, DefaultAITriageDailyCap)
	}
	if cfg.AITriage.ModelLabel() != AITriageProviderDisabled {
		t.Fatalf("model label = %q, want the provider name", cfg.AITriage.ModelLabel())
	}
}

// TestAITriageFakeRefusedInProduction: the rule-based fake is for tests and local demos only.
func TestAITriageFakeRefusedInProduction(t *testing.T) {
	cfg := AITriageConfig{Provider: AITriageProviderFake, DailyCap: DefaultAITriageDailyCap}
	if err := validateAITriageConfig(cfg, false); err != nil {
		t.Fatalf("fake outside production: %v", err)
	}
	err := validateAITriageConfig(cfg, true)
	if err == nil || !strings.Contains(err.Error(), "AI_TRIAGE_PROVIDER") {
		t.Fatalf("fake in production: err = %v, want an AI_TRIAGE_PROVIDER refusal", err)
	}
	if err := validateAITriageConfig(AITriageConfig{Provider: AITriageProviderDisabled, DailyCap: 1}, true); err != nil {
		t.Fatalf("disabled in production: %v", err)
	}
}

func TestAITriageConfigRejectsUnknownProviderAndCap(t *testing.T) {
	for name, cfg := range map[string]AITriageConfig{
		"unknown provider": {Provider: "anthropic", DailyCap: 1},
		"cap zero":         {Provider: AITriageProviderDisabled, DailyCap: 0},
		"cap too large":    {Provider: AITriageProviderFake, DailyCap: 100001},
		"model too long":   {Provider: AITriageProviderFake, DailyCap: 1, Model: strings.Repeat("m", 65)},
	} {
		if err := validateAITriageConfig(cfg, false); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	fake := AITriageConfig{Provider: AITriageProviderFake, DailyCap: 10, Model: "fake-rules"}
	if !fake.Available() || fake.ModelLabel() != "fake-rules" {
		t.Fatalf("fake config: available=%v label=%q", fake.Available(), fake.ModelLabel())
	}
}
