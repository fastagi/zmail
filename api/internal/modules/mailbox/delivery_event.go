package mailbox

import (
	"context"
	"encoding/json"
	"errors"
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

// RecordDeliveryEvent verifies and stores one provider event. The signature is checked before
// anything is parsed or looked up.
func (s *service) RecordDeliveryEvent(
	ctx context.Context,
	delivery DeliveryEvent,
) (*domain.MailMessage, domain.MailEvent, bool, error) {
	if s.settings.InboundSecret == "" {
		return nil, domain.MailEvent{}, false, ErrMailInboundDisabled
	}
	if len(delivery.Raw) == 0 || len(delivery.Raw) > MaxDeliveryEventBytes {
		return nil, domain.MailEvent{}, false, ErrMailDeliveryEventInvalid
	}
	if !verifyInbound(s.settings.InboundSecret, delivery.Signature, deliveryEventScope, delivery.Raw, s.now(), s.settings.InboundTolerance) {
		return nil, domain.MailEvent{}, false, ErrMailInboundSignature
	}
	providerMessageID, event, err := parseProviderEvent(delivery.Raw, s.now())
	if err != nil {
		return nil, domain.MailEvent{}, false, err
	}
	message, err := s.repo.FindOutboundMessageByProviderID(ctx, providerMessageID)
	if err != nil {
		return nil, domain.MailEvent{}, false, err
	}
	added, summary, err := s.repo.AddMessageEvents(ctx, message.ID, []domain.MailEvent{event})
	if err != nil {
		if errors.Is(err, ErrMailMessageNotFound) {
			return nil, domain.MailEvent{}, false, ErrMailMessageNotFound
		}
		return nil, domain.MailEvent{}, false, err
	}
	message.LastEvent = summary
	event.MessageID = message.ID
	return message, event, added > 0, nil
}
