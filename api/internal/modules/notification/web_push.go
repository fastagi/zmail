package notification

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/webpush"
)

const (
	// maxWebPushSubscriptionsPerUser bounds a user's devices; registering one more replaces the
	// least recently registered.
	maxWebPushSubscriptionsPerUser = 20
	// maxWebPushRejections removes a subscription the push service refused this many times in a row.
	maxWebPushRejections  = int16(3)
	maxWebPushDeviceLabel = 120
	// webPushSendBudget keeps one delivery's sends well inside its lease, so a slow push service
	// cannot let a second worker claim the same delivery while the first still sends.
	webPushSendBudget = defaultDeliveryLease * 3 / 4
	webPushTTL        = 24 * time.Hour
	webPushUrgency    = "normal"

	failureWebPushNotConfigured   = "WEB_PUSH.NOT_CONFIGURED"
	failureWebPushNoSubscriptions = "WEB_PUSH.NO_SUBSCRIPTIONS"
	failureWebPushRejected        = "WEB_PUSH.REJECTED"
	failureWebPushUnavailable     = "WEB_PUSH.PUSH_SERVICE_UNAVAILABLE"
	failureWebPushRetryExhausted  = "WEB_PUSH.RETRY_EXHAUSTED"
	failureWebPushRecipient       = "WEB_PUSH.RECIPIENT_UNAVAILABLE"
	failureWebPushInvalidMessage  = "WEB_PUSH.INVALID_MESSAGE"
)

// webPushSender sends one encrypted push; *webpush.Client implements it and tests fake it.
type webPushSender interface {
	PublicKey() string
	Send(context.Context, webpush.Message) (webpush.Result, error)
}

var _ webPushSender = (*webpush.Client)(nil)

// WebPushConfig tells browsers whether they can subscribe and with which application server key.
type WebPushConfig struct {
	Enabled   bool
	PublicKey string
}

// WebPushSubscriptionInput is a browser's PushSubscription as the client sends it.
type WebPushSubscriptionInput struct {
	Endpoint    string
	P256dh      string
	Auth        string
	DeviceLabel string
}

// WebPushSubscription is a registered device as its owner sees it: never the endpoint or keys.
type WebPushSubscription struct {
	ID            uint
	DeviceLabel   string
	LastSuccessAt *time.Time
	CreatedAt     time.Time
}

// ProvideWebPushSender returns the push client when the VAPID configuration is complete and valid,
// and nil otherwise. A partial or invalid configuration logs one warning without its values.
func ProvideWebPushSender(cfg *config.Config) webPushSender {
	if cfg == nil {
		return nil
	}
	settings := cfg.WebPush
	if !settings.Configured() {
		if settings.PartiallyConfigured() {
			slog.Warn("notification.web_push_disabled",
				"reason", "WEB_PUSH_VAPID_PUBLIC_KEY, WEB_PUSH_VAPID_PRIVATE_KEY, and WEB_PUSH_SUBJECT must be set together")
		}
		return nil
	}
	vapid, err := webpush.NewVAPID(settings.VAPIDPublicKey, settings.VAPIDPrivateKey, settings.Subject)
	if err != nil {
		slog.Warn("notification.web_push_disabled", "reason", err.Error())
		return nil
	}
	return webpush.NewClient(vapid, nil)
}

func (s *service) WebPushConfig() WebPushConfig {
	if s == nil || s.push == nil {
		return WebPushConfig{}
	}
	return WebPushConfig{Enabled: true, PublicKey: s.push.PublicKey()}
}

func (s *service) RegisterWebPushSubscription(
	ctx context.Context,
	userID uint,
	input WebPushSubscriptionInput,
) (*WebPushSubscription, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if s.push == nil {
		return nil, ErrWebPushDisabled
	}
	if userID == 0 {
		return nil, domain.ErrInvalidInput
	}
	registration, err := normalizeWebPushSubscription(input)
	if err != nil {
		return nil, err
	}
	registration.UserID = userID
	registration.At = s.now()
	return s.store.UpsertWebPushSubscription(ctx, registration, maxWebPushSubscriptionsPerUser)
}

func (s *service) ListWebPushSubscriptions(ctx context.Context, userID uint) ([]*WebPushSubscription, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if userID == 0 {
		return nil, domain.ErrInvalidInput
	}
	return s.store.ListWebPushSubscriptions(ctx, userID)
}

func (s *service) DeleteWebPushSubscription(ctx context.Context, userID, id uint) error {
	if err := s.available(); err != nil {
		return err
	}
	if userID == 0 || id == 0 {
		return domain.ErrInvalidInput
	}
	deleted, err := s.store.DeleteWebPushSubscription(ctx, userID, id)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrWebPushSubscriptionNotFound
	}
	return nil
}

func normalizeWebPushSubscription(input WebPushSubscriptionInput) (webPushRegistration, error) {
	endpoint := strings.TrimSpace(input.Endpoint)
	if containsControl(endpoint) || !webpush.AllowedEndpoint(endpoint) {
		return webPushRegistration{}, ErrWebPushEndpointInvalid
	}
	p256dh, err := decodeWebPushKey(input.P256dh)
	if err != nil || len(p256dh) != 65 {
		return webPushRegistration{}, ErrWebPushKeysInvalid
	}
	if _, err := ecdh.P256().NewPublicKey(p256dh); err != nil {
		return webPushRegistration{}, ErrWebPushKeysInvalid
	}
	auth, err := decodeWebPushKey(input.Auth)
	if err != nil || len(auth) != 16 {
		return webPushRegistration{}, ErrWebPushKeysInvalid
	}
	label := strings.TrimSpace(input.DeviceLabel)
	if label != "" && !validBoundedText(label, maxWebPushDeviceLabel) || containsControl(label) {
		return webPushRegistration{}, domain.ErrInvalidInput
	}
	return webPushRegistration{
		Endpoint:     endpoint,
		EndpointHash: hashEndpoint(endpoint),
		P256dh:       base64.RawURLEncoding.EncodeToString(p256dh),
		Auth:         base64.RawURLEncoding.EncodeToString(auth),
		DeviceLabel:  label,
	}, nil
}

