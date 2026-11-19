package mailbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/modules/webhook"
)

// Webhook event types the mailbox starter publishes to an organization's endpoints.
const (
	// WebhookMailSent: the provider took an outbound message.
	WebhookMailSent = "mail.message.sent"
	// WebhookMailFailed: the provider refused an outbound message, or later gave up on a recipient.
	WebhookMailFailed = "mail.message.failed"
	// WebhookMailReceived: an inbound message was stored.
	WebhookMailReceived = "mail.message.received"
	// WebhookMailDelivered: a recipient's mail server accepted the message.
	WebhookMailDelivered = "mail.message.delivered"
	// WebhookMailDeliveryDelayed: a temporary failure; the provider is still retrying.
	WebhookMailDeliveryDelayed = "mail.message.delivery_delayed"
	// WebhookMailBounced: a recipient's mail server refused the message for good.
	WebhookMailBounced = "mail.message.bounced"
	// WebhookMailComplained: a recipient reported the message as spam.
	WebhookMailComplained = "mail.message.complained"
	// WebhookMailSuppressed: the provider did not attempt a recipient on its suppression list.
	WebhookMailSuppressed = "mail.message.suppressed"

	webhookSource = "mailbox"
)

var mailWebhookTypes = []string{
	WebhookMailSent, WebhookMailFailed, WebhookMailReceived, WebhookMailDelivered,
	WebhookMailDeliveryDelayed, WebhookMailBounced, WebhookMailComplained, WebhookMailSuppressed,
}

// webhookTypeForEvent maps a delivery event to the webhook it publishes. An accepted event
// publishes nothing of its own: mail.message.sent already says the provider took the message.
func webhookTypeForEvent(eventType domain.MailEventType) (string, bool) {
	switch eventType {
	case domain.MailEventDelivered:
		return WebhookMailDelivered, true
	case domain.MailEventDeferred:
		return WebhookMailDeliveryDelayed, true
	case domain.MailEventBounced:
		return WebhookMailBounced, true
	case domain.MailEventFailed:
		return WebhookMailFailed, true
	case domain.MailEventRejected:
		return WebhookMailSuppressed, true
	case domain.MailEventComplained:
		return WebhookMailComplained, true
	default:
		return "", false
	}
}

// mailWebhookPayload is the exact `data` of every mail webhook. It carries identifiers and
// addresses so a receiver can find the message through the API; it never carries a subject or
// a body.
type mailWebhookPayload struct {
	MessageID      uint     `json:"message_id"`
	MailboxID      uint     `json:"mailbox_id"`
	MailboxAddress string   `json:"mailbox_address"`
	Direction      string   `json:"direction"`
	From           string   `json:"from"`
	To             []string `json:"to"`
	// Recipient is the address a per-recipient event is about; empty for a message-level event.
	Recipient string `json:"recipient"`
	// Reason is a short machine-readable code; empty when there is nothing to explain.
	Reason string `json:"reason"`
}

var errMailWebhookPayload = errors.New("mail webhook payload is invalid")

func validateMailWebhookPayload(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload mailWebhookPayload
	if err := decoder.Decode(&payload); err != nil {
		return errMailWebhookPayload
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errMailWebhookPayload
	}
	direction := domain.MailDirection(payload.Direction)
	if payload.MessageID == 0 || payload.MailboxID == 0 || payload.MailboxAddress == "" || !direction.Valid() ||
		payload.From == "" || payload.To == nil || len(payload.To) > domain.MaxMailRecipients ||
		len(payload.Reason) > 255 || len(payload.Recipient) > 320 {
		return errMailWebhookPayload
	}
	return nil
}

// NewWebhookCatalog is the product's webhook event catalog: the webhook starter's own
// definitions plus the mail events.
func NewWebhookCatalog() (*webhook.Catalog, error) {
	definitions := webhook.DefaultDefinitions()
	for _, eventType := range mailWebhookTypes {
		definitions = append(definitions, webhook.Definition{Type: eventType, ValidatePayload: validateMailWebhookPayload})
	}
	return webhook.NewCatalog(definitions...)
}

