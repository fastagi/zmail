package mailbox

import (
	"slices"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

// Settings is the resolved policy the service runs with.
type Settings struct {
	Domain string
	// PublicDomains lists shared public domains eligible for self-serve mailbox claims.
	PublicDomains           []string
	PublicLimitPerUser      int
	PublicMinLocalLen       int
	PublicDailySendLimitNew int
	PublicDailySendLimit    int
	PublicMaxRecipientsNew  int
	PublicMaxRecipients     int
	// Provider names the outbound transport, for display only.
	Provider         string
	InboundSecret    string
	InboundTolerance time.Duration
	// DailySendLimit is the personal lane limit: console sends, replies, API outbound messages, and
	// template tests one organization may attempt per rolling 24 hours.
	DailySendLimit int
	// DailyBroadcastLimit is the broadcast lane limit; 0 takes DailySendLimit.
	DailyBroadcastLimit int
	// DailyTransactionalLimit is the transactional lane limit; 0 takes DailySendLimit.
	DailyTransactionalLimit int
	// NewOrganizationDailySendLimit caps every lane of an organization younger than
	// NewOrganizationPeriod; 0 turns the cap off.
	NewOrganizationDailySendLimit int
	NewOrganizationPeriod         time.Duration
	// PublicURL is the public Web origin unsubscribe links point at; empty disables broadcasts.
	PublicURL string
	// CustomDomainsEnabled lets organizations connect their own domains.
	CustomDomainsEnabled bool
	// EmailWorker names the Email Worker routing rules of organization domains deliver to.
	EmailWorker string
	// MaxZonesPerOrganization bounds the managed zones one organization may have Zmail create.
	MaxZonesPerOrganization int
	// MaxMailboxes bounds the mailboxes one organization may own; 0 takes the default.
	MaxMailboxes int
	// TransactionalEnabled accepts transactional messages; SecretEncryptionKey must be set too.
	TransactionalEnabled bool
	// SecretEncryptionKey encrypts queued transactional content and keys idempotency fingerprints.
	SecretEncryptionKey string
	// SimulatorDomain is the domain whose addresses transactional mail simulates; empty takes
	// the default.
	SimulatorDomain string
	// TransactionalRecipientPerMinute and TransactionalKeyPerSecond are the transactional rate
	// limits; 0 takes the defaults.
	TransactionalRecipientPerMinute int
	TransactionalKeyPerSecond       int
	// Triage selects the AI triage model and its instance cap; the zero value is disabled.
	Triage config.AITriageConfig
	// ExternalMail enables read-only external mailboxes; the zero value is off.
	ExternalMail config.ExternalMailConfig
	// MailAgent bounds agent feed retention and agent reads; the zero value takes the defaults.
	MailAgent config.MailAgentConfig
	// AppPasswordPepper keys the hash of mail client passwords; empty refuses creating them.
	AppPasswordPepper string
	// ClientIMAPHost and ClientIMAPPort are the public IMAP server the console shows members.
	ClientIMAPHost string
	ClientIMAPPort int
	// ClientSMTPHost, ClientSMTPPort (implicit TLS), and ClientSubmissionPort (STARTTLS) are the
	// public SMTP server the console shows members.
	ClientSMTPHost       string
	ClientSMTPPort       int
	ClientSubmissionPort int
}

// SettingsFromConfig resolves the service policy from typed configuration.
func SettingsFromConfig(cfg *config.Config) Settings {
	settings := Settings{
		Domain:                  config.DefaultMailboxDomain,
		PublicDomains:           []string{config.DefaultMailboxDomain},
		PublicLimitPerUser:      config.DefaultMailboxPublicLimitPerUser,
		PublicMinLocalLen:       config.DefaultMailboxPublicMinLocalLen,
		PublicDailySendLimitNew: config.DefaultMailboxPublicDailySendLimitNew,
		PublicDailySendLimit:    config.DefaultMailboxPublicDailySendLimit,
		PublicMaxRecipientsNew:  config.DefaultMailboxPublicMaxRecipientsNew,
		PublicMaxRecipients:     config.DefaultMailboxPublicMaxRecipients,
		Provider:                config.MailboxProviderLog,
		InboundTolerance:        config.DefaultMailboxInboundTolerance,
		DailySendLimit:          config.DefaultMailboxDailySendLimit,

		MaxZonesPerOrganization: config.DefaultMailboxMaxZonesPerOrganization,
		MaxMailboxes:            config.DefaultMailboxMaxPerOrganization,
	}
	if cfg == nil {
		return settings
	}
	resolved := cfg.Mailbox.Resolved(cfg.IsProduction())
	settings.Domain = resolved.Domain
	settings.PublicDomains = slices.Clone(resolved.PublicDomains)
	settings.PublicLimitPerUser = resolved.PublicLimitPerUser
	settings.PublicMinLocalLen = resolved.PublicMinLocalLen
	settings.PublicDailySendLimitNew = resolved.PublicDailySendLimitNew
	settings.PublicDailySendLimit = resolved.PublicDailySendLimit
	settings.PublicMaxRecipientsNew = resolved.PublicMaxRecipientsNew
	settings.PublicMaxRecipients = resolved.PublicMaxRecipients
	settings.Provider = resolved.Provider
	settings.InboundSecret = resolved.InboundSecret
	settings.PublicURL = resolved.PublicURL
	settings.CustomDomainsEnabled = resolved.CustomDomainsEnabled
	settings.EmailWorker = resolved.CloudflareEmailWorker
	if resolved.DailySendLimit > 0 {
		settings.DailySendLimit = resolved.DailySendLimit
	}
	settings.DailyBroadcastLimit = resolved.DailyBroadcastLimit
	settings.DailyTransactionalLimit = resolved.DailyTransactionalLimit
	settings.NewOrganizationDailySendLimit = resolved.NewOrganizationDailySendLimit
	settings.NewOrganizationPeriod = resolved.NewOrganizationPeriod
	if resolved.MaxZonesPerOrganization > 0 {
		settings.MaxZonesPerOrganization = resolved.MaxZonesPerOrganization
	}
	if resolved.MaxPerOrganization > 0 {
		settings.MaxMailboxes = resolved.MaxPerOrganization
	}
	settings.TransactionalEnabled = resolved.TransactionalEnabled
	settings.SecretEncryptionKey = resolved.SecretEncryptionKey
	settings.SimulatorDomain = resolved.SimulatorDomain
	settings.TransactionalRecipientPerMinute = resolved.TransactionalRecipientPerMinute
	settings.TransactionalKeyPerSecond = resolved.TransactionalKeyPerSecond
	settings.Triage = cfg.AITriage
	settings.ExternalMail = cfg.ExternalMail
	settings.MailAgent = cfg.MailAgent
	settings.AppPasswordPepper = resolved.AppPasswordPepper
	settings.ClientIMAPHost = resolved.ClientIMAPHost
	settings.ClientIMAPPort = resolved.ClientIMAPPort
	settings.ClientSMTPHost = resolved.ClientSMTPHost
	settings.ClientSMTPPort = resolved.ClientSMTPPort
	settings.ClientSubmissionPort = resolved.ClientSubmissionPort
	return settings
}

func normalizeSettingDomain(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// publicDomains returns the normalized, deduplicated public domains configured for self-claims,
// falling back to [Domain] when PublicDomains is empty and Domain is set.
func (settings Settings) publicDomains() []string {
	domains := make([]string, 0, max(len(settings.PublicDomains), 1))
	for _, raw := range settings.PublicDomains {
		name := normalizeSettingDomain(raw)
		if name != "" && !slices.Contains(domains, name) {
			domains = append(domains, name)
		}
	}
	if len(domains) == 0 {
		if primary := normalizeSettingDomain(settings.Domain); primary != "" {
			domains = append(domains, primary)
		}
	}
	return domains
}

// sharedDomains returns the primary shared domain followed by any additional public domains.
func (settings Settings) sharedDomains() []string {
	domains := make([]string, 0, len(settings.PublicDomains)+1)
	if primary := normalizeSettingDomain(settings.Domain); primary != "" {
		domains = append(domains, primary)
	}
	for _, raw := range settings.PublicDomains {
		name := normalizeSettingDomain(raw)
		if name != "" && !slices.Contains(domains, name) {
			domains = append(domains, name)
		}
	}
	return domains
}

// isSharedDomain reports whether name matches the primary shared domain or any public domain.
func (settings Settings) isSharedDomain(name string) bool {
	normalized := normalizeSettingDomain(name)
	if normalized == "" {
		return false
	}
	return slices.Contains(settings.sharedDomains(), normalized)
}

func (settings Settings) publicLimitPerUser() int {
	if settings.PublicLimitPerUser > 0 {
		return settings.PublicLimitPerUser
	}
	return config.DefaultMailboxPublicLimitPerUser
}

func (settings Settings) publicMinLocalLen() int {
	if settings.PublicMinLocalLen > 0 {
		return settings.PublicMinLocalLen
	}
	return config.DefaultMailboxPublicMinLocalLen
}

func (settings Settings) publicDailySendLimitNew() int {
	if settings.PublicDailySendLimitNew > 0 {
		return settings.PublicDailySendLimitNew
	}
	return config.DefaultMailboxPublicDailySendLimitNew
}

func (settings Settings) publicDailySendLimit() int {
	if settings.PublicDailySendLimit > 0 {
		return settings.PublicDailySendLimit
	}
	return config.DefaultMailboxPublicDailySendLimit
}

func (settings Settings) publicMaxRecipientsNew() int {
	if settings.PublicMaxRecipientsNew > 0 {
		return settings.PublicMaxRecipientsNew
	}
	return config.DefaultMailboxPublicMaxRecipientsNew
}

func (settings Settings) publicMaxRecipients() int {
	if settings.PublicMaxRecipients > 0 {
		return settings.PublicMaxRecipients
	}
	return config.DefaultMailboxPublicMaxRecipients
}

func (s *service) isSharedDomain(name string) bool {
	return s.settings.isSharedDomain(name)
}

func (s *service) sharedDomains() []string {
	return s.settings.sharedDomains()
}
