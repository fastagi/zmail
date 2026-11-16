package domain

import (
	"context"
	"time"
)

// Mailbox limits shared by the service, persistence, and the HTTP contract.
const (
	// MaxMailboxesPerOrganization bounds how many addresses one organization may own.
	MaxMailboxesPerOrganization = 50
	// MaxMailRecipients matches the provider limit across to and cc.
	MaxMailRecipients = 50
	// MaxMailSubjectLength is the RFC 5322 line limit for a subject.
	MaxMailSubjectLength = 998
	// MaxMailBodyBytes bounds each stored body representation.
	MaxMailBodyBytes = 256 * 1024
	// MaxMailAttachments bounds the attachments of one message.
	MaxMailAttachments = 10
	// MaxMailAttachmentBytes bounds the decoded size of all attachments of one message. It is
	// larger than what a maximum-size inbound message can carry, so inbound attachments are
	// never cut by size.
	MaxMailAttachmentBytes = 4 * 1024 * 1024
	// MaxMailAttachmentNameLength bounds a stored file name.
	MaxMailAttachmentNameLength = 255
	// MaxMailTemplatesPerOrganization bounds how many templates one organization keeps.
	MaxMailTemplatesPerOrganization = 200
)

// MailDirection tells whether a message arrived at or left a mailbox.
type MailDirection string

const (
	MailDirectionInbound  MailDirection = "inbound"
	MailDirectionOutbound MailDirection = "outbound"
)

// Valid reports whether the direction is a known value.
func (d MailDirection) Valid() bool {
	return d == MailDirectionInbound || d == MailDirectionOutbound
}

// MailStatus is the delivery state of one message.
type MailStatus string

const (
	// MailStatusReceived marks an inbound message accepted from the mail edge.
	MailStatusReceived MailStatus = "received"
	// MailStatusQueued marks an outbound message whose provider call has not finished.
	MailStatusQueued MailStatus = "queued"
	// MailStatusSent marks an outbound message the provider accepted.
	MailStatusSent MailStatus = "sent"
	// MailStatusFailed marks an outbound message the provider rejected or could not take.
	MailStatusFailed MailStatus = "failed"
)

// Valid reports whether the status is a known value.
func (s MailStatus) Valid() bool {
	switch s {
	case MailStatusReceived, MailStatusQueued, MailStatusSent, MailStatusFailed:
		return true
	default:
		return false
	}
}

// MailEventType is one step in the delivery of an outbound message, as the provider reported it.
type MailEventType string

const (
	// MailEventAccepted: the provider took the message for delivery.
	MailEventAccepted MailEventType = "accepted"
	// MailEventDelivered: the recipient's mail server accepted the message.
	MailEventDelivered MailEventType = "delivered"
	// MailEventDeferred: a temporary failure; the provider is still retrying.
	MailEventDeferred MailEventType = "deferred"
	// MailEventBounced: the recipient's mail server refused the message for good.
	MailEventBounced MailEventType = "bounced"
	// MailEventFailed: the provider gave up without a bounce.
	MailEventFailed MailEventType = "failed"
	// MailEventRejected: the provider did not attempt the recipient, for example a suppressed one.
	MailEventRejected MailEventType = "rejected"
	// MailEventComplained: the recipient reported the message as spam.
	MailEventComplained MailEventType = "complained"
)

// severity orders event types by how much attention they need; the summary of a message is
// the most severe current state among its recipients.
func (t MailEventType) severity() int {
	switch t {
	case MailEventComplained:
		return 6
	case MailEventBounced:
		return 5
	case MailEventFailed:
		return 4
	case MailEventRejected:
		return 3
	case MailEventDeferred:
		return 2
	case MailEventDelivered:
		return 1
	default:
		return 0
	}
}

// Valid reports whether the type is a known value.
func (t MailEventType) Valid() bool {
	return t == MailEventAccepted || t.severity() > 0
}

// MailEvent is one recorded delivery step of a message.
type MailEvent struct {
	ID        uint
	MessageID uint
	Type      MailEventType
	// Recipient is the address the event is about; empty for a message-level event.
	Recipient string
	// Detail is a short machine-readable reason such as "5.1.1 permanent_failure"; never free text.
	Detail string
	// ProviderEventID makes a provider event idempotent. Empty for events recorded at send time.
	ProviderEventID string
	OccurredAt      time.Time
}

