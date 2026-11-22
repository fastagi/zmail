package domain

import (
	"context"
	"time"
)

// ExternalMailProviderRelease is what is left to do at a provider after a connection was revoked:
// stop its push subscription and hand its grant back. It is written in the revocation's transaction,
// so it survives a process that stops right after the commit, outlives the purge of its connection,
// and is deleted once done or past its deadline, which also erases the sealed grant it holds.
type ExternalMailProviderRelease struct {
	ID             uint
	OrganizationID uint
	// ConnectionID is the revoked connection; its row may be gone by the time the release runs.
	ConnectionID      uint
	Provider          ExternalMailProvider
	AccountAddress    string
	TenantID          *uint
	TenantDirectoryID string
	// EncryptedGrant is the connection's sealed refresh token, kept only for a connector whose
	// revocation really revokes a provider grant; empty otherwise and for a tenant mailbox.
	EncryptedGrant string
	// StopPush asks to stop the push subscription StopSubscriptionID names (empty for Gmail), watched
	// under StopWatchKey (Gmail's primary address).
	StopPush           bool
	StopSubscriptionID string
	StopWatchKey       string
	Attempts           int
	NextAttemptAt      time.Time
	ExpiresAt          time.Time
	CreatedAt          time.Time
}

// ExternalMailReleaseRepository keeps the pending provider releases.
type ExternalMailReleaseRepository interface {
	// CreateExternalMailProviderRelease stores a release and fills its id; call it in the revocation's
	// transaction.
	CreateExternalMailProviderRelease(ctx context.Context, release *ExternalMailProviderRelease) error
	// ClaimExternalMailProviderReleases returns up to limit releases due at now, oldest first, moving
	// each one's next attempt to leaseUntil, skipping rows another worker holds.
	ClaimExternalMailProviderReleases(ctx context.Context, now, leaseUntil time.Time, limit int) ([]*ExternalMailProviderRelease, error)
	// ClaimExternalMailProviderRelease takes one release whose next attempt is still expected, moving
	// it to leaseUntil; false when another caller took it or it is gone.
	ClaimExternalMailProviderRelease(ctx context.Context, id uint, expected, leaseUntil time.Time) (bool, error)
	// DeleteExternalMailProviderRelease deletes a release, and the sealed grant with it.
	DeleteExternalMailProviderRelease(ctx context.Context, id uint) error
	// DeferExternalMailProviderRelease records a failed attempt and when to try again.
	DeferExternalMailProviderRelease(ctx context.Context, id uint, attempts int, nextAttemptAt time.Time) error
	// DeleteExpiredExternalMailProviderReleases deletes up to limit releases past their deadline.
	DeleteExpiredExternalMailProviderReleases(ctx context.Context, now time.Time, limit int) (int, error)
	// DeleteExternalMailProviderReleasesForAccount deletes the organization's pending releases of the
	// provider account, ignoring the address's case: the account was bound again.
	DeleteExternalMailProviderReleasesForAccount(ctx context.Context, organizationID uint, provider ExternalMailProvider, address string) error
	// CountLiveExternalMailAccountGrants counts the connections other than exceptConnectionID, in any
	// organization, that read the provider account with a grant of their own and are not revoked.
	CountLiveExternalMailAccountGrants(ctx context.Context, provider ExternalMailProvider, address string, exceptConnectionID uint) (int, error)
}
