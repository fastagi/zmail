package domain

import (
	"context"
	"time"
)

// MailboxTypeExternal mailboxes hold read-only copies of an outside account's mail, synced through
// an external mail connection. Only the member who bound the account, the owner, reads them, and
// nobody sends from them. They are never created through the mailbox API.
const MailboxTypeExternal MailboxType = "external"

// MailSource says where a stored message came from.
type MailSource string

const (
	// MailSourceZmail is mail Zmail itself received or sent; the default.
	MailSourceZmail MailSource = "zmail"
	// MailSourceGmail is a copy synced from a Gmail account.
	MailSourceGmail MailSource = "gmail"
	// MailSourceOutlook is a copy synced from an Outlook or Microsoft 365 account.
	MailSourceOutlook MailSource = "outlook"
)

// Valid reports whether the source is a known value.
func (s MailSource) Valid() bool {
	return s == MailSourceZmail || s == MailSourceGmail || s == MailSourceOutlook
}

// ExternalMailProvider is the outside service of a connection.
type ExternalMailProvider string

const (
	ExternalMailProviderGmail   ExternalMailProvider = "gmail"
	ExternalMailProviderOutlook ExternalMailProvider = "outlook"
)

// Valid reports whether the provider is a known value.
func (p ExternalMailProvider) Valid() bool {
	return p == ExternalMailProviderGmail || p == ExternalMailProviderOutlook
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
)

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
	CreatedAt  time.Time
	UpdatedAt  time.Time
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
	// SyncedAt is set when the fetch succeeded.
	SyncedAt   *time.Time
	NextSyncAt time.Time
	Now        time.Time
}

// ExternalMailRepository stores external mail connections and the mail synced through them.
type ExternalMailRepository interface {
	// CreateExternalMailConnection stores a new active connection and fills its id.
	CreateExternalMailConnection(ctx context.Context, connection *ExternalMailConnection) error
	// ClaimExternalMailConnections returns up to limit active connections due at now, oldest
	// first, and pushes each one's next sync to leaseUntil, skipping rows another worker holds.
	ClaimExternalMailConnections(ctx context.Context, now, leaseUntil time.Time, limit int) ([]*ExternalMailConnection, error)
	// SaveExternalMailSync records a fetch while the connection is still active and stores its new
	// messages once per (mailbox, external id). applied is false, and nothing is stored, when the
	// connection stopped being active meanwhile.
	SaveExternalMailSync(ctx context.Context, result ExternalMailSyncResult) (stored int, applied bool, err error)
	// ListExternalMailConnections returns the owner's connections in the organization, id ascending,
	// at most MaxExternalMailConnectionsPerUser.
	ListExternalMailConnections(ctx context.Context, organizationID, ownerUserID uint) ([]*ExternalMailConnection, error)
	// FindExternalMailConnection returns one connection of the organization.
	FindExternalMailConnection(ctx context.Context, organizationID, id uint) (*ExternalMailConnection, error)
	// RevokeExternalMailConnection marks the connection revoked and clears its token and cursor.
	RevokeExternalMailConnection(ctx context.Context, organizationID, id uint, now time.Time) error
}

// ExternalMailSyncWorker syncs external mail outside request paths.
type ExternalMailSyncWorker interface {
	// SyncExternalMail fetches up to limit due connections and returns how many it handled.
	SyncExternalMail(ctx context.Context, limit int) (int, error)
}
