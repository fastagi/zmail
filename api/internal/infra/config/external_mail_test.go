package config

import (
	"strings"
	"testing"
)

const testExternalMailKey = "0123456789abcdef0123456789abcdef"

// TestExternalMailConfigDefaultsOff: with nothing set, no external provider is enabled, so binding
// answers 404 and the sync worker does not start.
func TestExternalMailConfigDefaultsOff(t *testing.T) {
	withoutEnv(t, "EXTERNAL_MAIL_PROVIDERS")
	t.Setenv("APP_ENV", "development")
	t.Setenv("DB_ENABLED", "false")

	cfg, err := LoadFresh()
	if err != nil {
		t.Fatalf("LoadFresh() error = %v", err)
	}
	if cfg.ExternalMail.Enabled() || len(cfg.ExternalMail.Providers) != 0 {
		t.Fatalf("external mail = %+v, want off", cfg.ExternalMail)
	}
	if cfg.ExternalMail.Source() != ExternalMailSourceDisabled {
		t.Fatalf("source = %q, want %q", cfg.ExternalMail.Source(), ExternalMailSourceDisabled)
	}
}

// TestExternalMailConfigReadsFakeList: the list is trimmed, lowercased, and deduplicated.
func TestExternalMailConfigReadsFakeList(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DB_ENABLED", "false")
	t.Setenv("EXTERNAL_MAIL_PROVIDERS", " Fake, fake ,")
	t.Setenv("MAILBOX_SECRET_ENCRYPTION_KEY", testExternalMailKey)

	cfg, err := LoadFresh()
	if err != nil {
		t.Fatalf("LoadFresh() error = %v", err)
	}
	if !cfg.ExternalMail.Enabled() || cfg.ExternalMail.Source() != ExternalMailSourceFake {
		t.Fatalf("external mail = %+v, want the fake source", cfg.ExternalMail)
	}
	if len(cfg.ExternalMail.Providers) != 1 {
		t.Fatalf("providers = %v, want one entry", cfg.ExternalMail.Providers)
	}
}

// TestExternalMailFakeRefusedInProduction: the in-memory fake is for tests and local demos only.
func TestExternalMailFakeRefusedInProduction(t *testing.T) {
	cfg := ExternalMailConfig{Providers: []string{ExternalMailSourceFake}}
	if err := validateExternalMailConfig(cfg, testExternalMailKey, false); err != nil {
		t.Fatalf("fake outside production: %v", err)
	}
	err := validateExternalMailConfig(cfg, testExternalMailKey, true)
	if err == nil || !strings.Contains(err.Error(), "EXTERNAL_MAIL_PROVIDERS") {
		t.Fatalf("fake in production: err = %v, want an EXTERNAL_MAIL_PROVIDERS refusal", err)
	}
	if err := validateExternalMailConfig(ExternalMailConfig{}, "", true); err != nil {
		t.Fatalf("off in production: %v", err)
	}
}

// TestExternalMailConfigRejectsUnavailableProvidersAndMissingKey: real providers wait for their
// OAuth applications, and refresh tokens are never stored without the encryption key.
func TestExternalMailConfigRejectsUnavailableProvidersAndMissingKey(t *testing.T) {
	for name, providers := range map[string][]string{
		"gmail before its source exists":   {"gmail"},
		"outlook before its source exists": {"outlook"},
		"unknown provider":                 {"imap"},
	} {
		if err := validateExternalMailConfig(ExternalMailConfig{Providers: providers}, testExternalMailKey, false); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	err := validateExternalMailConfig(ExternalMailConfig{Providers: []string{ExternalMailSourceFake}}, "short", false)
	if err == nil || !strings.Contains(err.Error(), "MAILBOX_SECRET_ENCRYPTION_KEY") {
		t.Fatalf("short key: err = %v, want a MAILBOX_SECRET_ENCRYPTION_KEY refusal", err)
	}
}