// decodeWebPushKey accepts base64url, which browsers produce, and standard base64, with or without
// padding.
func decodeWebPushKey(value string) ([]byte, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "=")
	if value == "" || len(value) > 256 {
		return nil, ErrWebPushKeysInvalid
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func hashEndpoint(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])
}

// dispatchWebPush sends one delivery to every subscription of its user. It is delivered when at
// least one device accepted it, retried when every failure was transient, and failed otherwise.
// Endpoints, keys, and payload text are never logged.
func (s *service) dispatchWebPush(ctx context.Context, delivery claimedEmailDelivery) (bool, error) {
	if delivery.RecipientDeletedAt != nil {
		return s.completePermanent(ctx, delivery, s.now(), failureWebPushRecipient)
	}
	if s.push == nil {
		return s.completePermanent(ctx, delivery, s.now(), failureWebPushNotConfigured)
	}
	targets, err := s.store.ListWebPushTargets(ctx, delivery.UserID)
	if err != nil {
		return false, err
	}
	if len(targets) == 0 {
		return s.completePermanent(ctx, delivery, s.now(), failureWebPushNoSubscriptions)
	}
	payload, err := webPushPayload(delivery)
	if err != nil {
		return s.completePermanent(ctx, delivery, s.now(), failureWebPushInvalidMessage)
	}

	sendCtx, cancel := context.WithTimeout(ctx, webPushSendBudget)
	defer cancel()
	topic := webPushTag(delivery.NotificationID)
	delivered, transient, rejected := 0, 0, 0
	for _, target := range targets {
		if sendCtx.Err() != nil {
			transient++
			continue
		}
		result, _ := s.push.Send(sendCtx, webpush.Message{
			Endpoint: target.Endpoint,
			Keys:     webpush.Keys{P256dh: target.P256dh, Auth: target.Auth},
			Payload:  payload,
			TTL:      webPushTTL,
			Topic:    topic,
			Urgency:  webPushUrgency,
		})
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		switch result {
		case webpush.Delivered:
			delivered++
			err = s.store.RecordWebPushDelivered(ctx, target.ID, s.now())
		case webpush.Gone:
			err = s.store.RemoveWebPushSubscription(ctx, target.ID)
		case webpush.Rejected:
			rejected++
			err = s.store.RecordWebPushRejected(ctx, target.ID, maxWebPushRejections)
		default:
			transient++
		}
		if err != nil {
			return false, err
		}
	}

	switch {
	case delivered > 0:
		return s.store.CompleteEmail(ctx, delivery.ID, delivery.LeaseToken, deliveryCompletion{
			Status:      deliveryStatusDelivered,
			CompletedAt: s.now(),
		})
	case transient > 0 && delivery.Attempts >= maxDeliveryAttempts:
		return s.completePermanent(ctx, delivery, s.now(), failureWebPushRetryExhausted)
	case transient > 0:
		retryAt := s.now().Add(deliveryRetryDelay(delivery.Attempts))
		return s.store.CompleteEmail(ctx, delivery.ID, delivery.LeaseToken, deliveryCompletion{
			Status:      deliveryStatusPending,
			CompletedAt: s.now(),
			RetryAt:     &retryAt,
			FailureCode: failureWebPushUnavailable,
		})
	case rejected > 0:
		return s.completePermanent(ctx, delivery, s.now(), failureWebPushRejected)
	default:
		// Every subscription was gone.
		return s.completePermanent(ctx, delivery, s.now(), failureWebPushNoSubscriptions)
	}
}

func webPushTag(notificationID uint) string {
	return fmt.Sprintf("n-%d", notificationID)
}

type webPushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
}

// webPushPayload is the JSON the service worker shows. The body is cut, with an ellipsis, until the
// payload fits webpush.MaxPayloadBytes.
func webPushPayload(delivery claimedEmailDelivery) ([]byte, error) {
	message := webPushMessage{
		Title: delivery.Title,
		Body:  delivery.Body,
		URL:   delivery.ActionURL,
		Tag:   webPushTag(delivery.NotificationID),
	}
	if message.URL == "" {
		message.URL = "/"
	}
	runes := []rune(message.Body)
	for {
		encoded, err := encodeWebPushMessage(message)
		if err != nil {
			return nil, err
		}
		excess := len(encoded) - webpush.MaxPayloadBytes
		if excess <= 0 {
			return encoded, nil
		}
		if len(runes) == 0 {
			return nil, errors.New("web push payload exceeds the limit without a body")
		}
		// Every rune is at least one byte, so dropping excess runes drops at least excess bytes;
		// the ellipsis costs three more, covered by the extra runes.
		runes = runes[:max(0, len(runes)-excess-utf8.UTFMax)]
		message.Body = strings.TrimSpace(string(runes)) + "…"
	}
}

func encodeWebPushMessage(message webPushMessage) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(message); err != nil {
		return nil, fmt.Errorf("encode web push payload: %w", err)
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}
