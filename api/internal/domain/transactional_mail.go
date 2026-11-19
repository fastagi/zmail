package domain

import (
	"context"
	"time"
)

// TransactionalStatus is where a transactional message is in its send. queued is the only
// non-terminal state.
type TransactionalStatus string

const (
	// TransactionalStatusQueued: stored and waiting for (or in) its provider call.
	TransactionalStatusQueued TransactionalStatus = "queued"
	// TransactionalStatusSent: the provider accepted the message, or the simulation said so.
	TransactionalStatusSent TransactionalStatus = "sent"
	// TransactionalStatusFailed: the message will not be sent; FailureCode says why.
	TransactionalStatusFailed TransactionalStatus = "failed"
	// TransactionalStatusSuppressed: the recipient is suppressed, so nothing was sent.
	TransactionalStatusSuppressed TransactionalStatus = "suppressed"
)

// Valid reports whether the status is a known value.
func (s TransactionalStatus) Valid() bool {
	switch s {
	case TransactionalStatusQueued, TransactionalStatusSent, TransactionalStatusFailed, TransactionalStatusSuppressed:
		return true
	default:
		return false
	}
}

// Terminal reports whether the status is final.
func (s TransactionalStatus) Terminal() bool {
	return s.Valid() && s != TransactionalStatusQueued
}

// TransactionalQueuedReason says why a queued message is waiting.
type TransactionalQueuedReason string

const (
	// TransactionalQueuedSendLimit: the transactional lane is full; it waits for room.
	TransactionalQueuedSendLimit TransactionalQueuedReason = "send_limit"
	// TransactionalQueuedRetry: the provider answered with a temporary failure; it waits for its retry.
	TransactionalQueuedRetry TransactionalQueuedReason = "retry"
)

// Failure codes the transactional storage sets by itself.
const (
	// TransactionalFailureExpired: the message stayed queued longer than TransactionalQueueTimeout.
	TransactionalFailureExpired = "send.expired"
	// TransactionalFailureUnknownOutcome: a provider call started but its result was never
	// recorded; the provider may have accepted it, so it is never resent.
	TransactionalFailureUnknownOutcome = "transport.unknown_outcome"
)

const (
	// TransactionalQueueTimeout is how long a message may stay queued before it fails as expired.
	TransactionalQueueTimeout = time.Hour
	// TransactionalLease is how long one dispatcher holds a message.
	TransactionalLease = 2 * time.Minute
	// TransactionalRetention is how long transactional messages and their events are kept.
	TransactionalRetention = 30 * 24 * time.Hour
)

// TransactionalMessage is one message sent from a transactional template to one recipient.
// The rendered payload is kept, encrypted, only while it is queued.
type TransactionalMessage struct {
	ID             uint
	OrganizationID uint
	Mode           APIKeyMode
	// Simulated messages go to a simulator address; they never reach the provider.
	Simulated bool
	// APIKeyID is the key that sent it; nil for a console test send.
	APIKeyID  *uint
	CreatedBy uint
	// TemplateID is nil once the template is deleted; TemplateKey keeps the snapshot.
	TemplateID  *uint
	TemplateKey string
	FromAddress string
	FromName    string
	// ToAddress is the lowercased full recipient address.
	ToAddress       string
	SubjectMasked   string
	VariablesMasked map[string]string
	// PayloadCiphertext is the encrypted rendered message; nil in every terminal state.
	PayloadCiphertext []byte
	// IdempotencyKey is released (nil) 24 hours after the message was created.
	IdempotencyKey         *string
	IdempotencyFingerprint []byte
	Status                 TransactionalStatus
	QueuedReason           *TransactionalQueuedReason
	Attempts               int
	NextAttemptAt          *time.Time
	LeaseUntil             *time.Time
	// SendAttemptedAt is when the latest provider call started; the lane counts by it.
	SendAttemptedAt   *time.Time
	SentAt            *time.Time
	ProviderMessageID *string
	// RFCMessageID is the Message-ID header Zmail generated, without angle brackets.
	RFCMessageID string
	FailureCode  string
	// LastEvent summarizes the delivery events; empty before any.
	LastEvent MailEventType
	CreatedAt time.Time
	UpdatedAt time.Time
	// Events is filled only by reads that ask for the delivery history.
	Events []MailEvent
}

