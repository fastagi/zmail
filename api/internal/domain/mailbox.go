package domain

import (
	"context"
	"time"
)

// Mailbox limits shared by the service, persistence, and the HTTP contract.
const (
	// MaxMailboxesPerUser bounds how many addresses one account may own.
	MaxMailboxesPerUser = 10
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

// Mailbox is one address a user sends from and receives at.
type Mailbox struct {
	ID          uint
	UserID      uint
	Address     string
	DisplayName string
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
	ReadAt      *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MailMessageFilter narrows a mailbox message listing.
type MailMessageFilter struct {
	Direction MailDirection
	Page      int
	PerPage   int
}

// MailboxRepository defines persistence for mailboxes and their messages.
type MailboxRepository interface {
	CreateMailbox(ctx context.Context, mailbox *Mailbox, limit int) error
	ListMailboxes(ctx context.Context, userID uint) ([]*Mailbox, error)
	FindMailbox(ctx context.Context, userID, id uint) (*Mailbox, error)
	FindMailboxByAddress(ctx context.Context, address string) (*Mailbox, error)
	DeleteMailbox(ctx context.Context, userID, id uint) error
	// DeleteMailboxesForUser removes every mailbox and message of one user. It joins the
	// transaction bound to ctx, so account deletion and this cleanup commit together.
	DeleteMailboxesForUser(ctx context.Context, userID uint) error

	CreateMessage(ctx context.Context, message *MailMessage) error
	// CreateInboundMessage stores an inbound message once per mailbox and dedupe key. It
	// reports created=false and fills message with the stored row on a repeated delivery.
	CreateInboundMessage(ctx context.Context, message *MailMessage) (created bool, err error)
	UpdateMessageDelivery(ctx context.Context, id uint, status MailStatus, messageID, failureCode string) error
	ListMessages(ctx context.Context, mailboxID uint, filter MailMessageFilter) ([]*MailMessage, int64, error)
	FindMessage(ctx context.Context, mailboxID, id uint) (*MailMessage, error)
	SetMessageRead(ctx context.Context, mailboxID, id uint, readAt *time.Time) error
	DeleteMessage(ctx context.Context, mailboxID, id uint) error
	CountOutboundSince(ctx context.Context, userID uint, since time.Time) (int64, error)
}