// SummarizeMailEvents returns the state a listing shows for a message: the most severe current
// state among its recipients, where a recipient's current state is its latest event. With no
// recipient events it is "accepted" when the provider took the message, and empty otherwise.
func SummarizeMailEvents(events []MailEvent) MailEventType {
	latest := map[string]MailEvent{}
	accepted := false
	for _, event := range events {
		if event.Recipient == "" {
			accepted = accepted || event.Type == MailEventAccepted
			continue
		}
		current, seen := latest[event.Recipient]
		if !seen || event.OccurredAt.After(current.OccurredAt) ||
			(event.OccurredAt.Equal(current.OccurredAt) && event.ID >= current.ID) {
			latest[event.Recipient] = event
		}
	}
	var summary MailEventType
	for _, event := range latest {
		if summary == "" || event.Type.severity() > summary.severity() {
			summary = event.Type
		}
	}
	if summary == "" && accepted {
		return MailEventAccepted
	}
	return summary
}

// Mailbox is one address an organization sends from and receives at. Every member of the
// organization may read and send its mail.
type Mailbox struct {
	ID             uint
	OrganizationID uint
	// CreatedBy is the user who created the mailbox. It grants nothing.
	CreatedBy   uint
	Address     string
	DisplayName string
	// UnreadCount is the number of unread inbound messages. Only listings fill it.
	UnreadCount int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MailMessage is one stored inbound or outbound message.
type MailMessage struct {
	ID          uint
	MailboxID   uint
	Direction   MailDirection
	Status      MailStatus
	FromAddress string
	FromName    string
	To          []string
	Cc          []string
	Subject     string
	TextBody    string
	HTMLBody    string
	// MessageID is the RFC 5322 Message-ID without angle brackets. Inbound messages carry the
	// sender's value; outbound messages get one only when the provider reports it.
	MessageID string
	InReplyTo string
	// DedupeKey makes inbound delivery idempotent for one mailbox. Empty for outbound.
	DedupeKey   string
	FailureCode string
	// IdempotencyKey names an outbound message chosen by the sender; a later send with the same
	// key in the same mailbox finds this message instead of sending again. Empty when unused.
	IdempotencyKey string
	// IdempotencyFingerprint is a hash of the send request, so a reused key with a different
	// request is refused instead of answered with an unrelated message.
	IdempotencyFingerprint string
	ReadAt                 *time.Time
	// MailboxAddress is the address of the owning mailbox. Only the cross-mailbox log and the
	// provider-id lookup fill it.
	MailboxAddress string
	// OrganizationID is the owner of the mailbox; zero for an unowned mailbox. Only the
	// provider-id lookup fills it.
	OrganizationID uint
	// LastEvent summarizes the delivery events of an outbound message; see SummarizeMailEvents.
	LastEvent MailEventType
	// Events is the delivery history, oldest first. Only the detail lookup fills it.
	Events []MailEvent
	// TemplateID names the template the message was rendered from; zero when none was used.
	TemplateID uint
	// AttachmentCount is how many attachments the message has.
	AttachmentCount int
	// Attachments carries content when a message is being stored and metadata only when the
	// detail lookup fills it.
	Attachments []MailAttachment
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MailAttachment is one file attached to a message.
type MailAttachment struct {
	ID        uint
	MessageID uint
	// Filename is a bare file name without any path.
	Filename string
	// ContentType is the media type the sender declared; it is never trusted for rendering.
	ContentType string
	Size        int
	// Content is empty when only metadata was loaded.
	Content []byte
}

// MailTemplate is reusable message content with variables, owned by an organization.
type MailTemplate struct {
	ID             uint
	OrganizationID uint
	Name           string
	Subject        string
	HTMLBody       string
	TextBody       string
	// SampleVariables are example values used by previews and test sends.
	SampleVariables map[string]string
	CreatedBy       uint
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// MailTemplateRepository stores templates. Every lookup is scoped to an organization.
type MailTemplateRepository interface {
	// CreateTemplate stores a template unless the organization already has limit templates.
	CreateTemplate(ctx context.Context, template *MailTemplate, limit int) error
	ListTemplates(ctx context.Context, organizationID uint) ([]*MailTemplate, error)
	FindTemplate(ctx context.Context, organizationID, id uint) (*MailTemplate, error)
	UpdateTemplate(ctx context.Context, template *MailTemplate) error
	DeleteTemplate(ctx context.Context, organizationID, id uint) error
}

// MailMessageFilter narrows a mailbox message listing.
type MailMessageFilter struct {
	Direction MailDirection
	Page      int
	PerPage   int
}

// MailLogFilter narrows the log of every message in every mailbox of an organization.
type MailLogFilter struct {
	Direction MailDirection
	Status    MailStatus
	MailboxID uint
	// Query matches the subject, the sender address, or a recipient address, ignoring case.
	Query   string
	Page    int
	PerPage int
}

// MailDayStats counts one UTC day of an organization's mail.
type MailDayStats struct {
	Day      time.Time
	Sent     int
	Failed   int
	Received int
}

// MailboxRepository defines persistence for mailboxes and their messages.
type MailboxRepository interface {
	CreateMailbox(ctx context.Context, mailbox *Mailbox, limit int) error
	ListMailboxes(ctx context.Context, organizationID uint) ([]*Mailbox, error)
	FindMailbox(ctx context.Context, organizationID, id uint) (*Mailbox, error)
	FindMailboxByAddress(ctx context.Context, address string) (*Mailbox, error)
	DeleteMailbox(ctx context.Context, organizationID, id uint) error

	// CreateMessage stores a message with its attachments. With an idempotency key that the mailbox already used it
	// reports created=false and fills message with the stored row.
	CreateMessage(ctx context.Context, message *MailMessage) (created bool, err error)
	FindMessageByIdempotencyKey(ctx context.Context, mailboxID uint, key string) (*MailMessage, error)
	// FindOutboundMessageForOrganization finds an outbound message in any mailbox of the organization.
	FindOutboundMessageForOrganization(ctx context.Context, organizationID, id uint) (*MailMessage, error)
	// CreateInboundMessage stores an inbound message with its attachments once per mailbox and dedupe key. It
	// reports created=false and fills message with the stored row on a repeated delivery.
	CreateInboundMessage(ctx context.Context, message *MailMessage) (created bool, err error)
	UpdateMessageDelivery(ctx context.Context, id uint, status MailStatus, messageID, failureCode string) error
	ListMessages(ctx context.Context, mailboxID uint, filter MailMessageFilter) ([]*MailMessage, int64, error)
	FindMessage(ctx context.Context, mailboxID, id uint) (*MailMessage, error)
	SetMessageRead(ctx context.Context, mailboxID, id uint, readAt *time.Time) error
	DeleteMessage(ctx context.Context, mailboxID, id uint) error
	CountOutboundSince(ctx context.Context, organizationID uint, since time.Time) (int64, error)
	// InTransaction runs fn in one database transaction. Repository calls made with the context
	// fn receives, and cooperating repositories of other modules, join it.
	InTransaction(ctx context.Context, fn func(ctx context.Context) error) error
	// AddMessageEvents records delivery events and refreshes the message's summary in one
	// transaction. An event whose ProviderEventID is already stored is skipped; the count of
	// newly stored events is returned.
	AddMessageEvents(ctx context.Context, messageID uint, events []MailEvent) (added int, summary MailEventType, err error)
	ListMessageEvents(ctx context.Context, messageID uint) ([]MailEvent, error)
	// ListMessageAttachments lists the attachments of a message without their content.
	ListMessageAttachments(ctx context.Context, messageID uint) ([]MailAttachment, error)
	// FindMessageAttachment loads one attachment of a message with its content.
	FindMessageAttachment(ctx context.Context, messageID, id uint) (*MailAttachment, error)
	// FindOutboundMessageByProviderID finds the outbound message a provider event names.
	FindOutboundMessageByProviderID(ctx context.Context, providerMessageID string) (*MailMessage, error)
	// ListMessagesForOrganization lists messages across every mailbox of the organization, newest first,
	// without bodies.
	ListMessagesForOrganization(ctx context.Context, organizationID uint, filter MailLogFilter) ([]*MailMessage, int64, error)
	// StatsForOrganization counts messages per UTC day from since, for days that have any.
	StatsForOrganization(ctx context.Context, organizationID uint, since time.Time) ([]MailDayStats, error)
}
