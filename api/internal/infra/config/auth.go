package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// AuditWriteMode values select when request audit records are written.
const (
	// AuditWriteModeAsync queues request audit records and writes them in batches off the response
	// path; a graceful shutdown drains the queue.
	AuditWriteModeAsync = "async"
	// AuditWriteModeSync writes each request audit record before the response completes.
	AuditWriteModeSync = "sync"
)

// AuditConfig holds audit starter runtime policy.
type AuditConfig struct {
	WriteMode string
}

// AuthenticationConfig owns the server-side user session lifecycle.
type AuthenticationConfig struct {
	SessionTTL           time.Duration
	SessionIdleTimeout   time.Duration
	SessionTouchInterval time.Duration
	SessionRetention     time.Duration
	// RegistrationDisabled closes public self-registration (POST /v1/register). It is set by
	// AUTH_REGISTRATION_ENABLED=false; the zero value keeps registration open.
	RegistrationDisabled bool
	// RegistrationEmailCodeRequired makes POST /v1/register demand a code mailed by
	// POST /v1/register/email-codes. AUTH_REGISTRATION_EMAIL_CODE_REQUIRED defaults to true.
	RegistrationEmailCodeRequired bool
	// TOTPEncryptionKey encrypts authenticator secrets (AUTH_TOTP_ENCRYPTION_KEY); empty turns
	// two-factor enrollment off.
	TOTPEncryptionKey string
	// GoogleClientID and GoogleClientSecret are the OAuth client of "Sign in with Google"
	// (GOOGLE_OAUTH_CLIENT_ID, GOOGLE_OAUTH_CLIENT_SECRET). Both empty turn it off.
	GoogleClientID     string
	GoogleClientSecret string
}

// GoogleSignInConfigured reports whether both halves of the Google OAuth client are set.
func (c AuthenticationConfig) GoogleSignInConfigured() bool {
	return strings.TrimSpace(c.GoogleClientID) != "" && strings.TrimSpace(c.GoogleClientSecret) != ""
}

// OrganizationConfig holds optional organization starter policy.
type OrganizationConfig struct {
	InvitationTTL time.Duration
}

// OperatorConfig configures the optional platform-operator starter and its Admin Console session.
type OperatorConfig struct {
	// AllowedOrigins are the exact browser origins allowed to send unsafe operator requests.
	AllowedOrigins []string
	// SessionCookieName overrides the operator session cookie name.
	SessionCookieName string
}

// WebPushConfig holds the VAPID application server key pair and contact subject of browser push
// notifications. Push is on only when all three are set and the key pair is valid; otherwise it
// is off and the API boots without it.
type WebPushConfig struct {
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	// Subject is the push services' contact for this server: a mailto: or https: URL.
	Subject string
}

// Configured reports whether every web push value is set.
func (c WebPushConfig) Configured() bool {
	return c.VAPIDPublicKey != "" && c.VAPIDPrivateKey != "" && c.Subject != ""
}

// PartiallyConfigured reports whether some, but not all, web push values are set.
func (c WebPushConfig) PartiallyConfigured() bool {
	return !c.Configured() && (c.VAPIDPublicKey != "" || c.VAPIDPrivateKey != "" || c.Subject != "")
}

func validateAuthenticationConfig(cfg AuthenticationConfig) error {
	const (
		minSessionTTL  = 15 * time.Minute
		maxSessionTTL  = 180 * 24 * time.Hour
		minIdleTimeout = 5 * time.Minute
		maxTouch       = time.Hour
		maxRetention   = 365 * 24 * time.Hour
	)
	if key := strings.TrimSpace(cfg.TOTPEncryptionKey); key != "" && len(key) < MinMailboxSecretEncryptionKeyLength {
		return fmt.Errorf("AUTH_TOTP_ENCRYPTION_KEY must be at least %d characters when set", MinMailboxSecretEncryptionKeyLength)
	}
	if (strings.TrimSpace(cfg.GoogleClientID) == "") != (strings.TrimSpace(cfg.GoogleClientSecret) == "") {
		return fmt.Errorf("GOOGLE_OAUTH_CLIENT_ID and GOOGLE_OAUTH_CLIENT_SECRET are set together")
	}
	if len(cfg.GoogleClientID) > 255 || len(cfg.GoogleClientSecret) > 255 {
		return fmt.Errorf("GOOGLE_OAUTH_CLIENT_ID and GOOGLE_OAUTH_CLIENT_SECRET must be at most 255 characters")
	}

	if cfg.SessionTTL < minSessionTTL || cfg.SessionTTL > maxSessionTTL {
		return fmt.Errorf("AUTH_SESSION_TTL must be between %s and %s", minSessionTTL, maxSessionTTL)
	}
	if cfg.SessionIdleTimeout < minIdleTimeout || cfg.SessionIdleTimeout > cfg.SessionTTL {
		return fmt.Errorf("AUTH_SESSION_IDLE_TIMEOUT must be between %s and AUTH_SESSION_TTL", minIdleTimeout)
	}
	if cfg.SessionTouchInterval <= 0 || cfg.SessionTouchInterval > maxTouch || cfg.SessionTouchInterval > cfg.SessionIdleTimeout {
		return fmt.Errorf("AUTH_SESSION_TOUCH_INTERVAL must be greater than 0, no more than %s, and no longer than AUTH_SESSION_IDLE_TIMEOUT", maxTouch)
	}
	if cfg.SessionRetention < 0 || cfg.SessionRetention > maxRetention {
		return fmt.Errorf("AUTH_SESSION_RETENTION must be between 0 and %s", maxRetention)
	}
	return nil
}

func validateOperatorConfig(cfg *Config, selected bool) error {
	operator := cfg.Operator
	if !selected {
		return nil
	}
	if len(operator.AllowedOrigins) == 0 {
		return fmt.Errorf("OPERATOR_ALLOWED_ORIGINS is required when the operator starter is selected")
	}
	for _, raw := range operator.AllowedOrigins {
		origin := strings.TrimSpace(raw)
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil ||
			strings.Contains(origin, "*") || origin != parsed.Scheme+"://"+parsed.Host {
			return fmt.Errorf("OPERATOR_ALLOWED_ORIGINS must list exact scheme://host[:port] origins without wildcards or paths")
		}
		if cfg.IsProduction() && parsed.Scheme != "https" {
			return fmt.Errorf("OPERATOR_ALLOWED_ORIGINS must use https in production")
		}
	}
	name := strings.TrimSpace(operator.SessionCookieName)
	if name != "" && !validCookieName(name) {
		return fmt.Errorf("OPERATOR_SESSION_COOKIE_NAME must be a valid cookie token")
	}
	if strings.HasPrefix(name, "__Host-") && !cfg.IsProduction() {
		return fmt.Errorf("OPERATOR_SESSION_COOKIE_NAME may use the __Host- prefix only in production, where cookies are Secure")
	}
	if name != "" && cfg.IsProduction() && !strings.HasPrefix(name, "__Host-") {
		return fmt.Errorf("OPERATOR_SESSION_COOKIE_NAME must keep the __Host- prefix in production, " +
			"which binds the cookie to the API host over HTTPS")
	}
	return nil
}

func validCookieName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if r <= 0x20 || r >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", r) {
			return false
		}
	}
	return true
}
