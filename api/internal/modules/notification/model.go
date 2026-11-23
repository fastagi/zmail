package notification

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/user"
)

type deliveryStatus string

const (
	deliveryStatusPending    deliveryStatus = "pending"
	deliveryStatusProcessing deliveryStatus = "processing"
	deliveryStatusDelivered  deliveryStatus = "delivered"
	deliveryStatusFailed     deliveryStatus = "failed"
)

// NotificationPO is the persistent notification event addressed to one user.
type NotificationPO struct {
	ID              uint       `gorm:"primaryKey"`
	UserID          uint       `gorm:"not null;uniqueIndex:idx_notifications_user_idempotency,priority:1;index:idx_notifications_user_created,priority:1;index:idx_notifications_user_read,priority:1"`
	IdempotencyKey  string     `gorm:"size:128;not null;uniqueIndex:idx_notifications_user_idempotency,priority:2"`
	PublicationHash string     `gorm:"size:64;not null"`
	Kind            string     `gorm:"size:100;not null;index"`
	Title           string     `gorm:"size:640;not null"`
	Body            string     `gorm:"type:text;not null"`
	ActionURL       string     `gorm:"size:2048;not null;default:''"`
	ReadAt          *time.Time `gorm:"index:idx_notifications_user_read,priority:2"`
	CreatedAt       time.Time  `gorm:"not null;index:idx_notifications_user_created,priority:2,sort:desc"`
	UpdatedAt       time.Time  `gorm:"not null"`

	User       user.UserPO              `gorm:"foreignKey:UserID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Deliveries []NotificationDeliveryPO `gorm:"foreignKey:NotificationID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (NotificationPO) TableName() string { return "notifications" }

// NotificationDeliveryPO is one channel execution ledger entry.
type NotificationDeliveryPO struct {
	ID              uint       `gorm:"primaryKey"`
	NotificationID  uint       `gorm:"not null;uniqueIndex:idx_notification_deliveries_notification_channel,priority:1;index"`
	Channel         string     `gorm:"size:24;not null;uniqueIndex:idx_notification_deliveries_notification_channel,priority:2;index:idx_notification_deliveries_pending,priority:1;index:idx_notification_deliveries_leased,priority:1;check:notification_deliveries_channel_check,channel IN ('in_app','email','web_push')"`
	Status          string     `gorm:"size:24;not null;index:idx_notification_deliveries_pending,priority:2;index:idx_notification_deliveries_leased,priority:2;check:notification_deliveries_status_check,status IN ('pending','processing','delivered','failed')"`
	Attempts        uint8      `gorm:"not null;default:0"`
	AvailableAt     time.Time  `gorm:"not null;index:idx_notification_deliveries_pending,priority:3"`
	LeaseToken      string     `gorm:"size:64;not null;default:''"`
	LeaseExpiresAt  *time.Time `gorm:"index:idx_notification_deliveries_leased,priority:3"`
	DestinationHash string     `gorm:"size:64;not null;default:''"`
	DeliveredAt     *time.Time
	LastFailureCode string    `gorm:"size:64;not null;default:''"`
	CreatedAt       time.Time `gorm:"not null"`
	UpdatedAt       time.Time `gorm:"not null"`

	Notification NotificationPO `gorm:"foreignKey:NotificationID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (NotificationDeliveryPO) TableName() string { return "notification_deliveries" }

// NotificationPreferencePO stores explicit global channel choices for one user.
type NotificationPreferencePO struct {
	UserID         uint      `gorm:"primaryKey"`
	InAppEnabled   bool      `gorm:"not null"`
	EmailEnabled   bool      `gorm:"not null"`
	WebPushEnabled bool      `gorm:"not null"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`

	User user.UserPO `gorm:"foreignKey:UserID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (NotificationPreferencePO) TableName() string { return "notification_preferences" }

// NotificationWebPushSubscriptionPO is one browser push subscription. The endpoint is unique by its
// SHA-256 digest, so a browser that subscribes again under another account moves to that account.
// Endpoint and keys are secrets of the device: they never leave the API.
type NotificationWebPushSubscriptionPO struct {
	ID            uint   `gorm:"primaryKey"`
	UserID        uint   `gorm:"not null;index:idx_web_push_subscriptions_user"`
	Endpoint      string `gorm:"size:2048;not null"`
	EndpointHash  string `gorm:"size:64;not null;uniqueIndex:idx_web_push_subscriptions_endpoint"`
	P256dh        string `gorm:"column:p256dh;size:256;not null"`
	Auth          string `gorm:"size:64;not null"`
	DeviceLabel   string `gorm:"size:120;not null;default:''"`
	LastSuccessAt *time.Time
	FailureCount  int16     `gorm:"not null;default:0"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`

	User user.UserPO `gorm:"foreignKey:UserID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (NotificationWebPushSubscriptionPO) TableName() string {
	return "notification_web_push_subscriptions"
}

func notificationFromPO(po *NotificationPO) *domain.Notification {
	if po == nil {
		return nil
	}
	return &domain.Notification{
		ID:             po.ID,
		UserID:         po.UserID,
		IdempotencyKey: po.IdempotencyKey,
		Kind:           po.Kind,
		Title:          po.Title,
		Body:           po.Body,
		ActionURL:      po.ActionURL,
		ReadAt:         cloneTime(po.ReadAt),
		CreatedAt:      po.CreatedAt,
		UpdatedAt:      po.UpdatedAt,
	}
}

func preferenceFromPO(po *NotificationPreferencePO) *domain.NotificationPreference {
	if po == nil {
		return nil
	}
	return &domain.NotificationPreference{
		UserID:         po.UserID,
		InAppEnabled:   po.InAppEnabled,
		EmailEnabled:   po.EmailEnabled,
		WebPushEnabled: po.WebPushEnabled,
		CreatedAt:      po.CreatedAt,
		UpdatedAt:      po.UpdatedAt,
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
