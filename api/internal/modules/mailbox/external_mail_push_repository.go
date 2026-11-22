package mailbox

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ExternalMailSubscriptionPO is the persistent object of a connection's push subscription.
type ExternalMailSubscriptionPO struct {
	ID                     uint   `gorm:"primaryKey"`
	ConnectionID           uint   `gorm:"not null;uniqueIndex"`
	Provider               string `gorm:"size:16;not null"`
	LookupKey              string `gorm:"size:320;not null;default:''"`
	ProviderSubscriptionID string `gorm:"size:255;not null;default:''"`
	SecretHash             string `gorm:"size:64;not null;default:''"`
	Status                 string `gorm:"size:16;not null;default:'pending'"`
	ExpiresAt              *time.Time
	RenewAfter             time.Time
	PendingAt              *time.Time
	LastNotifiedAt         *time.Time
	LastPushSyncAt         *time.Time
	ScheduledSyncAt        *time.Time
	ConsecutiveFailures    int     `gorm:"type:smallint;not null;default:0"`
	LastErrorCode          *string `gorm:"size:64"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (ExternalMailSubscriptionPO) TableName() string {
	return "external_mail_subscriptions"
}

func (po *ExternalMailSubscriptionPO) toDomain() *domain.ExternalMailSubscription {
	subscription := &domain.ExternalMailSubscription{
		ID: po.ID, ConnectionID: po.ConnectionID, Provider: domain.ExternalMailProvider(po.Provider),
		LookupKey: po.LookupKey, ProviderSubscriptionID: po.ProviderSubscriptionID, SecretHash: po.SecretHash,
		Status: domain.ExternalMailSubscriptionStatus(po.Status), ExpiresAt: po.ExpiresAt, RenewAfter: po.RenewAfter,
		PendingAt: po.PendingAt, LastNotifiedAt: po.LastNotifiedAt, LastPushSyncAt: po.LastPushSyncAt,
		ScheduledSyncAt: po.ScheduledSyncAt, ConsecutiveFailures: po.ConsecutiveFailures,
		CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt,
	}
	if po.LastErrorCode != nil {
		subscription.LastErrorCode = *po.LastErrorCode
	}
	return subscription
}

var _ domain.ExternalMailPushRepository = (*repository)(nil)

// pushTargetSQL is when a notification makes a connection (aliased c) due, given its subscription
// columns (aliased m): now, or the end of the coalescing window after the latest push-triggered
// sync. Its parameters are now, now, and the window in seconds.
const pushTargetSQL = `GREATEST(CAST(? AS timestamptz), COALESCE(m.last_push_sync_at + make_interval(secs => ?), CAST(? AS timestamptz)))`

// pushPromotableSQL is when a connection may be made due sooner: it is active, not backing off, not
// leased (next_sync_at is still what its latest save scheduled), and not due earlier already.
const pushPromotableSQL = `c.status = 'active' AND c.consecutive_failures = 0
	AND m.scheduled_sync_at IS NOT NULL AND c.next_sync_at = m.scheduled_sync_at AND c.next_sync_at > ` + pushTargetSQL

func (r *repository) FindExternalMailSubscription(ctx context.Context, connectionID uint) (*domain.ExternalMailSubscription, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []ExternalMailSubscriptionPO
	if err := r.conn(ctx).Where("connection_id = ?", connectionID).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0].toDomain(), nil
}

func (r *repository) ListExternalMailSubscriptions(ctx context.Context, connectionIDs []uint) (map[uint]*domain.ExternalMailSubscription, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	found := map[uint]*domain.ExternalMailSubscription{}
	if len(connectionIDs) == 0 {
		return found, nil
	}
	var rows []ExternalMailSubscriptionPO
	if err := r.conn(ctx).Where("connection_id IN ?", connectionIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		found[rows[i].ConnectionID] = rows[i].toDomain()
	}
	return found, nil
}

func (r *repository) MatchExternalMailSubscriptions(ctx context.Context, provider domain.ExternalMailProvider, lookupKey string, limit int) ([]domain.ExternalMailPushHit, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if lookupKey == "" || limit <= 0 {
		return nil, nil
	}
	var hits []domain.ExternalMailPushHit
	err := r.conn(ctx).Raw(`SELECT s.id AS subscription_id, s.connection_id, s.secret_hash
		FROM external_mail_subscriptions s
		JOIN external_mail_connections c ON c.id = s.connection_id
		WHERE s.provider = ? AND s.lookup_key = ? AND s.status <> 'stopped'
		  AND c.status = 'active' AND c.provider = s.provider
		ORDER BY s.id LIMIT ?`, string(provider), lookupKey, limit).Scan(&hits).Error
	return hits, err
}

func (r *repository) MarkExternalMailPush(ctx context.Context, mark domain.ExternalMailPushMark) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if len(mark.SubscriptionIDs) == 0 {
		return 0, nil
	}
	renewal := ""
	switch mark.Lifecycle {
	case domain.ExternalMailPushReauthorize:
		// The provider wants the subscription renewed: the next sync of the connection does it.
		renewal = `, renew_after = LEAST(s.renew_after, @now)`
	case domain.ExternalMailPushRemoved:
		// The provider ended the subscription: it is no longer live, and the next sync creates it again.
		renewal = `, renew_after = @now, expires_at = CASE WHEN s.expires_at IS NULL THEN NULL ELSE LEAST(s.expires_at, @now) END,
			status = CASE WHEN s.status = 'active' THEN 'pending' ELSE s.status END`
	}
	window := mark.Window.Seconds()
	result := r.conn(ctx).Exec(`WITH m AS (
		  UPDATE external_mail_subscriptions s
		  SET last_notified_at = @now, pending_at = COALESCE(s.pending_at, @now), updated_at = @now`+renewal+`
		  WHERE s.id IN @ids AND s.status <> 'stopped'
		  RETURNING s.connection_id, s.scheduled_sync_at, s.last_push_sync_at
		)
		UPDATE external_mail_connections c
		SET next_sync_at = GREATEST(CAST(@now AS timestamptz), COALESCE(m.last_push_sync_at + make_interval(secs => @window), CAST(@now AS timestamptz)))
		FROM m
		WHERE c.id = m.connection_id AND c.status = 'active' AND c.consecutive_failures = 0
		  AND m.scheduled_sync_at IS NOT NULL AND c.next_sync_at = m.scheduled_sync_at
		  AND c.next_sync_at > GREATEST(CAST(@now AS timestamptz), COALESCE(m.last_push_sync_at + make_interval(secs => @window), CAST(@now AS timestamptz)))`,
		map[string]any{"now": mark.Now, "ids": mark.SubscriptionIDs, "window": window})
	return int(result.RowsAffected), result.Error
}

func (r *repository) PromotePendingExternalMailPush(ctx context.Context, now time.Time, window time.Duration, limit int) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	seconds := window.Seconds()
	result := r.conn(ctx).Exec(`WITH due AS (
		  SELECT c.id, `+pushTargetSQL+` AS target
		  FROM external_mail_subscriptions m
		  JOIN external_mail_connections c ON c.id = m.connection_id
		  WHERE m.pending_at IS NOT NULL AND m.pending_at <= ? AND `+pushPromotableSQL+`
		  ORDER BY m.pending_at, m.id LIMIT ?
		  FOR UPDATE OF c SKIP LOCKED
		)
		UPDATE external_mail_connections c SET next_sync_at = due.target FROM due WHERE c.id = due.id`,
		now, seconds, now, now, now, seconds, now, limit)
	return int(result.RowsAffected), result.Error
}

func (r *repository) SaveExternalMailSubscription(ctx context.Context, save domain.ExternalMailSubscriptionSave) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	failures := min(save.ConsecutiveFailures, maxConsecutiveFailures)
	if save.Succeeded {
		return r.conn(ctx).Exec(`INSERT INTO external_mail_subscriptions (connection_id, provider, lookup_key,
		    provider_subscription_id, secret_hash, status, expires_at, renew_after, consecutive_failures, created_at, updated_at)
		  VALUES (@connection, @provider, @key, @subscription, @secret, 'active', @expires, @renew, 0, @now, @now)
		  ON CONFLICT (connection_id) DO UPDATE SET provider = EXCLUDED.provider, lookup_key = EXCLUDED.lookup_key,
		    provider_subscription_id = EXCLUDED.provider_subscription_id,
		    secret_hash = CASE WHEN EXCLUDED.secret_hash <> '' THEN EXCLUDED.secret_hash
		      WHEN external_mail_subscriptions.provider_subscription_id = EXCLUDED.provider_subscription_id THEN external_mail_subscriptions.secret_hash
		      ELSE '' END,
		    status = 'active', expires_at = EXCLUDED.expires_at, renew_after = EXCLUDED.renew_after,
		    consecutive_failures = 0, last_error_code = NULL, updated_at = EXCLUDED.updated_at`,
			map[string]any{
				"connection": save.ConnectionID, "provider": string(save.Provider), "key": save.LookupKey,
				"subscription": save.ProviderSubscriptionID, "secret": save.SecretHash, "expires": save.ExpiresAt,
				"renew": save.RenewAfter, "now": save.Now,
			}).Error
	}
	return r.conn(ctx).Exec(`INSERT INTO external_mail_subscriptions (connection_id, provider, status, renew_after,
	    consecutive_failures, last_error_code, created_at, updated_at)
	  VALUES (@connection, @provider, 'pending', @renew, @failures, @code, @now, @now)
	  ON CONFLICT (connection_id) DO UPDATE SET renew_after = EXCLUDED.renew_after,
	    consecutive_failures = EXCLUDED.consecutive_failures, last_error_code = EXCLUDED.last_error_code,
	    status = CASE WHEN external_mail_subscriptions.status = 'stopped' THEN 'pending' ELSE external_mail_subscriptions.status END,
	    updated_at = EXCLUDED.updated_at`,
		map[string]any{
			"connection": save.ConnectionID, "provider": string(save.Provider), "renew": save.RenewAfter,
			"failures": failures, "code": optionalText(save.LastErrorCode), "now": save.Now,
		}).Error
}

func (r *repository) RecordExternalMailPushSync(ctx context.Context, synced domain.ExternalMailSubscriptionSynced) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Exec(`UPDATE external_mail_subscriptions s
		SET pending_at = CASE WHEN s.pending_at <= @started THEN NULL ELSE s.pending_at END,
		  last_push_sync_at = CASE WHEN @pushed THEN CAST(@started AS timestamptz) ELSE s.last_push_sync_at END,
		  scheduled_sync_at = c.next_sync_at, updated_at = @now
		FROM external_mail_connections c
		WHERE s.connection_id = @connection AND c.id = s.connection_id AND s.status <> 'stopped'`,
		map[string]any{"started": synced.FetchStartedAt, "pushed": synced.Pushed, "now": synced.Now, "connection": synced.ConnectionID}).Error
}

func (r *repository) StopExternalMailSubscription(ctx context.Context, connectionID uint, now time.Time) (*domain.ExternalMailSubscription, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var stopped *domain.ExternalMailSubscription
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []ExternalMailSubscriptionPO
		if err := tx.Raw(`SELECT * FROM external_mail_subscriptions WHERE connection_id = ? AND status <> 'stopped' FOR UPDATE`,
			connectionID).Scan(&rows).Error; err != nil || len(rows) == 0 {
			return err
		}
		stopped = rows[0].toDomain()
		return tx.Exec(stopSubscriptionSQL+` WHERE id = ?`, now, rows[0].ID).Error
	})
	if err != nil {
		return nil, err
	}
	return stopped, nil
}

// stopSubscriptionSQL ends a subscription row and erases what a notification could match it by.
const stopSubscriptionSQL = `UPDATE external_mail_subscriptions SET status = 'stopped', lookup_key = '', secret_hash = '',
	pending_at = NULL, updated_at = ?`

func (r *repository) StopOrphanedExternalMailSubscriptions(ctx context.Context, now time.Time, limit int) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	result := r.conn(ctx).Exec(stopSubscriptionSQL+` WHERE id IN (
		SELECT s.id FROM external_mail_subscriptions s
		JOIN external_mail_connections c ON c.id = s.connection_id
		WHERE s.status <> 'stopped' AND c.status <> 'active'
		ORDER BY s.id LIMIT ?
		FOR UPDATE OF s SKIP LOCKED)`, now, limit)
	return int(result.RowsAffected), result.Error
}

func (r *repository) CountLiveExternalMailWatchers(ctx context.Context, provider domain.ExternalMailProvider, lookupKey string, exceptConnectionID uint) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if lookupKey == "" {
		return 0, nil
	}
	var count int64
	err := r.conn(ctx).Raw(`SELECT count(*) FROM external_mail_subscriptions s
		JOIN external_mail_connections c ON c.id = s.connection_id
		WHERE s.provider = ? AND s.lookup_key = ? AND s.status <> 'stopped' AND s.connection_id <> ? AND c.status = 'active'`,
		string(provider), lookupKey, exceptConnectionID).Scan(&count).Error
	return int(count), err
}
