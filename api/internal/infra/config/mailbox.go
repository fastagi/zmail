package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Mailbox transport providers.
const (
	// MailboxProviderLog records outbound mail in the log instead of delivering it.
	MailboxProviderLog = "log"
	// MailboxProviderCloudflare delivers through the Cloudflare Email Sending REST API.
	MailboxProviderCloudflare = "cloudflare"

	// MaxMailboxZonesPerOrganization is the highest MAILBOX_MAX_ZONES_PER_ORGANIZATION accepted. It
	// equals domain.MaxMailZonesPerOrganization; config may not import domain, so a mailbox test
	// keeps the two in step.
	MaxMailboxZonesPerOrganization = 20
	// DefaultMailboxDomain is the development-only address domain.
	DefaultMailboxDomain = "mail.localhost"
	// DefaultMailboxRequestTimeout caps one outbound provider call.
	DefaultMailboxRequestTimeout = 15 * time.Second
	// DefaultMailboxDailySendLimit bounds personal outbound messages per organization per 24 hours.
	DefaultMailboxDailySendLimit = 200
	// DefaultMailboxNewOrganizationPeriod is how long an organization counts as new for
	// MAILBOX_NEW_ORGANIZATION_DAILY_SEND_LIMIT.
	DefaultMailboxNewOrganizationPeriod = 7 * 24 * time.Hour
	// DefaultMailboxInboundTolerance is the accepted clock skew for a signed inbound delivery.
	DefaultMailboxInboundTolerance = 5 * time.Minute
	// DefaultMailboxMaxPerOrganization bounds the mailboxes one organization may own.
	DefaultMailboxMaxPerOrganization = 50
	// MailboxMaxPerOrganizationCeiling is the highest MAILBOX_MAX_PER_ORGANIZATION accepted and
	// the bound of every mailbox list. It equals domain.MailboxesPerOrganizationCeiling; config
	// may not import domain, so a mailbox test keeps the two in step.
	MailboxMaxPerOrganizationCeiling = 5000
	// DefaultMailboxMaxZonesPerOrganization bounds the DNS zones Zmail creates for one
	// organization; the provider account limits how many zones it may add.
	DefaultMailboxMaxZonesPerOrganization = 3
	// DefaultMailboxSimulatorDomain is the domain whose addresses transactional mail simulates
	// instead of sending.
	DefaultMailboxSimulatorDomain = "test.zmail.im"
	// DefaultMailboxTransactionalRecipientPerMinute bounds transactional messages of one
	// organization, mode, template, and recipient per minute.
	DefaultMailboxTransactionalRecipientPerMinute = 3
	// DefaultMailboxTransactionalKeyPerSecond bounds transactional messages one API key sends per second.
	DefaultMailboxTransactionalKeyPerSecond = 20
	// MinMailboxSecretEncryptionKeyLength is the shortest MAILBOX_SECRET_ENCRYPTION_KEY accepted.
	MinMailboxSecretEncryptionKeyLength = 32
	// MinMailAppPasswordPepperLength is the shortest MAIL_APP_PASSWORD_PEPPER accepted.
	MinMailAppPasswordPepperLength = 32
	// DefaultMailClientIMAPHost and DefaultMailClientIMAPPort are the IMAP server the console tells
	// members to configure in their mail apps.
	DefaultMailClientIMAPHost = "imap.zmail.im"
	DefaultMailClientIMAPPort = 993
	// DefaultMailClientSMTPHost, DefaultMailClientSMTPPort (implicit TLS), and
	// DefaultMailClientSubmissionPort (STARTTLS) are the SMTP server the console tells members to
	// configure in their mail apps.
	DefaultMailClientSMTPHost       = "smtp.zmail.im"
	DefaultMailClientSMTPPort       = 465
	DefaultMailClientSubmissionPort = 587
)

