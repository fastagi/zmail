package mailbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/events"
)

// eventBusMailPublisher adapts events.EventBus to the narrow domain.MailEventPublisher seam.
type eventBusMailPublisher struct {
	bus *events.EventBus
}

// NewMailEventPublisher returns a domain.MailEventPublisher backed by the given event bus.
// A nil bus produces a no-op publisher so callers without an event bus stay safe.
func NewMailEventPublisher(bus *events.EventBus) domain.MailEventPublisher {
	return &eventBusMailPublisher{bus: bus}
}

// PublishMailReceived publishes a MailReceivedEvent onto the underlying event bus.
func (p *eventBusMailPublisher) PublishMailReceived(ctx context.Context, event domain.MailReceivedEvent) error {
	if p == nil || p.bus == nil {
		return nil
	}
	return p.bus.Publish(ctx, event)
}

// SubscribeMailReceived registers a typed handler for domain.EventMailReceived on bus.
// Returns nil when bus or handler is nil.
func SubscribeMailReceived(
	bus *events.EventBus,
	handler func(context.Context, domain.MailReceivedEvent) error,
	opts ...events.SubscribeOption,
) events.Subscription {
	if bus == nil || handler == nil {
		return nil
	}
	return bus.Subscribe(domain.EventMailReceived, func(ctx context.Context, event events.Event) error {
		received, ok := unwrap[domain.MailReceivedEvent](event)
		if !ok {
			return nil
		}
		return handler(ctx, received)
	}, opts...)
}

// newMailReceivedEvent builds a domain.MailReceivedEvent from a stored mailbox and message.
// Returns false when required identifiers are missing.
func newMailReceivedEvent(mailbox *domain.Mailbox, message *domain.MailMessage, receivedAt time.Time) (domain.MailReceivedEvent, bool) {
	if mailbox == nil || message == nil || message.ID == 0 {
		return domain.MailReceivedEvent{}, false
	}
	if receivedAt.IsZero() {
		receivedAt = message.CreatedAt
	}
	mailboxID := mailbox.ID
	if mailboxID == 0 {
		mailboxID = message.MailboxID
	}
	return domain.NewMailReceivedEvent(
		message.ID,
		mailboxID,
		mailbox.OrganizationID,
		message.Direction,
		message.FromAddress,
		message.Subject,
		receivedAt.UTC(),
	), true
}

// publishInboundEvent emits a domain.MailReceivedEvent after an inbound message is stored.
// Subscriber errors are logged without message bodies and never fail inbound delivery.
func (s *service) publishInboundEvent(ctx context.Context, mailbox *domain.Mailbox, message *domain.MailMessage) {
	if s == nil || s.mailEvents == nil {
		return
	}
	var receivedAt time.Time
	if s.now != nil {
		receivedAt = s.now().UTC()
	}
	event, ok := newMailReceivedEvent(mailbox, message, receivedAt)
	if !ok {
		return
	}
	if err := s.mailEvents.PublishMailReceived(ctx, event); err != nil {
		slog.WarnContext(ctx, "mailbox inbound event publication failed",
			slog.String("event", event.EventName()),
			slog.Uint64("organization_id", uint64(event.OrganizationID)),
			slog.Uint64("mailbox_id", uint64(event.MailboxID)),
			slog.Uint64("message_id", uint64(event.MessageID)),
			slog.String("error", err.Error()))
	}
}
