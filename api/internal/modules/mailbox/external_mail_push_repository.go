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
	LiveSince              *time.Time
	DegradedAt             *time.Time
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
		ScheduledSyncAt: po.ScheduledSyncAt, LiveSince: po.LiveSince, DegradedAt: po.DegradedAt,
		ConsecutiveFailures: po.ConsecutiveFailures,
		CreatedAt:           po.CreatedAt, UpdatedAt: po.UpdatedAt,
	}
	if po.LastErrorCode != nil {
		subscription.LastErrorCode = *po.LastErrorCode
	}
	return subscription
}

var _ domain.ExternalMailPushRepository = (*repository)(nil)

// pushTargetSQL is when a notification makes a connection (aliased c) due, given its subscription
// columns (aliased m): now, but never inside the coalescing window after the latest push-triggered
// sync, nor inside the fetch spacing after the latest sync, so a throttled backfill keeps its pace.
// Its named parameters are now, window, and spacing, in seconds.
const pushTargetSQL = `GREATEST(CAST(@now AS timestamptz),
	COALESCE(m.last_push_sync_at + make_interval(secs => @window), CAST(@now AS timestamptz)),
	COALESCE(c.last_synced_at + make_interval(secs => @spacing), CAST(@now AS timestamptz)))`

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

func (r *repository) MatchExternalMailSubscriptions(ctx context.Context, provider domain.ExternalMailProvider, keys []string, limit int) ([]domain.ExternalMailPushHit, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	wanted := make([]string, 0, len(keys))
	for _, key := range keys {
		if key != "" {
			wanted = append(wanted, key)
		}
	}
	if len(wanted) == 0 || limit <= 0 {
		return nil, nil
	}
	var hits []domain.ExternalMailPushHit
	err := r.conn(ctx).Raw(`SELECT s.lookup_key, s.id AS subscription_id, s.connection_id, s.secret_hash
		FROM external_mail_subscriptions s
		JOIN external_mail_connections c ON c.id = s.connection_id
		WHERE s.provider = ? AND s.lookup_key IN ? AND s.status <> 'stopped'
		  AND c.status = 'active' AND c.provider = s.provider
		ORDER BY s.id LIMIT ?`, string(provider), wanted, limit).Scan(&hits).Error
	return hits, err
}

func (r *repository) MarkExternalMailPushDegraded(ctx context.Context, connectionID uint, now, pollBy time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Exec(`UPDATE external_mail_subscriptions
		SET degraded_at = ?, renew_after = LEAST(renew_after, ?), updated_at = ?
		WHERE connection_id = ? AND status = 'active' AND degraded_at IS NULL`, now, now, now, connectionID)
	if result.Error != nil || result.RowsAffected == 0 {
		return false, result.Error
	}
	err := r.conn(ctx).Exec(`UPDATE external_mail_connections SET next_sync_at = LEAST(next_sync_at, ?)
		WHERE id = ? AND status = 'active'`, pollBy, connectionID).Error
	return err == nil, err
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
	// The request path touches subscription rows only, in one statement that never waits: a row a
	// sync's save holds at that moment is skipped, and the caller answers "try again" for it. The
	// worker's promotion step makes the connections due, under the usual lock order.
	var eligible int64
	if err := r.conn(ctx).Raw(`SELECT count(*) FROM external_mail_subscriptions WHERE id IN ? AND status <> 'stopped'`,
		mark.SubscriptionIDs).Scan(&eligible).Error; err != nil {
		return 0, err
	}
	result := r.conn(ctx).Exec(`UPDATE external_mail_subscriptions s
		SET last_notified_at = @now, pending_at = COALESCE(s.pending_at, @now),
		  -- Only a change notification shows delivery works; a lifecycle event does not.
		  degraded_at = CASE WHEN @lifecycle = '' THEN NULL ELSE s.degraded_at END, updated_at = @now`+renewal+`
		WHERE s.id IN (SELECT id FROM external_mail_subscriptions WHERE id IN @ids AND status <> 'stopped'
		  ORDER BY id FOR UPDATE SKIP LOCKED)`,
		map[string]any{"now": mark.Now, "ids": mark.SubscriptionIDs, "lifecycle": mark.Lifecycle})
	if result.Error != nil {
		return 0, result.Error
	}
	recorded := int(result.RowsAffected)
	if int64(recorded) < eligible {
		return recorded, domain.ErrExternalMailPushBusy
	}
	return recorded, nil
}