// CountsInLane reports whether the message takes room in the transactional lane: only live,
// real messages reach the provider.
func (m *TransactionalMessage) CountsInLane() bool {
	return m.Mode == APIKeyModeLive && !m.Simulated
}

// TransactionalOutcome is the terminal state a dispatcher records for a message.
type TransactionalOutcome struct {
	Status            TransactionalStatus
	FailureCode       string
	ProviderMessageID string
	SentAt            *time.Time
}

// Outcome filter values of the transactional log.
const (
	// TransactionalOutcomeDelivered selects messages whose last event is delivered.
	TransactionalOutcomeDelivered = "delivered"
	// TransactionalOutcomeProblem selects messages that bounced, complained, failed, were
	// rejected, or ended failed or suppressed.
	TransactionalOutcomeProblem = "problem"
)

// TransactionalLogFilter narrows the transactional log. Empty fields do not filter.
type TransactionalLogFilter struct {
	Mode        APIKeyMode
	Status      string
	LastEvent   string
	Outcome     string
	TemplateKey string
	// To matches the full recipient address exactly, case-insensitively.
	To    string
	Since time.Time
	Until time.Time
	// Cursor lists messages with an id below it; 0 starts from the newest.
	Cursor  uint
	PerPage int
}

// Suppression reasons and sources.
const (
	MailSuppressionReasonBounced    = "bounced"
	MailSuppressionReasonComplained = "complained"

	MailSuppressionSourceBroadcast     = "broadcast"
	MailSuppressionSourceTransactional = "transactional"
	MailSuppressionSourceBackfill      = "backfill"
)

// MailSuppression is an address of an organization that bounced for good or complained; neither
// transactional sends nor broadcasts reach it until an administrator removes it.
type MailSuppression struct {
	ID             uint
	OrganizationID uint
	// Email is lowercased.
	Email     string
	Reason    string
	Source    string
	CreatedAt time.Time
}

// MailSuppressionFilter narrows the suppression list. Empty fields do not filter.
type MailSuppressionFilter struct {
	// Email matches the full address exactly, case-insensitively.
	Email  string
	Reason string
	// Cursor lists the entries after the one with this id, newest first; 0 starts from the newest.
	Cursor  uint
	PerPage int
}

