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
