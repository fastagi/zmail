package domain

import (
	"context"
	"time"
)

// Mail domain events published after the underlying message state change commits.
const (
	EventMailReceived = "mail.received"
)

// MailReceivedEvent reports that an inbound message has been stored for a mailbox.
// Downstream subscribers can listen to this event without coupling the core inbound
// delivery path directly to their implementations.
type MailReceivedEvent struct {
	MessageID      uint
	MailboxID      uint
	OrganizationID uint
	Direction      MailDirection
	FromAddress    string
	Subject        string
	ReceivedAt     time.Time
}

// NewMailReceivedEvent builds a MailReceivedEvent for a newly stored inbound message.
// When receivedAt is zero, it defaults to the current UTC time.
func NewMailReceivedEvent(
	messageID uint,
	mailboxID uint,
	organizationID uint,
	direction MailDirection,
	fromAddress string,
	subject string,
	receivedAt time.Time,
) MailReceivedEvent {
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	return MailReceivedEvent{
		MessageID:      messageID,
		MailboxID:      mailboxID,
		OrganizationID: organizationID,
		Direction:      direction,
		FromAddress:    fromAddress,
		Subject:        subject,
		ReceivedAt:     receivedAt,
	}
}

// EventName returns the canonical dotted event name for MailReceivedEvent.
func (e MailReceivedEvent) EventName() string { return EventMailReceived }

// OccurredAt returns the timestamp when the inbound message was received.
func (e MailReceivedEvent) OccurredAt() time.Time { return e.ReceivedAt }

// MailEventPublisher is the narrow domain seam used by the mailbox core to publish
// mail lifecycle events without depending on a specific event bus implementation.
type MailEventPublisher interface {
	PublishMailReceived(ctx context.Context, event MailReceivedEvent) error
}
