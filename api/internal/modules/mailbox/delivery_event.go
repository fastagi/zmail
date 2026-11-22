package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// MaxDeliveryEventBytes bounds one provider event accepted from the mail edge.
	MaxDeliveryEventBytes = 64 * 1024
	// deliveryEventScope takes the recipient's place in the signature, so a signed inbound
	// message can never be replayed as a delivery event or the other way round.
	deliveryEventScope = "delivery-event"

	providerEventPrefix = "cf.email.sending.message."
	maxEventDetailToken = 60
)

// providerEvent is the Cloudflare Email Sending event subscription payload, schema version 1.
// Only the fields Zmail stores are read; free-form provider text such as the SMTP response
// line is deliberately not.
type providerEvent struct {
	Type    string `json:"type"`
	Payload struct {
		EventID   string `json:"eventId"`
		MessageID string `json:"messageId"`
		Recipient string `json:"recipient"`
		Delivery  struct {
			SMTPStatusCode         json.Number `json:"smtpStatusCode"`
			SMTPEnhancedStatusCode string      `json:"smtpEnhancedStatusCode"`
		} `json:"delivery"`
		Bounce struct {
			Type           string `json:"type"`
			Classification string `json:"classification"`
		} `json:"bounce"`
		Failure struct {
			Reason string `json:"reason"`
		} `json:"failure"`
		Rejection struct {
			Reason string `json:"reason"`
		} `json:"rejection"`
		Complaint struct {
			Type string `json:"type"`
		} `json:"complaint"`
	} `json:"payload"`
	Metadata struct {
		EventTimestamp string `json:"eventTimestamp"`
	} `json:"metadata"`
}

var providerEventTypes = map[string]domain.MailEventType{
	"delivered":  domain.MailEventDelivered,
	"deferred":   domain.MailEventDeferred,
	"bounced":    domain.MailEventBounced,
	"failed":     domain.MailEventFailed,
	"rejected":   domain.MailEventRejected,
	"complained": domain.MailEventComplained,
}

// parseProviderEvent validates a provider event and returns the message it names and the
// delivery event to store.
func parseProviderEvent(raw []byte, now time.Time) (providerMessageID string, event domain.MailEvent, err error) {
	var parsed providerEvent
	if json.Unmarshal(raw, &parsed) != nil {
		return "", domain.MailEvent{}, ErrMailDeliveryEventInvalid
	}
	eventType, known := providerEventTypes[strings.TrimPrefix(parsed.Type, providerEventPrefix)]
	if !known || !strings.HasPrefix(parsed.Type, providerEventPrefix) {
		return "", domain.MailEvent{}, ErrMailDeliveryEventInvalid
	}
	messageID, ok := normalizeMessageID(parsed.Payload.MessageID)
	eventID := strings.TrimSpace(parsed.Payload.EventID)
	if !ok || messageID == "" || eventID == "" || len(eventID) > 128 || strings.ContainsAny(eventID, " \t\r\n") {
		return "", domain.MailEvent{}, ErrMailDeliveryEventInvalid
	}
	recipient, ok := normalizeRecipient(parsed.Payload.Recipient)
	if !ok {
		return "", domain.MailEvent{}, ErrMailDeliveryEventInvalid
	}

	occurredAt := now.UTC()
	if stamp, parseErr := time.Parse(time.RFC3339Nano, parsed.Metadata.EventTimestamp); parseErr == nil && !stamp.After(now.Add(time.Hour)) {
		occurredAt = stamp.UTC()
	}
	status := parsed.Payload.Delivery.SMTPEnhancedStatusCode
	if status == "" {
		status = parsed.Payload.Delivery.SMTPStatusCode.String()
	}
	return messageID, domain.MailEvent{
		Type:            eventType,
		Recipient:       recipient,
		ProviderEventID: eventID,
		OccurredAt:      occurredAt,
		Detail: eventDetail(
			status,
			parsed.Payload.Bounce.Classification,
			parsed.Payload.Failure.Reason,
			parsed.Payload.Rejection.Reason,
			parsed.Payload.Complaint.Type,
		),
	}, nil
}

