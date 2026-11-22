package config

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
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
	// Sync bounds how hard the sync worker reads providers and how long revoked copies stay.
	Sync ExternalMailSyncConfig
	// MaxSharedConnections bounds the live shared connections (team accounts bound by owners and
	// admins) of one organization; zero takes DefaultExternalMailMaxSharedConnections, and -1 marks
	// a set value that does not parse, which validation refuses.
	MaxSharedConnections int
	// TenantsEnabled turns tenant connect on: owners authorize whole tenants and assign mailboxes from
	// them. Off by default; while off every tenant route answers as an unavailable feature, and the
	// worker keeps syncing the mailboxes already assigned.
	TenantsEnabled bool
	// MaxTenantAssignments bounds the live connections (assigned mailboxes) of one authorized tenant;
	// zero takes DefaultExternalMailMaxTenantAssignments, and -1 marks a set value that does not
	// parse, which validation refuses.
	MaxTenantAssignments int
	// Push turns push subscriptions on, with each provider's settings.
	Push ExternalMailPushConfig
}

// Bounds of the per-tenant assignment ceiling.
const (
	DefaultExternalMailMaxTenantAssignments = 1000
	// MaxExternalMailMaxTenantAssignments matches domain.MaxExternalMailTenantAssignmentsCeiling.
	MaxExternalMailMaxTenantAssignments = 10000
)

// TenantAssignmentLimit returns the per-tenant assignment ceiling, or its default.
func (c ExternalMailConfig) TenantAssignmentLimit() int {
	if c.MaxTenantAssignments <= 0 {
		return DefaultExternalMailMaxTenantAssignments
	}
	return c.MaxTenantAssignments
}

// Bounds of the per-organization limit of live shared connections.
const (
	DefaultExternalMailMaxSharedConnections = 20
	// MaxExternalMailMaxSharedConnections matches domain.MaxSharedExternalMailConnectionsCeiling.
	MaxExternalMailMaxSharedConnections = 100
)

// SharedConnectionLimit returns the per-organization limit of live shared connections, or its
// default.
func (c ExternalMailConfig) SharedConnectionLimit() int {
	if c.MaxSharedConnections <= 0 {
		return DefaultExternalMailMaxSharedConnections
	}
	return c.MaxSharedConnections
}

// parseExternalMailMaxSharedConnections reads EXTERNAL_MAIL_MAX_SHARED_CONNECTIONS, and
// EXTERNAL_MAIL_MAX_TENANT_ASSIGNMENTS the same way: empty takes the default (0), a value that is not a positive whole number is -1 and refused by validation.
func parseExternalMailMaxSharedConnections(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return -1
	}
	return parsed
}

// Defaults of the sync budget and the retention of revoked copies.
const (
	// DefaultExternalMailProviderFetchesPerPass matches the worker's default batch, so one provider
	// alone keeps the full batch while several share it round-robin.
	DefaultExternalMailProviderFetchesPerPass = 20
	MaxExternalMailProviderFetchesPerPass     = 1000
	// DefaultExternalMailMinFetchSpacing separates two fetches of one connection that has more
	// pages ready, so a long backfill cannot occupy every pass.
	DefaultExternalMailMinFetchSpacing = 5 * time.Second
	MaxExternalMailMinFetchSpacing     = 10 * time.Minute
	// DefaultExternalMailRevokedRetention is how long copies of a revoked connection stay before
	// the worker deletes them: 30 days.
	DefaultExternalMailRevokedRetention = 720 * time.Hour
	MinExternalMailRevokedRetention     = 24 * time.Hour
	MaxExternalMailRevokedRetention     = 8760 * time.Hour
	// DefaultExternalMailFetchTimeout bounds one fetch, so a slow or hostile provider cannot stall
	// the sequential pass.
	DefaultExternalMailFetchTimeout = 60 * time.Second
	MaxExternalMailFetchTimeout     = 10 * time.Minute
	// DefaultExternalMailMaxImmediatePages is how many pages in a row one connection may fetch at
	// once before each further page waits the spacing.
	DefaultExternalMailMaxImmediatePages = 20
	MaxExternalMailMaxImmediatePages     = 1000
)

