package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Defaults and bounds of push subscriptions. A notification only marks a connection due; the poll
// stays the safety net, slowed to PollInterval while a live subscription exists.
const (
	// DefaultExternalMailPushPollInterval is the poll of a connection with a live subscription.
	DefaultExternalMailPushPollInterval = 15 * time.Minute
	MinExternalMailPushPollInterval     = 2 * time.Minute
	MaxExternalMailPushPollInterval     = time.Hour
	// DefaultExternalMailPushCoalesceWindow is the least time between two push-triggered syncs of
	// one connection, however many notifications arrive.
	DefaultExternalMailPushCoalesceWindow = 10 * time.Second
	MinExternalMailPushCoalesceWindow     = time.Second
	MaxExternalMailPushCoalesceWindow     = 5 * time.Minute
	// DefaultExternalMailPushQuietLimit is how long a live subscription may go without any
	// notification and still count as working; after it the connection polls at the regular interval.
	DefaultExternalMailPushQuietLimit = 24 * time.Hour
	MinExternalMailPushQuietLimit     = time.Hour
	MaxExternalMailPushQuietLimit     = 7 * 24 * time.Hour
	// DefaultExternalMailPushMaxPerMinute bounds the notification requests of one provider each
	// replica accepts per minute.
	DefaultExternalMailPushMaxPerMinute = 6000
	MaxExternalMailPushMaxPerMinute     = 600000
	// DefaultExternalMailGmailPushJWKSURL is where Google publishes the keys that sign the OIDC
	// tokens of Pub/Sub push requests.
	DefaultExternalMailGmailPushJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
)

// gmailPushTopicPattern is a Cloud Pub/Sub topic resource name.
var gmailPushTopicPattern = regexp.MustCompile(`^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/topics/[A-Za-z][A-Za-z0-9._~+%_-]{2,254}$`)

// ExternalMailPushConfig turns push subscriptions on. Off by default: no subscription is created and
// the notification routes answer as an unavailable feature.
type ExternalMailPushConfig struct {
	Enabled bool
	// PollInterval is the poll of a connection with a live subscription; zero takes the default.
	PollInterval time.Duration
	// CoalesceWindow is the least time between two push-triggered syncs of one connection.
	CoalesceWindow time.Duration
	// QuietLimit is how long a subscription without notifications still counts as working.
	QuietLimit time.Duration
	// MaxPerMinute bounds each provider's authenticated notification requests per minute.
	MaxPerMinute int
	// GmailTopic is the Pub/Sub topic users.watch publishes to; empty leaves Gmail without push.
	GmailTopic string
	// GmailAudience is the audience the push subscription's OIDC token carries.
	GmailAudience string
	// GmailServiceAccount, when set, is the only service account whose tokens are accepted.
	GmailServiceAccount string
	// GmailJWKSURL is where the signing keys are read; empty takes Google's.
	GmailJWKSURL string
	// OutlookNotificationURL is the public URL of the Outlook notification route; empty leaves
	// Outlook without push.
	OutlookNotificationURL string
	// invalid names keys set to a value that does not parse.
	invalid []string
}

// ExternalMailPushSettings are the raw values of the push keys; empty means unset.
type ExternalMailPushSettings struct {
	Enabled                bool
	PollInterval           string
	CoalesceWindow         string
	QuietLimit             string
	MaxPerMinute           string
	GmailTopic             string
	GmailAudience          string
	GmailServiceAccount    string
	GmailJWKSURL           string
	OutlookNotificationURL string
}

// parseExternalMailPushConfig parses the push keys; a key that does not parse is refused later.
func parseExternalMailPushConfig(raw ExternalMailPushSettings) ExternalMailPushConfig {
	c := ExternalMailPushConfig{
		Enabled:                raw.Enabled,
		GmailTopic:             strings.TrimSpace(raw.GmailTopic),
		GmailAudience:          strings.TrimSpace(raw.GmailAudience),
		GmailServiceAccount:    strings.ToLower(strings.TrimSpace(raw.GmailServiceAccount)),
		GmailJWKSURL:           strings.TrimSpace(raw.GmailJWKSURL),
		OutlookNotificationURL: strings.TrimSpace(raw.OutlookNotificationURL),
	}
	for _, field := range []struct {
		key, value string
		into       *time.Duration
	}{
		{"EXTERNAL_MAIL_PUSH_POLL_INTERVAL", raw.PollInterval, &c.PollInterval},
		{"EXTERNAL_MAIL_PUSH_COALESCE_WINDOW", raw.CoalesceWindow, &c.CoalesceWindow},
		{"EXTERNAL_MAIL_PUSH_QUIET_LIMIT", raw.QuietLimit, &c.QuietLimit},
	} {
		value := strings.TrimSpace(field.value)
		if value == "" {
			continue
		}
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			c.invalid = append(c.invalid, field.key)
			continue
		}
		*field.into = parsed
	}
	if value := strings.TrimSpace(raw.MaxPerMinute); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			c.invalid = append(c.invalid, "EXTERNAL_MAIL_PUSH_MAX_PER_MINUTE")
		} else {
			c.MaxPerMinute = parsed
		}
	}
	return c
}