// MailNotification is one mail event to tell an organization's webhook endpoints about.
type MailNotification struct {
	Type           string
	OrganizationID uint
	MailboxAddress string
	// MailboxType, WebhooksEnabled, and SealedBefore describe the mailbox when the change was
	// stored; they decide whether the notification may be published at all.
	MailboxType     domain.MailboxType
	WebhooksEnabled bool
	SealedBefore    *time.Time
	Message         *domain.MailMessage
	Recipient       string
	Reason          string
	OccurredAt      time.Time
	// Key distinguishes several notifications of one type for one message, such as one per
	// recipient or per provider event. Empty for a notification that happens once per message.
	Key string
}

// mailNotifier publishes mail notifications. Called with a transaction context, the publication
// commits or rolls back with the state change it describes.
type mailNotifier interface {
	Notify(ctx context.Context, notification MailNotification) error
	NotifyTransactional(ctx context.Context, notification TransactionalNotification) error
}

// webhookNotifier publishes through the webhook starter when that starter is selected and does
// nothing otherwise, so the mailbox starter works with or without it.
type webhookNotifier struct {
	publisher domain.WebhookPublisher
	enabled   bool
	now       func() time.Time
}

// NewWebhookNotifier builds the notifier from typed configuration.
func NewWebhookNotifier(cfg *config.Config, publisher domain.WebhookPublisher) *webhookNotifier {
	return &webhookNotifier{
		publisher: publisher,
		enabled:   cfg != nil && publisher != nil && cfg.Starters.Selected(config.StarterWebhook),
		now:       time.Now,
	}
}

func (n *webhookNotifier) Notify(ctx context.Context, notification MailNotification) error {
	if n == nil || !n.enabled {
		return nil
	}
	// An unowned mailbox has no organization whose endpoints could be told.
	if notification.OrganizationID == 0 || notification.Message == nil {
		return nil
	}
	message := notification.Message
	// Only a shared mailbox with its switch on publishes, and never about a sealed message.
	if notification.MailboxType != domain.MailboxTypeShared || !notification.WebhooksEnabled ||
		(notification.SealedBefore != nil && message.CreatedAt.Before(*notification.SealedBefore)) {
		return nil
	}
	to := message.To
	if to == nil {
		to = []string{}
	}
	data, err := json.Marshal(mailWebhookPayload{
		MessageID:      message.ID,
		MailboxID:      message.MailboxID,
		MailboxAddress: notification.MailboxAddress,
		Direction:      string(message.Direction),
		From:           message.FromAddress,
		To:             to,
		Recipient:      notification.Recipient,
		Reason:         notification.Reason,
	})
	if err != nil {
		return fmt.Errorf("encode mail webhook: %w", err)
	}
	_, err = n.publisher.PublishWebhook(ctx, domain.WebhookEvent{
		OrganizationID: notification.OrganizationID,
		Source:         webhookSource,
		EventID:        mailWebhookEventID(message.ID, notification.Type, notification.Key),
		Type:           notification.Type,
		OccurredAt:     n.publishableTime(notification.OccurredAt),
		Data:           data,
	})
	return err
}

// NotifyTransactional publishes a transactional mail event. Transactional webhooks are not
// published yet, so it does nothing.
func (n *webhookNotifier) NotifyTransactional(_ context.Context, _ TransactionalNotification) error {
	return nil
}

// publishableTime keeps a provider timestamp inside the window the webhook starter accepts.
func (n *webhookNotifier) publishableTime(occurredAt time.Time) time.Time {
	now := n.now().UTC()
	if occurredAt.IsZero() || occurredAt.After(now.Add(time.Hour)) || occurredAt.Before(now.AddDate(0, 0, -29)) {
		return now
	}
	return occurredAt.UTC()
}

// mailWebhookEventID is stable for one message, type, and key, so publishing the same event
// twice is recognized by the webhook starter. It uses only the characters that starter allows.
func mailWebhookEventID(messageID uint, eventType, key string) string {
	id := "m" + strconv.FormatUint(uint64(messageID), 10) + ":" + eventType[len("mail.message."):]
	if key == "" {
		return id
	}
	sum := sha256.Sum256([]byte(key))
	return id + ":" + hex.EncodeToString(sum[:12])
}
