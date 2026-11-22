package domain

import (
	"context"
	"time"
)

// ExternalMailProviderRelease is what is left to do at a provider after a connection was revoked:
// stop its push subscription and hand its grant back. It is written in the revocation's transaction,
// so it survives a process that stops right after the commit, and deleted once done or past its
// deadline, which also erases the sealed grant it holds.
type ExternalMailProviderRelease struct {
	ID             uint
	ConnectionID   uint
	OrganizationID uint
	Provider       ExternalMailProvider
	// EncryptedGrant is the connection's sealed refresh token, copied before the revocation erased
	// it; empty for a connection without one of its own (a tenant mailbox).
	EncryptedGrant string
	// StopPush asks to stop the push subscription StopSubscriptionID names (empty for Gmail).
	StopPush           bool
	StopSubscriptionID string
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
	// DeleteExternalMailProviderRelease deletes a release, and the sealed grant with it.
	DeleteExternalMailProviderRelease(ctx context.Context, id uint) error
	// DeferExternalMailProviderRelease records a failed attempt and when to try again.
	DeferExternalMailProviderRelease(ctx context.Context, id uint, attempts int, nextAttemptAt time.Time) error
	// DeleteExpiredExternalMailProviderReleases deletes up to limit releases past their deadline.
	DeleteExpiredExternalMailProviderReleases(ctx context.Context, now time.Time, limit int) (int, error)
}