// Poll returns the poll interval of a connection with a live subscription, or its default.
func (c ExternalMailPushConfig) Poll() time.Duration {
	if c.PollInterval <= 0 {
		return DefaultExternalMailPushPollInterval
	}
	return c.PollInterval
}

// Coalesce returns the coalescing window, or its default.
func (c ExternalMailPushConfig) Coalesce() time.Duration {
	if c.CoalesceWindow <= 0 {
		return DefaultExternalMailPushCoalesceWindow
	}
	return c.CoalesceWindow
}

// Quiet returns the quiet limit, or its default.
func (c ExternalMailPushConfig) Quiet() time.Duration {
	if c.QuietLimit <= 0 {
		return DefaultExternalMailPushQuietLimit
	}
	return c.QuietLimit
}

// RequestsPerMinute returns the per-provider request bound, or its default.
func (c ExternalMailPushConfig) RequestsPerMinute() int {
	if c.MaxPerMinute <= 0 {
		return DefaultExternalMailPushMaxPerMinute
	}
	return c.MaxPerMinute
}

// JWKSURL returns the Gmail signing keys URL, or Google's.
func (c ExternalMailPushConfig) JWKSURL() string {
	if c.GmailJWKSURL == "" {
		return DefaultExternalMailGmailPushJWKSURL
	}
	return c.GmailJWKSURL
}

// GmailConfigured reports whether Gmail push has its topic and audience.
func (c ExternalMailPushConfig) GmailConfigured() bool {
	return c.Enabled && c.GmailTopic != "" && c.GmailAudience != "" && c.GmailServiceAccount != ""
}

// OutlookConfigured reports whether Outlook push has its notification URL.
func (c ExternalMailPushConfig) OutlookConfigured() bool {
	return c.Enabled && c.OutlookNotificationURL != ""
}

// validateExternalMailPushConfig refuses values outside their bounds and malformed provider
// settings. A provider without its settings simply runs without push.
func validateExternalMailPushConfig(c ExternalMailPushConfig, production bool) error {
	if len(c.invalid) > 0 {
		return fmt.Errorf("%s must be a positive number or a duration such as 15m", strings.Join(c.invalid, ", "))
	}
	if c.PollInterval != 0 && (c.PollInterval < MinExternalMailPushPollInterval || c.PollInterval > MaxExternalMailPushPollInterval) {
		return fmt.Errorf("EXTERNAL_MAIL_PUSH_POLL_INTERVAL must be between %s and %s", MinExternalMailPushPollInterval, MaxExternalMailPushPollInterval)
	}
	if c.CoalesceWindow != 0 && (c.CoalesceWindow < MinExternalMailPushCoalesceWindow || c.CoalesceWindow > MaxExternalMailPushCoalesceWindow) {
		return fmt.Errorf("EXTERNAL_MAIL_PUSH_COALESCE_WINDOW must be between %s and %s", MinExternalMailPushCoalesceWindow, MaxExternalMailPushCoalesceWindow)
	}
	if c.QuietLimit != 0 && (c.QuietLimit < MinExternalMailPushQuietLimit || c.QuietLimit > MaxExternalMailPushQuietLimit) {
		return fmt.Errorf("EXTERNAL_MAIL_PUSH_QUIET_LIMIT must be between %s and %s", MinExternalMailPushQuietLimit, MaxExternalMailPushQuietLimit)
	}
	if c.MaxPerMinute < 0 || c.MaxPerMinute > MaxExternalMailPushMaxPerMinute {
		return fmt.Errorf("EXTERNAL_MAIL_PUSH_MAX_PER_MINUTE must be between 1 and %d", MaxExternalMailPushMaxPerMinute)
	}
	if c.GmailTopic != "" && !gmailPushTopicPattern.MatchString(c.GmailTopic) {
		return fmt.Errorf("EXTERNAL_MAIL_GMAIL_PUSH_TOPIC must be a Pub/Sub topic name such as projects/my-project/topics/zmail-gmail")
	}
	if (c.GmailTopic == "") != (c.GmailAudience == "") {
		return fmt.Errorf("EXTERNAL_MAIL_GMAIL_PUSH_TOPIC and EXTERNAL_MAIL_GMAIL_PUSH_AUDIENCE are set together")
	}
	if len(c.GmailAudience) > 512 || len(c.GmailServiceAccount) > 320 {
		return fmt.Errorf("EXTERNAL_MAIL_GMAIL_PUSH_AUDIENCE or EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT is too long")
	}
	if c.GmailTopic != "" && c.GmailServiceAccount == "" {
		// Without it any Google-signed token for the audience would be accepted.
		return fmt.Errorf("EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT is required when EXTERNAL_MAIL_GMAIL_PUSH_TOPIC is set")
	}
	if c.GmailServiceAccount != "" && !strings.Contains(c.GmailServiceAccount, "@") {
		return fmt.Errorf("EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT must be a service account email")
	}
	for key, value := range map[string]string{
		"EXTERNAL_MAIL_GMAIL_PUSH_JWKS_URL":           c.GmailJWKSURL,
		"EXTERNAL_MAIL_OUTLOOK_PUSH_NOTIFICATION_URL": c.OutlookNotificationURL,
	} {
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
			(parsed.Scheme != "https" && (parsed.Scheme != "http" || production)) {
			return fmt.Errorf("%s must be an absolute https URL (http only outside production)", key)
		}
	}
	return nil
}
