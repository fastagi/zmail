package notification

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

type replaceNotificationReadStateRequest struct {
	IsRead *bool `json:"is_read" binding:"required"`
}

type replaceNotificationReadStateThroughRequest struct {
	ThroughID uint `json:"through_id" binding:"required"`
}

type replaceNotificationPreferenceRequest struct {
	InAppEnabled *bool `json:"in_app_enabled" binding:"required"`
	EmailEnabled *bool `json:"email_enabled" binding:"required"`
	// WebPushEnabled is optional so older clients keep working; omitted keeps the stored value.
	WebPushEnabled *bool `json:"web_push_enabled"`
}

type putWebPushSubscriptionRequest struct {
	Endpoint    string             `json:"endpoint" binding:"required,max=2048"`
	Keys        webPushKeysRequest `json:"keys" binding:"required"`
	DeviceLabel string             `json:"device_label" binding:"max=480"`
}

type webPushKeysRequest struct {
	P256dh string `json:"p256dh" binding:"required,max=256"`
	Auth   string `json:"auth" binding:"required,max=64"`
}

type WebPushConfigResponse struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
}

type WebPushSubscriptionResponse struct {
	ID            uint       `json:"id"`
	DeviceLabel   string     `json:"device_label"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
}

type NotificationResponse struct {
	ID        uint       `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	ActionURL string     `json:"action_url,omitempty"`
	IsRead    bool       `json:"is_read"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type NotificationStatusResponse struct {
	UnreadCount int64 `json:"unread_count"`
}

type NotificationReadStateResponse struct {
	UpdatedCount int64 `json:"updated_count"`
	UnreadCount  int64 `json:"unread_count"`
}

type NotificationPreferenceResponse struct {
	InAppEnabled   bool `json:"in_app_enabled"`
	EmailEnabled   bool `json:"email_enabled"`
	WebPushEnabled bool `json:"web_push_enabled"`
}

func toNotificationResponse(notification *domain.Notification) *NotificationResponse {
	if notification == nil {
		return nil
	}
	return &NotificationResponse{
		ID:        notification.ID,
		Kind:      notification.Kind,
		Title:     notification.Title,
		Body:      notification.Body,
		ActionURL: notification.ActionURL,
		IsRead:    notification.IsRead(),
		ReadAt:    cloneTime(notification.ReadAt),
		CreatedAt: notification.CreatedAt,
	}
}

func toNotificationPreferenceResponse(
	preference *domain.NotificationPreference,
) *NotificationPreferenceResponse {
	if preference == nil {
		return nil
	}
	return &NotificationPreferenceResponse{
		InAppEnabled:   preference.InAppEnabled,
		EmailEnabled:   preference.EmailEnabled,
		WebPushEnabled: preference.WebPushEnabled,
	}
}

func toWebPushSubscriptionResponse(subscription *WebPushSubscription) *WebPushSubscriptionResponse {
	if subscription == nil {
		return nil
	}
	return &WebPushSubscriptionResponse{
		ID:            subscription.ID,
		DeviceLabel:   subscription.DeviceLabel,
		CreatedAt:     subscription.CreatedAt,
		LastSuccessAt: cloneTime(subscription.LastSuccessAt),
	}
}