// eventDetail joins the short machine-readable tokens of an event and drops anything else, so
// provider free text never reaches storage or clients.
func eventDetail(tokens ...string) string {
	kept := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" || len(token) > maxEventDetailToken || !isDetailToken(token) {
			continue
		}
		kept = append(kept, token)
	}
	return strings.Join(kept, " ")
}

func isDetailToken(token string) bool {
	for _, char := range token {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '.', char == '_', char == '-':
		default:
			return false
		}
	}
	return true
}

// Kinds of message a delivery event can match.
const (
	DeliveryEventKindMailbox       = "mailbox"
	DeliveryEventKindTransactional = "transactional"
)

// DeliveryEventResult says which message a provider event matched and whether it was new.
type DeliveryEventResult struct {
	// Kind is DeliveryEventKindMailbox or DeliveryEventKindTransactional.
	Kind string
	// MessageID is the id of the matched message in the table Kind names.
	MessageID uint
	Event     domain.MailEvent
	// Recorded is false when the event was already stored.
	Recorded bool
}

// RecordDeliveryEvent verifies and stores one provider event. The signature is checked before
// anything is parsed or looked up. Mailbox messages are searched first, then transactional ones.
func (s *service) RecordDeliveryEvent(ctx context.Context, delivery DeliveryEvent) (DeliveryEventResult, error) {
	if s.settings.InboundSecret == "" {
		return DeliveryEventResult{}, ErrMailInboundDisabled
	}
	if len(delivery.Raw) == 0 || len(delivery.Raw) > MaxDeliveryEventBytes {
		return DeliveryEventResult{}, ErrMailDeliveryEventInvalid
	}
	if !verifyInbound(s.settings.InboundSecret, delivery.Signature, deliveryEventScope, delivery.Raw, s.now(), s.settings.InboundTolerance) {
		return DeliveryEventResult{}, ErrMailInboundSignature
	}
	providerMessageID, event, err := parseProviderEvent(delivery.Raw, s.now())
	if err != nil {
		return DeliveryEventResult{}, err
	}
	message, mailbox, err := s.repo.FindOutboundMessageByProviderID(ctx, providerMessageID)
	if errors.Is(err, ErrMailMessageNotFound) && suppressesAddress(event.Type) {
		// A bounce or complaint for a message deleted since still counts in the sending-health
		// check; the message itself is gone, so the edge is still answered not found.
		found, _, keptErr := s.repo.RecordDeletedSendEvent(ctx, providerMessageID, event.Type)
		if keptErr != nil {
			return DeliveryEventResult{}, keptErr
		}
		if found {
			return DeliveryEventResult{}, ErrMailMessageNotFound
		}
	}
	if errors.Is(err, ErrMailMessageNotFound) && s.transactional != nil {
		return s.recordTransactionalDeliveryEvent(ctx, providerMessageID, event)
	}
	if err != nil {
		return DeliveryEventResult{}, err
	}
	event.MessageID = message.ID
	added := 0
	err = s.apply(ctx, func(tx context.Context) ([]MailNotification, error) {
		var summary domain.MailEventType
		var addErr error
		if added, summary, addErr = s.repo.AddMessageEvents(tx, message.ID, []domain.MailEvent{event}); addErr != nil {
			return nil, addErr
		}
		message.LastEvent = summary
		if added > 0 && suppressesAddress(event.Type) {
			// A permanent bounce or a complaint from a broadcast recipient suppresses that contact.
			if suppressErr := s.broadcasts.SuppressContactForMessage(tx, message.ID, string(event.Type)); suppressErr != nil {
				return nil, suppressErr
			}
			// It also puts the address on the organization's suppression list, which leaves the
			// contact's status to the rule above.
			if message.SendLane == domain.SendLaneBroadcast && message.OrganizationID != 0 && s.transactional != nil {
				if suppressErr := s.transactional.AddSuppression(tx, domain.MailSuppression{
					OrganizationID: message.OrganizationID, Email: event.Recipient, Reason: string(event.Type),
					Source: domain.MailSuppressionSourceBroadcast, CreatedAt: s.now().UTC(),
				}); suppressErr != nil {
					return nil, suppressErr
				}
			}
		}
		eventType, publish := webhookTypeForEvent(event.Type)
		if added == 0 || !publish {
			return nil, nil
		}
		// A message sealed by a handover still records its events; the webhook guard keeps them
		// from being published.
		notification := mailNotificationFor(mailbox, message)
		notification.Type = eventType
		notification.Recipient = event.Recipient
		notification.Reason = event.Detail
		notification.OccurredAt = event.OccurredAt
		notification.Key = "event:" + event.ProviderEventID
		return []MailNotification{notification}, nil
	})
	if err != nil {
		return DeliveryEventResult{}, err
	}
	return DeliveryEventResult{Kind: DeliveryEventKindMailbox, MessageID: message.ID, Event: event, Recorded: added > 0}, nil
}

