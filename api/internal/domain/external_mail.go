package domain

import (
	"context"
	"regexp"
	"time"
)

// MailboxTypeExternal mailboxes hold read-only copies of an outside account's mail, synced through
// an external mail connection. Only the member who bound the account, the owner, reads them, and
// nobody sends from them. They are never created through the mailbox API.
const MailboxTypeExternal MailboxType = "external"

// connectorIDPattern is the shape of a connector id. The schema checks the same pattern, so adding
// a connector needs code only.
var connectorIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,15}$`)

// MailSource says where a stored message came from: zmail, or the id of the connector that synced
// the copy.
type MailSource string

const (
	// MailSourceZmail is mail Zmail itself received or sent; the default.
	MailSourceZmail MailSource = "zmail"
	// MailSourceGmail is a copy synced from a Gmail account.
	MailSourceGmail MailSource = "gmail"
	// MailSourceOutlook is a copy synced from an Outlook or Microsoft 365 account.
	MailSourceOutlook MailSource = "outlook"
)

// Valid reports whether the source is zmail or has the shape of a connector id.
func (s MailSource) Valid() bool {
	return s == MailSourceZmail || connectorIDPattern.MatchString(string(s))
}

// ExternalMailProvider is the id of the connector that reads a connection's outside account. The
// known ids are constants; any id of the connector shape is valid, so the set grows with code.
type ExternalMailProvider string

const (
	ExternalMailProviderGmail   ExternalMailProvider = "gmail"
	ExternalMailProviderOutlook ExternalMailProvider = "outlook"
)

// Valid reports whether the provider has the shape of a connector id. "zmail" is reserved for
// mail Zmail itself handles.
func (p ExternalMailProvider) Valid() bool {
	return p != ExternalMailProvider(MailSourceZmail) && connectorIDPattern.MatchString(string(p))
}

// Source is the message source of mail synced from the provider.
func (p ExternalMailProvider) Source() MailSource {
	return MailSource(p)
}

// ExternalMailStatus is the sync state of a connection.
type ExternalMailStatus string

const (
	// ExternalMailStatusActive connections are polled.
	ExternalMailStatusActive ExternalMailStatus = "active"
	// ExternalMailStatusNeedsReconnect connections lost their authorization; polling stops until
	// the owner binds the account again.
	ExternalMailStatusNeedsReconnect ExternalMailStatus = "needs_reconnect"
	// ExternalMailStatusRevoked connections were unbound; they are never polled again.
	ExternalMailStatusRevoked ExternalMailStatus = "revoked"
)

// Error codes stored on a connection after a failed sync.
const (
	ExternalMailErrorAuthFailed  = "external.auth_failed"
	ExternalMailErrorFetchFailed = "external.fetch_failed"
	// ExternalMailErrorProviderUnavailable means the instance no longer runs the connection's
	// connector; the connection keeps its state until it is enabled again.
	ExternalMailErrorProviderUnavailable = "external.provider_unavailable"
)

// ExternalMailAuthorizationTTL is how long a started sign-in may be completed.
const ExternalMailAuthorizationTTL = 10 * time.Minute

// MaxExternalMailBackfillDays bounds how far back the first sync of a connection may read.
const MaxExternalMailBackfillDays = 90

// ExternalMailAuthorization is one pending sign-in to an outside account. It is completed once,
// by the member who started it, before it expires.
type ExternalMailAuthorization struct {
	ID             uint
	OrganizationID uint
	UserID         uint
	Provider       ExternalMailProvider
	// StateHash is the hex SHA-256 of the single-use state; the state itself is never stored.
	StateHash string
	// SealedVerifier is the encrypted PKCE code verifier.
	SealedVerifier string
	// ConnectionID is the connection a reconnect restores; nil for a new binding.
	ConnectionID *uint
	BackfillDays int
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

// ExternalMailRebind restores a connection with a fresh grant.
type ExternalMailRebind struct {
	ConnectionID          uint
	EncryptedRefreshToken string
	// ResetCursor starts a fresh backfill, used when the connection had been revoked.
	ResetCursor bool
	Now         time.Time
}

// DefaultExternalMailBackfillDays is how far back the first sync of a connection reads.
const DefaultExternalMailBackfillDays = 14

// MaxExternalMailConnectionsPerUser bounds the connections one member may bind in an organization.
const MaxExternalMailConnectionsPerUser = 10

// ExternalMailConnection binds one outside account to one external mailbox.
type ExternalMailConnection struct {
	ID             uint
	OrganizationID uint
	// MailboxID is the external mailbox synced mail is stored in.
	MailboxID      uint
	Provider       ExternalMailProvider
	AccountAddress string
	// OwnerUserID is the member who bound the account; nil once that account was deleted.
	OwnerUserID *uint
	Status      ExternalMailStatus
	// LastSyncedAt is when a fetch last succeeded; nil before the first one.
	LastSyncedAt *time.Time
	// LastErrorCode is the code of the latest failed sync; empty after a success.
	LastErrorCode string
	// EncryptedRefreshToken is the sealed refresh token; empty once revoked.
	EncryptedRefreshToken string
	// SyncCursor is the provider's position: a Gmail historyId or a Graph deltaLink.
	SyncCursor   string
	BackfillDays int
	// NextSyncAt is when the connection is next due; a claim pushes it ahead as a lease.
	NextSyncAt time.Time
	// ConsecutiveFailures counts failed fetches in a row; it sets the backoff.
	ConsecutiveFailures int
	// ConsecutiveMore counts pages in a row that reported more; it bounds immediate refetches.
	ConsecutiveMore int
	// RevokedAt is when the connection was revoked; its copies are deleted a retention later.
	RevokedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ExternalMailSyncResult is the outcome of one fetch of one connection.
type ExternalMailSyncResult struct {
	ConnectionID uint
	MailboxID    uint
	// Messages are new inbound copies with Source and ExternalID set; existing ids are skipped.
	Messages      []*MailMessage
	Cursor        string
	Status        ExternalMailStatus
	LastErrorCode string
	// EncryptedRefreshToken replaces the stored grant when the provider rotated it during the
	// fetch; empty keeps the stored one.
	EncryptedRefreshToken string
	// SyncedAt is set when the fetch succeeded.
	SyncedAt   *time.Time
	NextSyncAt time.Time
	Now        time.Time
	// ConsecutiveFailures is the connection's failed fetches in a row after this one.
	ConsecutiveFailures int
	// ConsecutiveMore is the connection's pages in a row that reported more after this one.
	ConsecutiveMore int
	// Run is recorded with the result; its Stored count and a dropped outcome are filled in by
	// the save.
	Run *ExternalMailSyncRun
}

// ExternalMailRepository stores external mail connections and the mail synced through them.
type ExternalMailRepository interface {
	// CreateExternalMailConnection stores a new active connection and fills its id.
	CreateExternalMailConnection(ctx context.Context, connection *ExternalMailConnection) error
	// ClaimExternalMailConnections returns up to claim.Limit active connections due at claim.Now,
	// at most claim.PerProvider of each provider, taken round-robin across providers and oldest
	// first within one, in that order. It pushes each one's next sync to claim.LeaseUntil and
	// skips rows another worker holds.
	ClaimExternalMailConnections(ctx context.Context, claim ExternalMailClaim) ([]*ExternalMailConnection, error)
	// SaveExternalMailSync records a fetch while the connection is still active and stores its new
	// messages once per (mailbox, external id). applied is false, and nothing is stored, when the
	// connection stopped being active meanwhile. The result's run is recorded either way, and the
	// connection's runs beyond MaxExternalMailSyncRuns are pruned.
	// The returned messages are the ones newly stored, with their ids.
	SaveExternalMailSync(ctx context.Context, result ExternalMailSyncResult) (stored []*MailMessage, applied bool, err error)
	// CountLiveExternalMailConnections counts the owner's connections that are not revoked; the
	// per-member limit bounds these.
	CountLiveExternalMailConnections(ctx context.Context, organizationID, ownerUserID uint) (int, error)
	// ListExternalMailConnections returns the owner's connections in the organization, live ones
	// first, then id ascending, at most MaxExternalMailConnectionsPerUser.
	ListExternalMailConnections(ctx context.Context, organizationID, ownerUserID uint) ([]*ExternalMailConnection, error)
	// FindExternalMailConnection returns one connection of the organization.
	FindExternalMailConnection(ctx context.Context, organizationID, id uint) (*ExternalMailConnection, error)
	// RevokeExternalMailConnection marks the connection revoked, clears its token and cursor, and
	// sets revoked_at unless it is already set.
	RevokeExternalMailConnection(ctx context.Context, organizationID, id uint, now time.Time) error
	// FindExternalMailConnectionByAccount returns the organization's connection of the provider
	// and outside address, ignoring case, in any state.
	FindExternalMailConnectionByAccount(ctx context.Context, organizationID uint, provider ExternalMailProvider, address string) (*ExternalMailConnection, error)
	// RebindExternalMailConnection stores a fresh grant, makes the connection active and due now,
	// and clears its error, failures, and revoked_at, which cancels a pending purge.
	RebindExternalMailConnection(ctx context.Context, rebind ExternalMailRebind) (*ExternalMailConnection, error)
	// CreateExternalMailAuthorization stores a pending sign-in and removes expired ones.
	CreateExternalMailAuthorization(ctx context.Context, authorization *ExternalMailAuthorization) error
	// TakeExternalMailAuthorization deletes and returns the unexpired sign-in with the state hash,
	// so it is used at most once.
	TakeExternalMailAuthorization(ctx context.Context, stateHash string, now time.Time) (*ExternalMailAuthorization, error)
	ExternalMailOpsRepository
}

// ExternalMailSyncWorker syncs external mail outside request paths.
type ExternalMailSyncWorker interface {
	// SyncExternalMail fetches up to limit due connections and returns how many it handled.
	SyncExternalMail(ctx context.Context, limit int) (int, error)
}
