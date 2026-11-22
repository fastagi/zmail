package config

import (
	"testing"
	"time"
)

// TestExternalMailSyncConfigBounds: zero takes the defaults, and values outside the bounds are
// refused.
func TestExternalMailSyncConfigBounds(t *testing.T) {
	var zero ExternalMailSyncConfig
	if zero.FetchesPerPass() != DefaultExternalMailProviderFetchesPerPass || zero.Spacing() != DefaultExternalMailMinFetchSpacing ||
		zero.Retention() != DefaultExternalMailRevokedRetention || zero.Timeout() != DefaultExternalMailFetchTimeout ||
		zero.ImmediatePages() != DefaultExternalMailMaxImmediatePages {
		t.Fatalf("zero config does not take the defaults: %+v", zero)
	}
	if err := validateExternalMailSyncConfig(zero); err != nil {
		t.Fatalf("zero config: %v", err)
	}
	for name, cfg := range map[string]ExternalMailSyncConfig{
		"negative fetches":  {ProviderFetchesPerPass: -1},
		"too many fetches":  {ProviderFetchesPerPass: MaxExternalMailProviderFetchesPerPass + 1},
		"spacing too short": {MinFetchSpacing: time.Millisecond},
		"spacing too long":  {MinFetchSpacing: time.Hour},
		"retention short":   {RevokedRetention: time.Minute},
		"retention long":    {RevokedRetention: 10000 * time.Hour},
		"timeout too short": {FetchTimeout: time.Millisecond},
		"timeout too long":  {FetchTimeout: time.Hour},
		"too many pages":    {MaxImmediatePages: MaxExternalMailMaxImmediatePages + 1},
		"negative pages":    {MaxImmediatePages: -1},
	} {
		if err := validateExternalMailSyncConfig(cfg); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// TestExternalMailSyncConfigParsing: unset keys take defaults, days are accepted, and a key set to
// a value that does not parse fails startup instead of falling back to a default.
func TestExternalMailSyncConfigParsing(t *testing.T) {
	parsed := parseExternalMailSyncConfig(ExternalMailSyncSettings{RevokedRetention: "90d", FetchTimeout: "45s", MaxImmediatePages: "7"})
	if err := validateExternalMailSyncConfig(parsed); err != nil {
		t.Fatalf("valid settings: %v", err)
	}
	if parsed.Retention() != 90*24*time.Hour || parsed.Timeout() != 45*time.Second || parsed.ImmediatePages() != 7 ||
		parsed.FetchesPerPass() != DefaultExternalMailProviderFetchesPerPass {
		t.Fatalf("parsed = %+v", parsed)
	}
	for name, raw := range map[string]ExternalMailSyncSettings{
		"retention in words":   {RevokedRetention: "90 days"},
		"retention typo":       {RevokedRetention: "30dd"},
		"retention below 24h":  {RevokedRetention: "12h"},
		"zero retention":       {RevokedRetention: "0d"},
		"fetches not a number": {ProviderFetchesPerPass: "many"},
		"spacing unparseable":  {MinFetchSpacing: "5 sec"},
		"timeout unparseable":  {FetchTimeout: "1min"},
		"pages zero":           {MaxImmediatePages: "0"},
	} {
		if err := validateExternalMailSyncConfig(parseExternalMailSyncConfig(raw)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// TestExternalMailSharedConnectionLimit: unset takes 20, a set value must be a whole number from 1
// to 100, and anything else fails startup instead of falling back to the default.
func TestExternalMailSharedConnectionLimit(t *testing.T) {
	if limit := (ExternalMailConfig{}).SharedConnectionLimit(); limit != DefaultExternalMailMaxSharedConnections {
		t.Fatalf("default limit = %d", limit)
	}
	for raw, want := range map[string]int{"": 0, " 5 ": 5, "100": 100, "abc": -1, "0": -1, "-3": -1, "2.5": -1} {
		if got := parseExternalMailMaxSharedConnections(raw); got != want {
			t.Fatalf("parse(%q) = %d, want %d", raw, got, want)
		}
	}
	for value, valid := range map[int]bool{0: true, 1: true, 100: true, 101: false, -1: false} {
		err := validateExternalMailConfig(ExternalMailConfig{MaxSharedConnections: value}, "", false)
		if (err == nil) != valid {
			t.Fatalf("validate(%d) = %v, want valid %v", value, err, valid)
		}
	}
	if limit := (ExternalMailConfig{MaxSharedConnections: 7}).SharedConnectionLimit(); limit != 7 {
		t.Fatalf("configured limit = %d", limit)
	}
}
