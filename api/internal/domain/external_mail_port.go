package domain

import (
	"context"
	"errors"
	"time"
)

// External mail sentinel errors shared across the externalmail and mailbox modules.
var (
	ErrExternalMailConnectionNotFound    = errors.New("external mail connection not found")
	ErrExternalMailAuthorizationNotFound = errors.New("external mail authorization not found")
	ErrExternalMailAuthorizationFailed   = errors.New("external mail authorization failed")
	ErrExternalMailAccountMismatch       = errors.New("external mail account does not match the connection")
	ErrExternalMailAccountTaken          = errors.New("external mail account is bound by another member")
	ErrExternalMailConnectionLimit       = errors.New("external mail connection limit reached")
	ErrExternalMailAuthorizationInvalid  = errors.New("external mail authorization request is invalid")
	ErrExternalMailProviderUnavailable   = errors.New("external mail provider unavailable")
)

// ExternalMailConnectorView is one enabled external provider shown to clients.
type ExternalMailConnectorView struct {
	ID       string
	AuthKind string
	Read     bool
	Send     bool
	Tenant   bool
}

// ExternalMailAuthorizationInput carries parameters to begin an external mail authorization.
type ExternalMailAuthorizationInput struct {
	Provider     ExternalMailProvider
	ConnectionID *uint
	BackfillDays int
	Sharing      *ExternalMailSharing
}

// ExternalMailAuthorizationStart is the browser redirect URL and expiry of an authorization.
type ExternalMailAuthorizationStart struct {
	AuthorizationURL string
	ExpiresAt        time.Time
}

// ExternalMailPort is the hexagonal port exposed to the top-level externalmail module.
type ExternalMailPort interface {
	ListExternalMailConnectorsPort(ctx context.Context) []ExternalMailConnectorView
	StartExternalMailAuthorizationPort(ctx context.Context, viewer OrganizationContext, input ExternalMailAuthorizationInput) (ExternalMailAuthorizationStart, error)
	CompleteExternalMailAuthorizationPort(ctx context.Context, viewer OrganizationContext, state, code string) (*ExternalMailConnection, bool, error)
	ListExternalMailConnectionsPort(ctx context.Context, viewer OrganizationContext) ([]*ExternalMailConnection, error)
	RevokeExternalMailConnectionPort(ctx context.Context, viewer OrganizationContext, id uint) error
}