// ExternalMailSyncConfig is the sync worker's budget. A zero field takes its default, so a
// configuration built in code needs none of them.
type ExternalMailSyncConfig struct {
	// ProviderFetchesPerPass caps the fetches of one provider in one worker pass.
	ProviderFetchesPerPass int
	// MinFetchSpacing is the least time between two fetches of one connection.
	MinFetchSpacing time.Duration
	// RevokedRetention is how long the copies of a revoked connection are kept.
	RevokedRetention time.Duration
	// FetchTimeout bounds one fetch of one connection.
	FetchTimeout time.Duration
	// MaxImmediatePages is how many pages in a row are fetched again at once.
	MaxImmediatePages int
	// invalid names the keys that were set to a value that does not parse; startup refuses them,
	// so a typo never silently falls back to a default (for the retention, an earlier deletion).
	invalid []string
}

// ExternalMailSyncSettings are the raw values of the sync budget keys; empty means unset.
type ExternalMailSyncSettings struct {
	ProviderFetchesPerPass string
	MinFetchSpacing        string
	RevokedRetention       string
	FetchTimeout           string
	MaxImmediatePages      string
}

// parseExternalMailSyncConfig parses the sync budget. An unset key takes its default; a key set to
// a value that does not parse is recorded and refused by validation.
func parseExternalMailSyncConfig(raw ExternalMailSyncSettings) ExternalMailSyncConfig {
	var c ExternalMailSyncConfig
	integer := func(key, value string, into *int) {
		if value = strings.TrimSpace(value); value == "" {
			return
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			c.invalid = append(c.invalid, key)
			return
		}
		*into = parsed
	}
	duration := func(key, value string, into *time.Duration) {
		if value = strings.TrimSpace(value); value == "" {
			return
		}
		parsed, err := parseExternalMailDuration(value)
		if err != nil || parsed <= 0 {
			c.invalid = append(c.invalid, key)
			return
		}
		*into = parsed
	}
	integer("EXTERNAL_MAIL_PROVIDER_FETCHES_PER_PASS", raw.ProviderFetchesPerPass, &c.ProviderFetchesPerPass)
	duration("EXTERNAL_MAIL_MIN_FETCH_SPACING", raw.MinFetchSpacing, &c.MinFetchSpacing)
	duration("EXTERNAL_MAIL_REVOKED_RETENTION", raw.RevokedRetention, &c.RevokedRetention)
	duration("EXTERNAL_MAIL_FETCH_TIMEOUT", raw.FetchTimeout, &c.FetchTimeout)
	integer("EXTERNAL_MAIL_MAX_IMMEDIATE_PAGES", raw.MaxImmediatePages, &c.MaxImmediatePages)
	return c
}

// parseExternalMailDuration accepts a Go duration ("720h", "90s") or a whole number of days
// ("30d").
func parseExternalMailDuration(value string) (time.Duration, error) {
	if days, found := strings.CutSuffix(value, "d"); found {
		count, err := strconv.Atoi(days)
		if err != nil || count <= 0 || count > 3660 {
			return 0, fmt.Errorf("invalid day count %q", value)
		}
		return time.Duration(count) * 24 * time.Hour, nil
	}
	return time.ParseDuration(value)
}

// Timeout returns the fetch deadline, or its default.
func (c ExternalMailSyncConfig) Timeout() time.Duration {
	if c.FetchTimeout <= 0 {
		return DefaultExternalMailFetchTimeout
	}
	return c.FetchTimeout
}

// ImmediatePages returns the bound on immediate refetches, or its default.
func (c ExternalMailSyncConfig) ImmediatePages() int {
	if c.MaxImmediatePages <= 0 {
		return DefaultExternalMailMaxImmediatePages
	}
	return c.MaxImmediatePages
}