// MailboxConfig holds optional mailbox starter policy and its mail transport.
type MailboxConfig struct {
	// Domain is the address domain every mailbox belongs to.
	Domain string
	// Provider selects the outbound transport: "log" or "cloudflare".
	Provider            string
	CloudflareAccountID string
	CloudflareAPIToken  string
	RequestTimeout      time.Duration
	// InboundSecret signs deliveries from the mail edge. Empty disables inbound mail.
	InboundSecret string
	// DailySendLimit is the personal lane limit: console sends, replies, API outbound messages, and
	// template tests one organization may attempt per rolling 24 hours.
	DailySendLimit int
	// DailyBroadcastLimit and DailyTransactionalLimit are the broadcast and transactional lane
	// limits; 0 (unset) takes DailySendLimit when resolved.
	DailyBroadcastLimit     int
	DailyTransactionalLimit int
	// NewOrganizationDailySendLimit caps every lane of an organization younger than
	// NewOrganizationPeriod; 0 turns the cap off.
	NewOrganizationDailySendLimit int
	NewOrganizationPeriod         time.Duration
	// PublicURL is the public origin of the Web console, such as https://app.example.com. Broadcast
	// unsubscribe links point at it; empty disables broadcasts.
	PublicURL string
	// CustomDomainsEnabled lets organization owners and admins connect their own domains, into a
	// zone the operator added to the Cloudflare account or into a managed zone Zmail creates. The
	// token covers every zone of the account, so the account is the trust boundary: any
	// organization may claim a domain in a zone the operator added.
	CustomDomainsEnabled bool
	// CloudflareEmailWorker names the Email Worker that routing rules of organization domains
	// deliver to; the same Worker that posts to the inbound route.
	CloudflareEmailWorker string
	// MaxZonesPerOrganization bounds the zones Zmail adds to the Cloudflare account for one
	// organization's apex domains, between 1 and MaxMailboxZonesPerOrganization.
	MaxZonesPerOrganization int
	// MaxPerOrganization bounds the mailboxes one organization may own, between 1 and
	// MailboxMaxPerOrganizationCeiling. Lowering it keeps every existing mailbox.
	MaxPerOrganization int
	// TransactionalEnabled accepts transactional messages; it requires SecretEncryptionKey.
	TransactionalEnabled bool
	// SecretEncryptionKey encrypts queued transactional content and keys idempotency fingerprints.
	SecretEncryptionKey string
	// SimulatorDomain is the domain whose addresses transactional mail simulates.
	SimulatorDomain string
	// TransactionalRecipientPerMinute and TransactionalKeyPerSecond are the transactional rate limits.
	TransactionalRecipientPerMinute int
	TransactionalKeyPerSecond       int
	// AppPasswordPepper keys the hash of every mail client password. Empty turns creating mail
	// client passwords off; it has no default, and changing it invalidates every password.
	AppPasswordPepper string
	// ClientIMAPHost and ClientIMAPPort are the public IMAP server shown to members.
	ClientIMAPHost string
	ClientIMAPPort int
	// ClientSMTPHost, ClientSMTPPort (implicit TLS), and ClientSubmissionPort (STARTTLS) are the
	// public SMTP server shown to members.
	ClientSMTPHost       string
	ClientSMTPPort       int
	ClientSubmissionPort int
}

// Resolved fills the development defaults: outside production an unset domain is
// "mail.localhost" and an unset provider is "log". Production keeps both explicit.
func (m MailboxConfig) Resolved(production bool) MailboxConfig {
	m.Domain = strings.ToLower(strings.TrimSpace(m.Domain))
	m.Provider = strings.ToLower(strings.TrimSpace(m.Provider))
	m.CloudflareAccountID = strings.TrimSpace(m.CloudflareAccountID)
	m.CloudflareAPIToken = strings.TrimSpace(m.CloudflareAPIToken)
	m.InboundSecret = strings.TrimSpace(m.InboundSecret)
	m.PublicURL = strings.TrimRight(strings.TrimSpace(m.PublicURL), "/")
	m.CloudflareEmailWorker = strings.TrimSpace(m.CloudflareEmailWorker)
	m.SecretEncryptionKey = strings.TrimSpace(m.SecretEncryptionKey)
	m.AppPasswordPepper = strings.TrimSpace(m.AppPasswordPepper)
	m.ClientIMAPHost = strings.ToLower(strings.TrimSpace(m.ClientIMAPHost))
	if m.ClientIMAPHost == "" {
		m.ClientIMAPHost = DefaultMailClientIMAPHost
	}
	if m.ClientIMAPPort == 0 {
		m.ClientIMAPPort = DefaultMailClientIMAPPort
	}
	m.ClientSMTPHost = strings.ToLower(strings.TrimSpace(m.ClientSMTPHost))
	if m.ClientSMTPHost == "" {
		m.ClientSMTPHost = DefaultMailClientSMTPHost
	}
	if m.ClientSMTPPort == 0 {
		m.ClientSMTPPort = DefaultMailClientSMTPPort
	}
	if m.ClientSubmissionPort == 0 {
		m.ClientSubmissionPort = DefaultMailClientSubmissionPort
	}
	m.SimulatorDomain = strings.ToLower(strings.TrimSpace(m.SimulatorDomain))
	if m.SimulatorDomain == "" {
		m.SimulatorDomain = DefaultMailboxSimulatorDomain
	}
	if m.TransactionalRecipientPerMinute == 0 {
		m.TransactionalRecipientPerMinute = DefaultMailboxTransactionalRecipientPerMinute
	}
	if m.TransactionalKeyPerSecond == 0 {
		m.TransactionalKeyPerSecond = DefaultMailboxTransactionalKeyPerSecond
	}
	if m.DailyBroadcastLimit == 0 {
		m.DailyBroadcastLimit = m.DailySendLimit
	}
	if m.DailyTransactionalLimit == 0 {
		m.DailyTransactionalLimit = m.DailySendLimit
	}
	if !production {
		if m.Domain == "" {
			m.Domain = DefaultMailboxDomain
		}
		if m.Provider == "" {
			m.Provider = MailboxProviderLog
		}
	}
	return m
}

var mailboxWorkerPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidMailboxDomain reports whether name is an accepted lowercase mailbox domain.
func ValidMailboxDomain(name string) bool {
	return len(name) <= 253 && mailboxDomainPattern.MatchString(name)
}

var mailboxDomainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$|^[a-z0-9-]+\.localhost$`)

func validateMailboxConfig(mailbox MailboxConfig, selected, production bool) error {
	if !selected {
		return nil
	}
	resolved := mailbox.Resolved(production)
	if resolved.Domain == "" {
		return fmt.Errorf("MAILBOX_DOMAIN must be set in production when the mailbox starter is selected")
	}
	if len(resolved.Domain) > 253 || !mailboxDomainPattern.MatchString(resolved.Domain) {
		return fmt.Errorf("MAILBOX_DOMAIN must be a valid lowercase domain name")
	}
	switch resolved.Provider {
	case MailboxProviderCloudflare:
		if resolved.CloudflareAccountID == "" || resolved.CloudflareAPIToken == "" {
			return fmt.Errorf("MAILBOX_CLOUDFLARE_ACCOUNT_ID and MAILBOX_CLOUDFLARE_API_TOKEN are required when MAILBOX_PROVIDER is cloudflare")
		}
	case MailboxProviderLog:
		if production {
			return fmt.Errorf("MAILBOX_PROVIDER must be cloudflare in production: the log provider does not deliver mail")
		}
	default:
		return fmt.Errorf("MAILBOX_PROVIDER must be %q or %q", MailboxProviderLog, MailboxProviderCloudflare)
	}
	if resolved.RequestTimeout <= 0 || resolved.RequestTimeout > 60*time.Second {
		return fmt.Errorf("MAILBOX_REQUEST_TIMEOUT must be greater than 0 and no more than 60s")
	}
	if resolved.InboundSecret != "" && len(resolved.InboundSecret) < 32 {
		return fmt.Errorf("MAILBOX_INBOUND_SECRET must be at least 32 characters when set")
	}
	if resolved.AppPasswordPepper != "" && len(resolved.AppPasswordPepper) < MinMailAppPasswordPepperLength {
		return fmt.Errorf("MAIL_APP_PASSWORD_PEPPER must be at least %d characters when set", MinMailAppPasswordPepperLength)
	}
	if strings.ContainsAny(resolved.ClientIMAPHost, " /:@") || len(resolved.ClientIMAPHost) > 253 {
		return fmt.Errorf("MAIL_GATEWAY_PUBLIC_IMAP_HOST must be a host name")
	}
	if resolved.ClientIMAPPort < 1 || resolved.ClientIMAPPort > 65535 {
		return fmt.Errorf("MAIL_GATEWAY_PUBLIC_IMAP_PORT must be between 1 and 65535")
	}
	if strings.ContainsAny(resolved.ClientSMTPHost, " /:@") || len(resolved.ClientSMTPHost) > 253 {
		return fmt.Errorf("MAIL_GATEWAY_PUBLIC_SMTP_HOST must be a host name")
	}
	if resolved.ClientSMTPPort < 1 || resolved.ClientSMTPPort > 65535 {
		return fmt.Errorf("MAIL_GATEWAY_PUBLIC_SMTP_PORT must be between 1 and 65535")
	}
	if resolved.ClientSubmissionPort < 1 || resolved.ClientSubmissionPort > 65535 {
		return fmt.Errorf("MAIL_GATEWAY_PUBLIC_SUBMISSION_PORT must be between 1 and 65535")
	}
	if resolved.DailySendLimit <= 0 || resolved.DailySendLimit > 100000 {
		return fmt.Errorf("MAILBOX_DAILY_SEND_LIMIT must be between 1 and 100000")
	}
	if resolved.DailyBroadcastLimit <= 0 || resolved.DailyBroadcastLimit > 100000 {
		return fmt.Errorf("MAILBOX_DAILY_BROADCAST_LIMIT must be between 1 and 100000, or unset to take MAILBOX_DAILY_SEND_LIMIT")
	}
	if resolved.DailyTransactionalLimit <= 0 || resolved.DailyTransactionalLimit > 100000 {
		return fmt.Errorf("MAILBOX_DAILY_TRANSACTIONAL_LIMIT must be between 1 and 100000, or unset to take MAILBOX_DAILY_SEND_LIMIT")
	}
	if resolved.NewOrganizationDailySendLimit < 0 || resolved.NewOrganizationDailySendLimit > 100000 {
		return fmt.Errorf("MAILBOX_NEW_ORGANIZATION_DAILY_SEND_LIMIT must be between 0 and 100000")
	}
	if resolved.NewOrganizationDailySendLimit > 0 && (resolved.NewOrganizationPeriod <= 0 || resolved.NewOrganizationPeriod > 366*24*time.Hour) {
		return fmt.Errorf("MAILBOX_NEW_ORGANIZATION_PERIOD must be greater than 0 and no more than 8784h")
	}
	if resolved.MaxPerOrganization < 1 || resolved.MaxPerOrganization > MailboxMaxPerOrganizationCeiling {
		return fmt.Errorf("MAILBOX_MAX_PER_ORGANIZATION must be between 1 and %d", MailboxMaxPerOrganizationCeiling)
	}
	if err := validateMailboxTransactional(resolved); err != nil {
		return err
	}
	if resolved.PublicURL != "" {
		parsed, err := url.Parse(resolved.PublicURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Scheme != "https" && (production || parsed.Scheme != "http")) {
			return fmt.Errorf("MAILBOX_PUBLIC_URL must be an https URL without credentials, query, or fragment")
		}
	}
	if resolved.CustomDomainsEnabled {
		if resolved.Provider != MailboxProviderCloudflare {
			return fmt.Errorf("MAILBOX_CUSTOM_DOMAINS_ENABLED requires MAILBOX_PROVIDER cloudflare")
		}
		if !mailboxWorkerPattern.MatchString(resolved.CloudflareEmailWorker) {
			return fmt.Errorf("MAILBOX_CLOUDFLARE_EMAIL_WORKER must name the Email Worker when MAILBOX_CUSTOM_DOMAINS_ENABLED is true")
		}
		if resolved.MaxZonesPerOrganization < 1 || resolved.MaxZonesPerOrganization > MaxMailboxZonesPerOrganization {
			return fmt.Errorf("MAILBOX_MAX_ZONES_PER_ORGANIZATION must be between 1 and %d", MaxMailboxZonesPerOrganization)
		}
	}
	return nil
}

// validateMailboxTransactional checks the transactional mail settings of a resolved config.
func validateMailboxTransactional(resolved MailboxConfig) error {
	if resolved.TransactionalEnabled && len(resolved.SecretEncryptionKey) < MinMailboxSecretEncryptionKeyLength {
		return fmt.Errorf("MAILBOX_SECRET_ENCRYPTION_KEY must be at least %d characters when MAILBOX_TRANSACTIONAL_ENABLED is true",
			MinMailboxSecretEncryptionKeyLength)
	}
	if !ValidMailboxDomain(resolved.SimulatorDomain) {
		return fmt.Errorf("MAILBOX_SIMULATOR_DOMAIN must be a valid lowercase domain name")
	}
	if resolved.TransactionalRecipientPerMinute < 1 || resolved.TransactionalRecipientPerMinute > 1000 {
		return fmt.Errorf("MAILBOX_TRANSACTIONAL_RECIPIENT_PER_MINUTE must be between 1 and 1000")
	}
	if resolved.TransactionalKeyPerSecond < 1 || resolved.TransactionalKeyPerSecond > 10000 {
		return fmt.Errorf("MAILBOX_TRANSACTIONAL_KEY_PER_SECOND must be between 1 and 10000")
	}
	return nil
}