// TransactionalMailRepository stores transactional messages, their events, and the organization
// suppression list.
type TransactionalMailRepository interface {
	// CreateTransactional stores a message. It reports false, storing nothing, when another
	// message of the organization and mode already holds the idempotency key.
	CreateTransactional(ctx context.Context, message *TransactionalMessage) (bool, error)
	// FindTransactionalByIdempotencyKey finds the message holding the key in an organization and mode.
	FindTransactionalByIdempotencyKey(ctx context.Context, organizationID uint, mode APIKeyMode, key string) (*TransactionalMessage, error)
	// ReleaseIdempotencyKey frees a message's idempotency key for reuse.
	ReleaseIdempotencyKey(ctx context.Context, id uint) error
	// FindTransactional finds a message of an organization.
	FindTransactional(ctx context.Context, organizationID, id uint) (*TransactionalMessage, error)
	// FindTransactionalByID finds a message of any organization, for the dispatcher.
	FindTransactionalByID(ctx context.Context, id uint) (*TransactionalMessage, error)
	// OldestQueuedTransactional reports when the oldest live, real message still queued and not
	// waiting for lane room was created; nil when there is none.
	OldestQueuedTransactional(ctx context.Context) (*time.Time, error)
	// FindTransactionalByProviderID finds the message the provider knows by this id.
	FindTransactionalByProviderID(ctx context.Context, providerMessageID string) (*TransactionalMessage, error)
	// CountTransactionalAttemptsSince counts the organization's provider calls since a time.
	CountTransactionalAttemptsSince(ctx context.Context, organizationID uint, since time.Time) (int64, error)
	// CountTransactionalWaiting counts the organization's messages waiting for lane room.
	CountTransactionalWaiting(ctx context.Context, organizationID uint) (int64, error)
	// LeaseTransactional leases due, free messages of an organization to one dispatcher until
	// leaseUntil: the given ids, or the oldest due ones when ids is empty, at most limit. Live, real
	// messages are leased only while the lane has room (laneLimit minus the attempts since
	// windowStart and the leases not yet attempted); the rest are marked as waiting for room.
	// Test and simulated messages always lease.
	LeaseTransactional(ctx context.Context, organizationID uint, ids []uint, now, leaseUntil, windowStart time.Time, laneLimit, limit int) ([]*TransactionalMessage, error)
	// TransactionalDueOrganizations lists organizations with due, free queued messages.
	TransactionalDueOrganizations(ctx context.Context, now time.Time, limit int) ([]uint, error)
	// StartTransactionalAttempt records the start of a provider call by the lease holder.
	StartTransactionalAttempt(ctx context.Context, id uint, lease, at time.Time) (bool, error)
	// FinishTransactional records the terminal outcome by the lease holder and wipes the payload.
	FinishTransactional(ctx context.Context, id uint, lease time.Time, outcome TransactionalOutcome) (bool, error)
	// ScheduleTransactionalRetry queues the message again, by the lease holder, for a retry at next.
	ScheduleTransactionalRetry(ctx context.Context, id uint, lease, next time.Time, failureCode string) (bool, error)
	// ExpireTransactional fails free messages queued longer than TransactionalQueueTimeout.
	ExpireTransactional(ctx context.Context, now time.Time, limit int) ([]*TransactionalMessage, error)
	// FailUnknownTransactional fails messages whose provider call started inside a lease that
	// then ran out without a recorded result. They are never resent.
	FailUnknownTransactional(ctx context.Context, now time.Time, limit int) ([]*TransactionalMessage, error)
	// PurgeTransactional deletes at most limit messages created before a time, with their events.
	PurgeTransactional(ctx context.Context, before time.Time, limit int) (int, error)
	// AddTransactionalEvents stores new delivery events, skipping known provider events, and
	// returns how many were added and the recomputed summary.
	AddTransactionalEvents(ctx context.Context, id uint, events []MailEvent) (int, MailEventType, error)
	// ListTransactionalEvents lists a message's events, oldest first.
	ListTransactionalEvents(ctx context.Context, id uint, limit int) ([]MailEvent, error)
	// ListTransactionalLog lists an organization's messages, newest first, PerPage+1 at most so
	// the caller can tell whether another page exists.
	ListTransactionalLog(ctx context.Context, organizationID uint, filter TransactionalLogFilter) ([]*TransactionalMessage, error)
	// CountTransactionalSince counts, up to limit, an organization's messages of one mode (and
	// template key, when given) created since a time.
	CountTransactionalSince(ctx context.Context, organizationID uint, mode APIKeyMode, templateKey string, since time.Time, limit int) (int64, error)

	// IsSuppressed reports whether an address is on the organization's suppression list.
	IsSuppressed(ctx context.Context, organizationID uint, email string) (bool, error)
	// AddSuppression puts an address on the list; an address already there is left as it is.
	AddSuppression(ctx context.Context, suppression MailSuppression) error
	// ListSuppressions lists the organization's suppressions, newest first, PerPage+1 at most.
	ListSuppressions(ctx context.Context, organizationID uint, filter MailSuppressionFilter) ([]MailSuppression, error)
	// DeleteSuppression removes one entry and returns it; nil when there was none.
	DeleteSuppression(ctx context.Context, organizationID, id uint) (*MailSuppression, error)
}
