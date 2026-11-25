package domain

import (
	"context"
	"errors"
	"time"
)

// Transactional mail sentinel errors shared across the transactional and mailbox modules.
var (
	ErrTransactionalMessageNotFound = errors.New("transactional message not found")
	ErrTransactionalDisabled        = errors.New("transactional mail is disabled")
	ErrMailRecipientRateLimited     = errors.New("recipient rate limit exceeded")
	ErrTransactionalRateLimited     = errors.New("transactional mail rate limit exceeded")
	ErrMailMessageInvalid           = errors.New("mail message is invalid")
)

// MailTransactionalActor identifies who initiated a transactional send.
type MailTransactionalActor struct {
	OrganizationID uint
	UserID         uint
	APIKeyID       *uint
	Mode           APIKeyMode
	Viewer         OrganizationContext
}

// MailTransactionalSendInput holds the parameters of one transactional API send.
type MailTransactionalSendInput struct {
	Template       string
	To             string
	IdempotencyKey string
	Variables      map[string]string
}

// MailDirectSendInput holds the parameters of one direct (template-free) transactional send.
type MailDirectSendInput struct {
	From           string
	To             string
	Subject        string
	HTML           string
	Text           string
	IdempotencyKey string
}

// MailTransactionalTestInput holds the parameters of one console test send.
type MailTransactionalTestInput struct {
	Template  string
	Mode      APIKeyMode
	Variables map[string]string
}

// MailTransactionalLogPage is one cursor page of transactional mail log entries.
type MailTransactionalLogPage struct {
	Items      []*TransactionalMessage
	NextCursor *uint
	Total24h   int64
}

// MailSendLaneUsage reports one lane's rolling usage against its cap.
type MailSendLaneUsage struct {
	Lane    SendLane
	Limit   int
	Used    int64
	Waiting int64
}

// MailTransactionalPort is the hexagonal port exposed to the top-level transactional module.
type MailTransactionalPort interface {
	TransactionalEnabledPort() bool
	SendTransactionalPort(ctx context.Context, actor MailTransactionalActor, input MailTransactionalSendInput) (*TransactionalMessage, bool, error)
	SendDirectTransactionalPort(ctx context.Context, actor MailTransactionalActor, input MailDirectSendInput) (*TransactionalMessage, bool, error)
	GetTransactionalPort(ctx context.Context, organizationID uint, mode APIKeyMode, id uint) (*TransactionalMessage, error)
	ListTransactionalLogPort(ctx context.Context, organizationID uint, filter TransactionalLogFilter) (MailTransactionalLogPage, error)
	GetTransactionalLogEntryPort(ctx context.Context, organizationID, id uint) (*TransactionalMessage, error)
	SendTransactionalTestPort(ctx context.Context, viewer OrganizationContext, input MailTransactionalTestInput) (*TransactionalMessage, error)
	ListSendLanesPort(ctx context.Context, viewer OrganizationContext) ([]MailSendLaneUsage, time.Time, error)
	ListSuppressionsPort(ctx context.Context, viewer OrganizationContext, filter MailSuppressionFilter) ([]MailSuppression, *uint, error)
	RemoveSuppressionPort(ctx context.Context, viewer OrganizationContext, id uint) error
}
