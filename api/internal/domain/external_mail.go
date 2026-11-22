package domain

import (
	"context"
	"regexp"
	"time"
)

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
	// Sharing makes a new binding the organization's shared connected mailbox; nil binds the
	// member's own account.
	Sharing      *ExternalMailSharing
	BackfillDays int
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

// ExternalMailSharing is the shared connected mailbox an owner or admin binds a team account into:
// its display name (empty takes the account address) and its first members, as a shared mailbox
// is created with.
type ExternalMailSharing struct {
	DisplayName string                      `json:"display_name,omitempty"`
	Members     []ExternalMailSharingMember `json:"members"`
}

// ExternalMailSharingMember is one first member of a shared connected mailbox.
type ExternalMailSharingMember struct {
	UserID     uint              `json:"user_id"`
	Permission MailboxPermission `json:"permission"`
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

// MaxSharedExternalMailConnectionsCeiling is the highest per-organization limit of live shared
// connections an instance may configure; it also bounds the shared connections one listing returns.
const MaxSharedExternalMailConnectionsCeiling = 100

// ExternalMailConnection binds one outside account to one connected mailbox.
type ExternalMailConnection struct {
	ID             uint
	OrganizationID uint
	// MailboxID is the connected mailbox synced mail is stored in.
	MailboxID      uint
	Provider       ExternalMailProvider
	AccountAddress string
	// OwnerUserID is the member whose own account this is; nil for a shared connection, and once
	// the owner's user account was deleted.
	OwnerUserID *uint
	// Shared marks the organization's connection of a shared connected mailbox: it has no owner,
	// counts against the organization's limit, and outlives whoever bound it.
	Shared bool
	// BoundByUserID records who bound the account; nil once that user account was deleted.
	BoundByUserID *uint
	Status        ExternalMailStatus
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
	// TenantID is the authorized tenant the connection reads through; nil for an account bound with
	// its own grant. A tenant connection stores no per-account token.
	TenantID *uint
	// TenantDirectoryID is the mailbox's stable id in the tenant's directory; empty without a tenant.
	TenantDirectoryID string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ExternalMailTenantStatus is the state of an authorized tenant.
type ExternalMailTenantStatus string

const (
	// ExternalMailTenantActive tenants mint tokens for their assigned mailboxes.
	ExternalMailTenantActive ExternalMailTenantStatus = "active"
	// ExternalMailTenantNeedsReauthorization tenants lost their credential at the provider; every
	// connection of them waits for the owner to authorize the tenant again.
	ExternalMailTenantNeedsReauthorization ExternalMailTenantStatus = "needs_reauthorization"
	// ExternalMailTenantRevoked tenants were revoked by the owner; the row stays for the record.
	ExternalMailTenantRevoked ExternalMailTenantStatus = "revoked"
)

// ExternalMailTenant is a whole Google Workspace or Microsoft 365 tenant an organization owner
// authorized once. It reads nothing by itself: only mailboxes assigned from its directory are synced.
type ExternalMailTenant struct {
	ID             uint
	OrganizationID uint
	Provider       ExternalMailProvider
	// TenantIdentifier is the provider's id of the tenant: the Workspace customer id, or the Entra
	// tenant id. It is live in at most one organization of the instance.
	TenantIdentifier string
	// DisplayName is a label such as the Workspace primary domain; may be empty.
	DisplayName string
	Status      ExternalMailTenantStatus
	// EncryptedCredential is the sealed tenant credential; empty once revoked.
	EncryptedCredential string
	// AuthorizedByUserID is the owner who authorized the tenant last; nil once that user was deleted.
	AuthorizedByUserID *uint
	// LastErrorCode is set while the tenant needs reauthorization.
	LastErrorCode string
	AuthorizedAt  time.Time
	RevokedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ExternalMailErrorTenantAuthFailed is stored on a tenant and its connections when the provider
// refused the tenant credential.
const ExternalMailErrorTenantAuthFailed = "external.tenant_auth_failed"

// MaxExternalMailTenantsPerOrganization bounds the tenants, in any state, one organization lists.
const MaxExternalMailTenantsPerOrganization = 50

// MaxExternalMailTenantAssignmentsCeiling is the highest per-tenant assignment ceiling an instance
// may configure.
const MaxExternalMailTenantAssignmentsCeiling = 10000

// ExternalMailTenantRepository stores authorized tenants.
type ExternalMailTenantRepository interface {
	// CreateExternalMailTenant stores a new active tenant and fills its id.
	CreateExternalMailTenant(ctx context.Context, tenant *ExternalMailTenant) error
	// FindExternalMailTenant returns one tenant of the organization, in any state.
	FindExternalMailTenant(ctx context.Context, organizationID, id uint) (*ExternalMailTenant, error)
	// FindLiveExternalMailTenant returns the tenant of the provider and identifier that is not
	// revoked, in any organization.
	FindLiveExternalMailTenant(ctx context.Context, provider ExternalMailProvider, identifier string) (*ExternalMailTenant, error)
	// ListExternalMailTenants returns the organization's tenants, live ones first, then id ascending.
	ListExternalMailTenants(ctx context.Context, organizationID uint) ([]*ExternalMailTenant, error)
	// ReauthorizeExternalMailTenant stores a fresh credential, makes the tenant active, and returns
	// its connections that lost their authorization with it to active, due now.
	ReauthorizeExternalMailTenant(ctx context.Context, tenantID uint, encryptedCredential, displayName string, authorizedBy uint, now time.Time) (*ExternalMailTenant, error)
	// MarkExternalMailTenantLost moves an active tenant to needs_reauthorization and every active
	// connection of it to needs_reconnect, in one statement each; false when it was not active.
	MarkExternalMailTenantLost(ctx context.Context, tenantID uint, now time.Time) (bool, error)
	// RevokeExternalMailTenant marks the tenant revoked, erases its credential, revokes every live
	// connection of it, and returns those connections as they were before.
	RevokeExternalMailTenant(ctx context.Context, organizationID, tenantID uint, now time.Time) ([]*ExternalMailConnection, error)
	// CountLiveTenantConnections counts the tenant's connections that are not revoked.
	CountLiveTenantConnections(ctx context.Context, tenantID uint) (int, error)
	// ListLiveConnectionsByAccounts returns the organization's connections of the provider that are
	// not revoked and whose address is one of addresses, ignoring case.
	ListLiveConnectionsByAccounts(ctx context.Context, organizationID uint, provider ExternalMailProvider, addresses []string) ([]*ExternalMailConnection, error)
	// ListTenantPersonalConnections returns the owner's tenant-assigned personal connections, live
	// ones first, at most limit.
	ListTenantPersonalConnections(ctx context.Context, organizationID, ownerUserID uint, limit int) ([]*ExternalMailConnection, error)
	// RestoreTenantConnection makes a revoked tenant connection active again with an empty cursor.
	RestoreTenantConnection(ctx context.Context, connectionID, tenantID uint, directoryID string, now time.Time) (*ExternalMailConnection, error)
	// ShareTenantConnection makes a tenant connection the organization's (shared, no owner), as a
	// handover of its personal mailbox does.
	ShareTenantConnection(ctx context.Context, mailboxID uint) error
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
	// LockExternalMailBindings holds the owner's binding lock in the organization until the
	// surrounding transaction ends, so two bindings of one member count their live connections in
	// turn and never both pass the per-member limit.
	LockExternalMailBindings(ctx context.Context, organizationID, ownerUserID uint) error
	// CountLiveExternalMailConnections counts the owner's connections that are not revoked; the
	// per-member limit bounds these.
	CountLiveExternalMailConnections(ctx context.Context, organizationID, ownerUserID uint) (int, error)
	// LockSharedExternalMailBindings holds the organization's shared binding lock until the
	// surrounding transaction ends, so two shared bindings count the live shared connections in turn.
	LockSharedExternalMailBindings(ctx context.Context, organizationID uint) error
	// CountLiveSharedExternalMailConnections counts the organization's shared connections that are
	// not revoked; the per-organization limit bounds these.
	CountLiveSharedExternalMailConnections(ctx context.Context, organizationID uint) (int, error)
	// ListSharedExternalMailConnections returns the organization's shared connections whose mailbox
	// is not deleted, live ones first, then id ascending, at most limit: every one when all is set,
	// otherwise those whose mailbox the membership holds with the manage permission.
	ListSharedExternalMailConnections(ctx context.Context, organizationID, membershipID uint, all bool, limit int) ([]*ExternalMailConnection, error)
	// ListExternalMailConnections returns the owner's connections in the organization, live ones
	// first, then id ascending, at most MaxExternalMailConnectionsPerUser.
	ListExternalMailConnections(ctx context.Context, organizationID, ownerUserID uint) ([]*ExternalMailConnection, error)
	// ListExternalMailConnectionDirectory returns up to limit connections of the organization in any
	// state, id descending, below beforeID when it is not zero.
	ListExternalMailConnectionDirectory(ctx context.Context, organizationID, beforeID uint, limit int) ([]*ExternalMailConnection, error)
	// FindExternalMailConnectionByMailbox returns the connection of a connected mailbox, in any state.
	FindExternalMailConnectionByMailbox(ctx context.Context, organizationID, mailboxID uint) (*ExternalMailConnection, error)
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
	ExternalMailTenantRepository
}

// ExternalMailSyncWorker syncs external mail outside request paths.
type ExternalMailSyncWorker interface {
	// SyncExternalMail fetches up to limit due connections and returns how many it handled.
	SyncExternalMail(ctx context.Context, limit int) (int, error)
}
