package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/externalmail"
)

// ExternalMailSubscriptionPO is the persistent object of a connection's push subscription.
type ExternalMailSubscriptionPO = externalmail.ExternalMailSubscriptionPO

var _ domain.ExternalMailPushRepository = (*repository)(nil)

func (r *repository) FindExternalMailSubscription(ctx context.Context, connectionID uint) (*domain.ExternalMailSubscription, error) {
	return r.externalMailRepo().FindExternalMailSubscription(ctx, connectionID)
}

func (r *repository) ListExternalMailSubscriptions(ctx context.Context, connectionIDs []uint) (map[uint]*domain.ExternalMailSubscription, error) {
	return r.externalMailRepo().ListExternalMailSubscriptions(ctx, connectionIDs)
}

func (r *repository) MatchExternalMailSubscriptions(ctx context.Context, provider domain.ExternalMailProvider, keys []string, limit int) ([]domain.ExternalMailPushHit, error) {
	return r.externalMailRepo().MatchExternalMailSubscriptions(ctx, provider, keys, limit)
}

func (r *repository) MarkExternalMailPushDegraded(ctx context.Context, connectionID uint, now, pollBy time.Time) (bool, error) {
	return r.externalMailRepo().MarkExternalMailPushDegraded(ctx, connectionID, now, pollBy)
}

func (r *repository) MarkExternalMailPush(ctx context.Context, mark domain.ExternalMailPushMark) (int, error) {
	return r.externalMailRepo().MarkExternalMailPush(ctx, mark)
}

func (r *repository) KeepExternalMailRotatedToken(ctx context.Context, connectionID uint, previous, sealed string) error {
	return r.externalMailRepo().KeepExternalMailRotatedToken(ctx, connectionID, previous, sealed)
}

func (r *repository) PromotePendingExternalMailPush(ctx context.Context, now time.Time, window, spacing time.Duration, limit int) (int, error) {
	return r.externalMailRepo().PromotePendingExternalMailPush(ctx, now, window, spacing, limit)
}

func (r *repository) SaveExternalMailSubscription(ctx context.Context, save domain.ExternalMailSubscriptionSave) error {
	return r.externalMailRepo().SaveExternalMailSubscription(ctx, save)
}

func (r *repository) RecordExternalMailPushSync(ctx context.Context, synced domain.ExternalMailSubscriptionSynced) error {
	return r.externalMailRepo().RecordExternalMailPushSync(ctx, synced)
}

func (r *repository) StopExternalMailSubscription(ctx context.Context, connectionID uint, now time.Time) (*domain.ExternalMailSubscription, error) {
	return r.externalMailRepo().StopExternalMailSubscription(ctx, connectionID, now)
}

func (r *repository) StopOrphanedExternalMailSubscriptions(ctx context.Context, now time.Time, limit int) (int, error) {
	return r.externalMailRepo().StopOrphanedExternalMailSubscriptions(ctx, now, limit)
}

func (r *repository) CountLiveExternalMailWatchers(ctx context.Context, provider domain.ExternalMailProvider, lookupKey string, exceptConnectionID uint) (int, error) {
	return r.externalMailRepo().CountLiveExternalMailWatchers(ctx, provider, lookupKey, exceptConnectionID)
}
