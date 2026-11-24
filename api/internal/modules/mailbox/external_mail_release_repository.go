package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/externalmail"
)

// ExternalMailProviderReleasePO is the persistent object of a pending provider release.
type ExternalMailProviderReleasePO = externalmail.ExternalMailProviderReleasePO

var _ domain.ExternalMailReleaseRepository = (*repository)(nil)

func (r *repository) CreateExternalMailProviderRelease(ctx context.Context, release *domain.ExternalMailProviderRelease) error {
	return r.externalMailRepo().CreateExternalMailProviderRelease(ctx, release)
}

func (r *repository) ClaimExternalMailProviderReleases(ctx context.Context, now, leaseUntil time.Time, limit int) ([]*domain.ExternalMailProviderRelease, error) {
	return r.externalMailRepo().ClaimExternalMailProviderReleases(ctx, now, leaseUntil, limit)
}

func (r *repository) ClaimExternalMailProviderRelease(ctx context.Context, id uint, expected, leaseUntil time.Time) (bool, error) {
	return r.externalMailRepo().ClaimExternalMailProviderRelease(ctx, id, expected, leaseUntil)
}

func (r *repository) DeleteExternalMailProviderRelease(ctx context.Context, id uint) error {
	return r.externalMailRepo().DeleteExternalMailProviderRelease(ctx, id)
}

func (r *repository) DeferExternalMailProviderRelease(ctx context.Context, id uint, attempts int, nextAttemptAt time.Time) error {
	return r.externalMailRepo().DeferExternalMailProviderRelease(ctx, id, attempts, nextAttemptAt)
}

func (r *repository) DeleteExpiredExternalMailProviderReleases(ctx context.Context, now time.Time, limit int) (int, error) {
	return r.externalMailRepo().DeleteExpiredExternalMailProviderReleases(ctx, now, limit)
}

func (r *repository) DeleteExternalMailProviderReleasesForAccount(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, address string, stopOnly bool) error {
	return r.externalMailRepo().DeleteExternalMailProviderReleasesForAccount(ctx, organizationID, provider, address, stopOnly)
}

func (r *repository) CountLiveExternalMailAccountGrants(ctx context.Context, provider domain.ExternalMailProvider, address string, exceptConnectionID uint) (int, error) {
	return r.externalMailRepo().CountLiveExternalMailAccountGrants(ctx, provider, address, exceptConnectionID)
}

func (r *repository) CountPendingExternalMailAuthorizations(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, userID, connectionID uint, now time.Time) (int, error) {
	return r.externalMailRepo().CountPendingExternalMailAuthorizations(ctx, organizationID, provider, userID, connectionID, now)
}