// KeepExternalMailRotatedToken stores a grant the provider rotated during a sync whose save failed,
// so the spent grant is never used again: only while the connection still holds the grant the sync
// started with (previous), so a member's reconnect meanwhile is never overwritten. A revoked
// connection, or one without a grant of its own, is left as it is.
func (r *repository) KeepExternalMailRotatedToken(ctx context.Context, connectionID uint, previous, sealed string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Exec(`UPDATE external_mail_connections SET encrypted_refresh_token = ?
		WHERE id = ? AND status <> 'revoked' AND tenant_id IS NULL AND encrypted_refresh_token <> ''
		  AND encrypted_refresh_token = ?`, sealed, connectionID, previous).Error
}

func (r *repository) PromotePendingExternalMailPush(ctx context.Context, now time.Time, window, spacing time.Duration, limit int) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	result := r.conn(ctx).Exec(`WITH due AS (
		  SELECT c.id, `+pushTargetSQL+` AS target
		  FROM external_mail_subscriptions m
		  JOIN external_mail_connections c ON c.id = m.connection_id
		  WHERE m.pending_at IS NOT NULL AND m.pending_at <= @now AND `+pushPromotableSQL+`
		  ORDER BY m.pending_at, m.id LIMIT @limit
		  FOR UPDATE OF c SKIP LOCKED
		)
		UPDATE external_mail_connections c SET next_sync_at = due.target FROM due WHERE c.id = due.id`,
		map[string]any{"now": now, "window": window.Seconds(), "spacing": spacing.Seconds(), "limit": limit})
	return int(result.RowsAffected), result.Error
}

func (r *repository) SaveExternalMailSubscription(ctx context.Context, save domain.ExternalMailSubscriptionSave) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	failures := min(save.ConsecutiveFailures, maxConsecutiveFailures)
	if save.Unsupported {
		// Nothing to subscribe: no key, secret, or expiry, and no failure to report.
		return r.conn(ctx).Exec(`INSERT INTO external_mail_subscriptions (connection_id, provider, status, renew_after,
		    created_at, updated_at)
		  VALUES (@connection, @provider, 'unsupported', @renew, @now, @now)
		  ON CONFLICT (connection_id) DO UPDATE SET status = 'unsupported', lookup_key = '', provider_subscription_id = '',
		    secret_hash = '', expires_at = NULL, live_since = NULL, degraded_at = NULL, pending_at = NULL,
		    renew_after = EXCLUDED.renew_after, consecutive_failures = 0, last_error_code = NULL, updated_at = EXCLUDED.updated_at`,
			map[string]any{"connection": save.ConnectionID, "provider": string(save.Provider), "renew": save.RenewAfter, "now": save.Now}).Error
	}
	if save.Succeeded {
		return r.conn(ctx).Exec(`INSERT INTO external_mail_subscriptions (connection_id, provider, lookup_key,
		    provider_subscription_id, secret_hash, status, expires_at, renew_after, live_since, consecutive_failures, created_at, updated_at)
		  VALUES (@connection, @provider, @key, @subscription, @secret, 'active', @expires, @renew, @now, 0, @now, @now)
		  ON CONFLICT (connection_id) DO UPDATE SET provider = EXCLUDED.provider, lookup_key = EXCLUDED.lookup_key,
		    provider_subscription_id = EXCLUDED.provider_subscription_id,
		    secret_hash = CASE WHEN EXCLUDED.secret_hash <> '' THEN EXCLUDED.secret_hash
		      WHEN external_mail_subscriptions.provider_subscription_id = EXCLUDED.provider_subscription_id THEN external_mail_subscriptions.secret_hash
		      ELSE '' END,
		    live_since = CASE WHEN external_mail_subscriptions.status = 'active' AND external_mail_subscriptions.expires_at > EXCLUDED.updated_at
		      THEN external_mail_subscriptions.live_since ELSE EXCLUDED.updated_at END,
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
	    status = CASE WHEN external_mail_subscriptions.status IN ('stopped', 'unsupported') THEN 'pending' ELSE external_mail_subscriptions.status END,
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
		SET pending_at = CASE WHEN s.pending_at IS NULL THEN NULL
		    WHEN s.pending_at > @started THEN s.pending_at
		    -- A notification received after the fetch started is not answered by it, even when an
		    -- earlier one was pending: it stays pending from its own time.
		    WHEN s.last_notified_at > @started THEN s.last_notified_at
		    ELSE NULL END,
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
	pending_at = NULL, degraded_at = NULL, updated_at = ?`

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
