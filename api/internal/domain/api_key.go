package domain

import (
	"context"
	"strings"
	"time"
)

// APIKeyMode says whether a key acts for real or only in test. It is fixed when the key is created.
type APIKeyMode string

const (
	// APIKeyModeLive keys act for real. Keys created before modes existed are live.
	APIKeyModeLive APIKeyMode = "live"
	// APIKeyModeTest keys are accepted only by routes that declare test support.
	APIKeyModeTest APIKeyMode = "test"
)

// Valid reports whether the mode is a known mode.
func (m APIKeyMode) Valid() bool {
	return m == APIKeyModeLive || m == APIKeyModeTest
}

// APIKey represents an application API key owned by a user.
type APIKey struct {
	ID         uint       `json:"id"`
	UserID     uint       `json:"user_id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	KeyHash    string     `json:"-"`
	Scopes     []string   `json:"scopes"`
	Mode       APIKeyMode `json:"mode"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	// OrganizationID binds the key to one organization; nil leaves it unrestricted.
	OrganizationID *uint `json:"organization_id,omitempty"`
	// SendingDomains limits the from addresses the key may send from; nil allows every domain.
	SendingDomains []string `json:"sending_domains,omitempty"`
}

// Restriction is what the key's organization and sending-domain binding allow.
func (k *APIKey) Restriction() APIKeyRestriction {
	if k == nil {
		return APIKeyRestriction{}
	}
	restriction := APIKeyRestriction{}
	if k.OrganizationID != nil {
		restriction.OrganizationID = *k.OrganizationID
	}
	if k.SendingDomains != nil {
		restriction.SendingDomains = append([]string{}, k.SendingDomains...)
	}
	return restriction
}

// APIKeyRestriction is the optional binding an API key carries into a request. The zero value
// restricts nothing.
type APIKeyRestriction struct {
	// OrganizationID is the only organization the key acts in; zero for any of its owner's.
	OrganizationID uint
	// SendingDomains are the only from-address domains the key may send from, lowercase; nil for
	// every domain.
	SendingDomains []string
}

// AllowsSendingFrom reports whether the key may send from address. A malformed address is never
// allowed by a domain-restricted key.
func (r APIKeyRestriction) AllowsSendingFrom(address string) bool {
	if r.SendingDomains == nil {
		return true
	}
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return false
	}
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(address[at+1:])), ".")
	for _, allowed := range r.SendingDomains {
		if name != "" && name == allowed {
			return true
		}
	}
	return false
}

type apiKeyRestrictionKey struct{}

// WithAPIKeyRestriction records the authenticated key's binding for the rest of the request.
func WithAPIKeyRestriction(ctx context.Context, value APIKeyRestriction) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, apiKeyRestrictionKey{}, value)
}

// APIKeyRestrictionFromContext returns the binding of the key that authenticated the request. A
// request without one, such as a browser session, is unrestricted.
func APIKeyRestrictionFromContext(ctx context.Context) APIKeyRestriction {
	if ctx == nil {
		return APIKeyRestriction{}
	}
	value, _ := ctx.Value(apiKeyRestrictionKey{}).(APIKeyRestriction)
	return value
}

// SendingDomainDirectory lists the domains a member may restrict an API key to in an
// organization: the domains the organization's mailboxes may send from.
type SendingDomainDirectory interface {
	// SendingDomains returns lowercase domain names, or ErrOrganizationNotFound when userID is not
	// a member of organizationID.
	SendingDomains(ctx context.Context, organizationID, userID uint) ([]string, error)
}

// IsActive returns whether the API key can still be used.
func (k *APIKey) IsActive(now time.Time) bool {
	if k == nil {
		return false
	}
	if k.RevokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && k.ExpiresAt.Before(now) {
		return false
	}
	return true
}

// HasScope checks whether the key grants a required scope.
func (k *APIKey) HasScope(scope string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return true
	}

	for _, candidate := range k.Scopes {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == scope {
			return true
		}
	}
	return false
}

// APIKeyRepository defines the contract for API key persistence.
type APIKeyRepository interface {
	Create(ctx context.Context, key *APIKey) error
	Revoke(ctx context.Context, userID, id uint, revokedAt time.Time) (bool, error)
	RecordUse(ctx context.Context, id uint, usedAt, staleBefore time.Time) error
	FindByUserID(ctx context.Context, userID uint, page, pageSize int) ([]*APIKey, int64, error)
	FindByHash(ctx context.Context, hash string) (*APIKey, error)
}