// FetchesPerPass returns the per-provider cap, or its default.
func (c ExternalMailSyncConfig) FetchesPerPass() int {
	if c.ProviderFetchesPerPass <= 0 {
		return DefaultExternalMailProviderFetchesPerPass
	}
	return c.ProviderFetchesPerPass
}

// Spacing returns the per-connection spacing, or its default.
func (c ExternalMailSyncConfig) Spacing() time.Duration {
	if c.MinFetchSpacing <= 0 {
		return DefaultExternalMailMinFetchSpacing
	}
	return c.MinFetchSpacing
}

// Retention returns the retention of revoked copies, or its default.
func (c ExternalMailSyncConfig) Retention() time.Duration {
	if c.RevokedRetention <= 0 {
		return DefaultExternalMailRevokedRetention
	}
	return c.RevokedRetention
}

// validateExternalMailSyncConfig refuses a budget outside its bounds; zero takes the default.
func validateExternalMailSyncConfig(c ExternalMailSyncConfig) error {
	if len(c.invalid) > 0 {
		return fmt.Errorf("%s must be a positive number, or a duration such as 90s, 720h, or 30d", strings.Join(c.invalid, ", "))
	}
	if c.ProviderFetchesPerPass < 0 || c.ProviderFetchesPerPass > MaxExternalMailProviderFetchesPerPass {
		return fmt.Errorf("EXTERNAL_MAIL_PROVIDER_FETCHES_PER_PASS must be between 1 and %d", MaxExternalMailProviderFetchesPerPass)
	}
	if c.MinFetchSpacing != 0 && (c.MinFetchSpacing < time.Second || c.MinFetchSpacing > MaxExternalMailMinFetchSpacing) {
		return fmt.Errorf("EXTERNAL_MAIL_MIN_FETCH_SPACING must be between 1s and %s", MaxExternalMailMinFetchSpacing)
	}
	if c.FetchTimeout != 0 && (c.FetchTimeout < time.Second || c.FetchTimeout > MaxExternalMailFetchTimeout) {
		// The worker renews a connection's lease to the deadline before each fetch, so no other
		// worker can claim it while the fetch may still run.
		return fmt.Errorf("EXTERNAL_MAIL_FETCH_TIMEOUT must be between 1s and %s", MaxExternalMailFetchTimeout)
	}
	if c.MaxImmediatePages < 0 || c.MaxImmediatePages > MaxExternalMailMaxImmediatePages {
		return fmt.Errorf("EXTERNAL_MAIL_MAX_IMMEDIATE_PAGES must be between 1 and %d", MaxExternalMailMaxImmediatePages)
	}
	if c.RevokedRetention != 0 && (c.RevokedRetention < MinExternalMailRevokedRetention || c.RevokedRetention > MaxExternalMailRevokedRetention) {
		return fmt.Errorf("EXTERNAL_MAIL_REVOKED_RETENTION must be between %s and %s",
			MinExternalMailRevokedRetention, MaxExternalMailRevokedRetention)
	}
	return nil
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
	if err := validateExternalMailSyncConfig(c.Sync); err != nil {
		return err
	}
	if err := validateExternalMailPushConfig(c.Push, production); err != nil {
		return err
	}
	if c.MaxSharedConnections < 0 || c.MaxSharedConnections > MaxExternalMailMaxSharedConnections {
		return fmt.Errorf("EXTERNAL_MAIL_MAX_SHARED_CONNECTIONS must be a whole number between 1 and %d", MaxExternalMailMaxSharedConnections)
	}
	if c.MaxTenantAssignments < 0 || c.MaxTenantAssignments > MaxExternalMailMaxTenantAssignments {
		return fmt.Errorf("EXTERNAL_MAIL_MAX_TENANT_ASSIGNMENTS must be a whole number between 1 and %d", MaxExternalMailMaxTenantAssignments)
	}
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
