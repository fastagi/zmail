package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// External mail sources. The real Gmail and Outlook sources are added once their OAuth
// applications exist; until then no client id or secret is read.
const (
	// ExternalMailSourceDisabled is the default: binding answers 404 and the sync worker does
	// not start.
	ExternalMailSourceDisabled = "disabled"
	// ExternalMailSourceFake serves scripted mail from memory for tests and local demos, standing
	// in for both Gmail and Outlook. Production refuses to start with it.
	ExternalMailSourceFake = "fake"
)

// ExternalMailConfig enables read-only external mailbox binding.
type ExternalMailConfig struct {
	// Providers lists the enabled sources; empty turns the feature off.
	Providers []string
	// RedirectURL is where a provider sends the browser back after sign-in: the Web console's
	// callback page. Each provider's OAuth application must list it.
	RedirectURL string
}

// Enabled reports whether any source is enabled, so binding endpoints and the worker exist.
func (c ExternalMailConfig) Enabled() bool {
	return len(c.Providers) > 0
}

// Source names the source implementation the instance runs.
func (c ExternalMailConfig) Source() string {
	if slices.Contains(c.Providers, ExternalMailSourceFake) {
		return ExternalMailSourceFake
	}
	return ExternalMailSourceDisabled
}

// parseExternalMailProviders turns the comma-separated list into trimmed, lowercased, unique
// entries.
func parseExternalMailProviders(raw string) []string {
	var providers []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry != "" && !slices.Contains(providers, entry) {
			providers = append(providers, entry)
		}
	}
	return providers
}

// validateExternalMailConfig refuses providers without a source, the fake source in production,
// and an enabled feature without the key that seals refresh tokens.
func validateExternalMailConfig(c ExternalMailConfig, secretKey string, production bool) error {
	for _, provider := range c.Providers {
		switch provider {
		case ExternalMailSourceFake:
			if production {
				return fmt.Errorf("EXTERNAL_MAIL_PROVIDERS=fake is for tests and local demos only and is refused in production")
			}
		default:
			return fmt.Errorf("EXTERNAL_MAIL_PROVIDERS accepts only %q until the Gmail and Outlook sources exist; got %q",
				ExternalMailSourceFake, provider)
		}
	}
	if c.RedirectURL != "" {
		parsed, err := url.Parse(c.RedirectURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && !production)) {
			return fmt.Errorf("EXTERNAL_MAIL_REDIRECT_URL must be an absolute https URL (http only outside production)")
		}
	}
	if c.Enabled() && len(strings.TrimSpace(secretKey)) < MinMailboxSecretEncryptionKeyLength {
		return fmt.Errorf("MAILBOX_SECRET_ENCRYPTION_KEY must be at least %d characters when EXTERNAL_MAIL_PROVIDERS is set",
			MinMailboxSecretEncryptionKeyLength)
	}
	return nil
}
