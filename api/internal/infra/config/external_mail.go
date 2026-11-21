package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// External mail connectors an instance may enable. The OAuth connectors need the client of an OAuth
// application: the operator's own, or an organization's internal one.
const (
	ExternalMailProviderGmail   = "gmail"
	ExternalMailProviderOutlook = "outlook"
	// ExternalMailProviderIMAP binds accounts with an app password; it needs no client.
	ExternalMailProviderIMAP = "imap"
	// DefaultExternalMailOutlookTenant accepts personal and work accounts of any tenant.
	DefaultExternalMailOutlookTenant = "common"
)

// External mail sources.
const (
	// ExternalMailSourceDisabled is the default: binding answers 404 and the sync worker does
	// not start.
	ExternalMailSourceDisabled = "disabled"
	// ExternalMailSourceFake serves scripted mail from memory for tests and local demos, standing
	// in for both Gmail and Outlook. Production refuses to start with it.
	ExternalMailSourceFake = "fake"
	// ExternalMailSourceConnectors runs the real connectors the list names.
	ExternalMailSourceConnectors = "connectors"
)

// ExternalMailOAuthClient is the client of one provider's OAuth application.
type ExternalMailOAuthClient struct {
	ClientID     string
	ClientSecret string
	// Tenant is the Microsoft identity tenant sign-ins go to; unused by other providers.
	Tenant string
}

// Configured reports whether both the id and the secret are set.
func (c ExternalMailOAuthClient) Configured() bool {
	return strings.TrimSpace(c.ClientID) != "" && strings.TrimSpace(c.ClientSecret) != ""
}

// ExternalMailConfig enables read-only external mailbox binding.
type ExternalMailConfig struct {
	// Providers lists the enabled sources; empty turns the feature off.
	Providers []string
	// RedirectURL is where a provider sends the browser back after sign-in: the Web console's
	// callback page. Each provider's OAuth application must list it.
	RedirectURL string
	// OAuth holds the OAuth client of each enabled OAuth connector, keyed by provider.
	OAuth map[string]ExternalMailOAuthClient
}

// Provides reports whether the list enables the provider.
func (c ExternalMailConfig) Provides(provider string) bool {
	return slices.Contains(c.Providers, provider)
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
	if len(c.Providers) > 0 {
		return ExternalMailSourceConnectors
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

// validateExternalMailConfig refuses unknown providers, the fake source in production or next to a
// real connector, an OAuth connector without its client or the redirect URL, and an enabled
// feature without the key that seals grants.
func validateExternalMailConfig(c ExternalMailConfig, secretKey string, production bool) error {
	for _, provider := range c.Providers {
		switch provider {
		case ExternalMailSourceFake:
			if production {
				return fmt.Errorf("EXTERNAL_MAIL_PROVIDERS=fake is for tests and local demos only and is refused in production")
			}
		case ExternalMailProviderGmail, ExternalMailProviderOutlook:
			if !c.OAuth[provider].Configured() {
				name := strings.ToUpper(provider)
				return fmt.Errorf("EXTERNAL_MAIL_%s_CLIENT_ID and EXTERNAL_MAIL_%s_CLIENT_SECRET are required when EXTERNAL_MAIL_PROVIDERS lists %q",
					name, name, provider)
			}
			if c.RedirectURL == "" {
				return fmt.Errorf("EXTERNAL_MAIL_REDIRECT_URL is required when EXTERNAL_MAIL_PROVIDERS lists %q", provider)
			}
		case ExternalMailProviderIMAP:
		default:
			return fmt.Errorf("EXTERNAL_MAIL_PROVIDERS accepts %q, %q, %q, and %q; got %q",
				ExternalMailProviderGmail, ExternalMailProviderOutlook, ExternalMailProviderIMAP, ExternalMailSourceFake, provider)
		}
	}
	if c.Provides(ExternalMailSourceFake) && len(c.Providers) > 1 {
		return fmt.Errorf("EXTERNAL_MAIL_PROVIDERS=fake stands in for every provider and cannot be combined with a real one")
	}
	if c.RedirectURL != "" {
		parsed, err := url.Parse(c.RedirectURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && (parsed.Scheme != "http" || production)) {
			return fmt.Errorf("EXTERNAL_MAIL_REDIRECT_URL must be an absolute https URL (http only outside production)")
		}
	}
	if c.Enabled() && len(strings.TrimSpace(secretKey)) < MinMailboxSecretEncryptionKeyLength {
		return fmt.Errorf("MAILBOX_SECRET_ENCRYPTION_KEY must be at least %d characters when EXTERNAL_MAIL_PROVIDERS is set",
			MinMailboxSecretEncryptionKeyLength)
	}
	return nil
}
