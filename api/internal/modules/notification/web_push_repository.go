package notification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// webPushTarget is a stored subscription with the device secrets a send needs. It never leaves
// the starter.
type webPushTarget struct {
	ID       uint
	Endpoint string
	P256dh   string
	Auth     string
}

// webPushStore persists browser push subscriptions.
type webPushStore interface {
	// UpsertWebPushSubscription stores the subscription keyed by its endpoint hash, moving it to
	// the caller when another user held it, and keeps at most limit subscriptions for the user by
	// removing the least recently registered.
	UpsertWebPushSubscription(context.Context, webPushRegistration, int) (*WebPushSubscription, error)
	ListWebPushSubscriptions(context.Context, uint) ([]*WebPushSubscription, error)
	DeleteWebPushSubscription(context.Context, uint, uint) (bool, error)
	ListWebPushTargets(context.Context, uint) ([]webPushTarget, error)
	RecordWebPushDelivered(context.Context, uint, time.Time) error
	RemoveWebPushSubscription(context.Context, uint) error
	// RecordWebPushRejected counts a permanent refusal and removes the subscription once it
	// reaches maxFailures.
	RecordWebPushRejected(context.Context, uint, int16) error
}

// webPushRegistration is a validated subscription about to be stored.
type webPushRegistration struct {
	UserID       uint
	Endpoint     string
	EndpointHash string
	P256dh       string
	Auth         string
	DeviceLabel  string
	At           time.Time
}

var _ webPushStore = (*repository)(nil)

func (r *repository) UpsertWebPushSubscription(
	ctx context.Context,
	registration webPushRegistration,
	limit int,
) (*WebPushSubscription, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var stored WebPushSubscription
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize registrations of one user so the cap holds under concurrency.
		var owner struct{ ID uint }
		if err := tx.Raw(
			`SELECT id FROM users WHERE id = ? AND deleted_at IS NULL FOR NO KEY UPDATE`,
			registration.UserID,
		).Scan(&owner).Error; err != nil {
			return fmt.Errorf("lock web push subscriber: %w", err)
		}
		if owner.ID == 0 {
			return domain.ErrUserNotFound
		}
		if err := tx.Raw(`
INSERT INTO notification_web_push_subscriptions
    (user_id, endpoint, endpoint_hash, p256dh, auth, device_label, failure_count, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)
ON CONFLICT (endpoint_hash) DO UPDATE SET
    created_at = CASE WHEN notification_web_push_subscriptions.user_id = EXCLUDED.user_id
        THEN notification_web_push_subscriptions.created_at ELSE EXCLUDED.created_at END,
    last_success_at = CASE WHEN notification_web_push_subscriptions.user_id = EXCLUDED.user_id
        THEN notification_web_push_subscriptions.last_success_at ELSE NULL END,
    user_id = EXCLUDED.user_id,
    endpoint = EXCLUDED.endpoint,
    p256dh = EXCLUDED.p256dh,
    auth = EXCLUDED.auth,
    device_label = EXCLUDED.device_label,
    failure_count = 0,
    updated_at = EXCLUDED.updated_at
RETURNING id, device_label, last_success_at, created_at`,
			registration.UserID,
			registration.Endpoint,
			registration.EndpointHash,
			registration.P256dh,
			registration.Auth,
			registration.DeviceLabel,
			registration.At,
			registration.At,
		).Scan(&stored).Error; err != nil {
			return fmt.Errorf("store web push subscription: %w", err)
		}
		if err := tx.Exec(`
DELETE FROM notification_web_push_subscriptions
WHERE user_id = ? AND id NOT IN (
    SELECT id FROM notification_web_push_subscriptions
    WHERE user_id = ?
    ORDER BY updated_at DESC, id DESC
    LIMIT ?
)`, registration.UserID, registration.UserID, limit).Error; err != nil {
			return fmt.Errorf("trim web push subscriptions: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func (r *repository) ListWebPushSubscriptions(ctx context.Context, userID uint) ([]*WebPushSubscription, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []NotificationWebPushSubscriptionPO
	if err := r.db.WithContext(ctx).
		Select("id", "device_label", "last_success_at", "created_at").
		Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Limit(maxWebPushSubscriptionsPerUser).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list web push subscriptions: %w", err)
	}
	items := make([]*WebPushSubscription, len(rows))
	for index := range rows {
		items[index] = &WebPushSubscription{
			ID:            rows[index].ID,
			DeviceLabel:   rows[index].DeviceLabel,
			LastSuccessAt: cloneTime(rows[index].LastSuccessAt),
			CreatedAt:     rows[index].CreatedAt,
		}
	}
	return items, nil
}

func (r *repository) DeleteWebPushSubscription(ctx context.Context, userID, id uint) (bool, error) {
	if r == nil || r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&NotificationWebPushSubscriptionPO{})
	if result.Error != nil {
		return false, fmt.Errorf("delete web push subscription: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func (r *repository) ListWebPushTargets(ctx context.Context, userID uint) ([]webPushTarget, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var targets []webPushTarget
	if err := r.db.WithContext(ctx).Model(&NotificationWebPushSubscriptionPO{}).
		Select("id", "endpoint", "p256dh", "auth").
		Where("user_id = ?", userID).
		Order("id ASC").
		Limit(maxWebPushSubscriptionsPerUser).
		Scan(&targets).Error; err != nil {
		return nil, fmt.Errorf("list web push targets: %w", err)
	}
	return targets, nil
}

// RecordWebPushDelivered leaves updated_at alone: it orders subscriptions by registration for the
// per-user cap.
func (r *repository) RecordWebPushDelivered(ctx context.Context, id uint, at time.Time) error {
	if r == nil || r.db == nil {
		return domain.ErrServiceUnavailable
	}
	if err := r.db.WithContext(ctx).Model(&NotificationWebPushSubscriptionPO{}).
		Where("id = ?", id).
		Updates(map[string]any{"last_success_at": at, "failure_count": 0}).Error; err != nil {
		return fmt.Errorf("record web push success: %w", err)
	}
	return nil
}

func (r *repository) RemoveWebPushSubscription(ctx context.Context, id uint) error {
	if r == nil || r.db == nil {
		return domain.ErrServiceUnavailable
	}
	if err := r.db.WithContext(ctx).Where("id = ?", id).
		Delete(&NotificationWebPushSubscriptionPO{}).Error; err != nil {
		return fmt.Errorf("remove web push subscription: %w", err)
	}
	return nil
}

func (r *repository) RecordWebPushRejected(ctx context.Context, id uint, maxFailures int16) error {
	if r == nil || r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var failures struct{ FailureCount int16 }
		err := tx.Raw(`
UPDATE notification_web_push_subscriptions
SET failure_count = failure_count + 1
WHERE id = ?
RETURNING failure_count`, id).Scan(&failures).Error
		if err != nil {
			return fmt.Errorf("record web push rejection: %w", err)
		}
		if failures.FailureCount < maxFailures {
			return nil
		}
		if err := tx.Where("id = ?", id).Delete(&NotificationWebPushSubscriptionPO{}).Error; err != nil {
			return fmt.Errorf("remove rejected web push subscription: %w", err)
		}
		return nil
	})
}

func hasWebPushSubscription(tx *gorm.DB, userID uint) (bool, error) {
	var found struct{ ID uint }
	err := tx.Model(&NotificationWebPushSubscriptionPO{}).
		Select("id").
		Where("user_id = ?", userID).
		Limit(1).
		Take(&found).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find web push subscription: %w", err)
	}
	return true, nil
}