// suppressesAddress reports whether an event puts its recipient on the suppression list: a
// permanent bounce or a complaint. A rejection by the provider's own list never does (D35).
func suppressesAddress(eventType domain.MailEventType) bool {
	return eventType == domain.MailEventBounced || eventType == domain.MailEventComplained
}

// recordTransactionalDeliveryEvent stores a provider event about a transactional message, updates
// its last_event, suppresses the address after a permanent bounce or a complaint, and publishes
// the matching webhook, all in one transaction and only for a new event.
func (s *service) recordTransactionalDeliveryEvent(ctx context.Context, providerMessageID string, event domain.MailEvent) (DeliveryEventResult, error) {
	message, err := s.transactional.FindTransactionalByProviderID(ctx, providerMessageID)
	if errors.Is(err, ErrTransactionalMessageNotFound) {
		return DeliveryEventResult{}, ErrMailMessageNotFound
	}
	if err != nil {
		return DeliveryEventResult{}, err
	}
	event.MessageID = message.ID
	added := 0
	err = s.applyTransactional(ctx, func(tx context.Context) ([]TransactionalNotification, error) {
		var summary domain.MailEventType
		var addErr error
		if added, summary, addErr = s.transactional.AddTransactionalEvents(tx, message.ID, []domain.MailEvent{event}); addErr != nil {
			return nil, addErr
		}
		if added == 0 {
			return nil, nil
		}
		// Only mail that reached the provider can teach anything about the address; simulated and
		// test-mode messages never write the list.
		if suppressesAddress(event.Type) && message.CountsInLane() {
			if suppressErr := s.transactional.AddSuppression(tx, domain.MailSuppression{
				OrganizationID: message.OrganizationID, Email: message.ToAddress, Reason: string(event.Type),
				Source: domain.MailSuppressionSourceTransactional, CreatedAt: s.now().UTC(),
			}); suppressErr != nil {
				return nil, suppressErr
			}
		}
		notification, publish := transactionalNotificationForEvent(event)
		if !publish {
			return nil, nil
		}
		final := *message
		final.LastEvent = summary
		notification.Message = &final
		return []TransactionalNotification{notification}, nil
	})
	if err != nil {
		return DeliveryEventResult{}, err
	}
	if added > 0 {
		logTransactional(ctx, "transactional_message.event_recorded", message, slog.String("event", string(event.Type)))
	}
	return DeliveryEventResult{Kind: DeliveryEventKindTransactional, MessageID: message.ID, Event: event, Recorded: added > 0}, nil
}

// transactionalNotificationForEvent maps a provider event to its transactional webhook (spec
// §9.16): a deferral is a temporary bounce and a rejection is email.suppressed.
func transactionalNotificationForEvent(event domain.MailEvent) (TransactionalNotification, bool) {
	notification := TransactionalNotification{
		Reason: event.Detail, Key: "event:" + event.ProviderEventID, OccurredAt: event.OccurredAt,
	}
	switch event.Type {
	case domain.MailEventDelivered:
		notification.Type = WebhookEmailDelivered
	case domain.MailEventBounced:
		notification.Type, notification.BounceType = WebhookEmailBounced, bounceTypePermanent
	case domain.MailEventDeferred:
		notification.Type, notification.BounceType = WebhookEmailBounced, bounceTypeTemporary
	case domain.MailEventComplained:
		notification.Type = WebhookEmailComplained
	case domain.MailEventRejected:
		notification.Type = WebhookEmailSuppressed
	case domain.MailEventFailed:
		notification.Type = WebhookEmailFailed
	default:
		return TransactionalNotification{}, false
	}
	return notification, true
}
