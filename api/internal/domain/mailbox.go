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
	// MailboxAddress is the address of the owning mailbox. Only the cross-mailbox log fills it.
	MailboxAddress string
	CreatedAt      time.Time
	UpdatedAt      time.Time
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

	// CreateMessage stores a message. With an idempotency key that the mailbox already used it
	// reports created=false and fills message with the stored row.
	CreateMessage(ctx context.Context, message *MailMessage) (created bool, err error)
	FindMessageByIdempotencyKey(ctx context.Context, mailboxID uint, key string) (*MailMessage, error)
	// FindOutboundMessageForOrganization finds an outbound message in any mailbox of the organization.
	FindOutboundMessageForOrganization(ctx context.Context, organizationID, id uint) (*MailMessage, error)
	// CreateInboundMessage stores an inbound message once per mailbox and dedupe key. It
	// reports created=false and fills message with the stored row on a repeated delivery.
	CreateInboundMessage(ctx context.Context, message *MailMessage) (created bool, err error)
	UpdateMessageDelivery(ctx context.Context, id uint, status MailStatus, messageID, failureCode string) error
	ListMessages(ctx context.Context, mailboxID uint, filter MailMessageFilter) ([]*MailMessage, int64, error)
	FindMessage(ctx context.Context, mailboxID, id uint) (*MailMessage, error)
	SetMessageRead(ctx context.Context, mailboxID, id uint, readAt *time.Time) error
	DeleteMessage(ctx context.Context, mailboxID, id uint) error
	CountOutboundSince(ctx context.Context, organizationID uint, since time.Time) (int64, error)
	// ListMessagesForOrganization lists messages across every mailbox of the organization, newest first,
	// without bodies.
	ListMessagesForOrganization(ctx context.Context, organizationID uint, filter MailLogFilter) ([]*MailMessage, int64, error)
	// StatsForOrganization counts messages per UTC day from since, for days that have any.
	StatsForOrganization(ctx context.Context, organizationID uint, since time.Time) ([]MailDayStats, error)
}
