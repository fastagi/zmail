package domain

import (
	"context"
	"errors"
	"time"
)

// Mail broadcast sentinel errors shared across the broadcast and mailbox modules.
var (
	ErrMailBroadcastNotFound     = errors.New("mail broadcast not found")
	ErrMailBroadcastInvalid      = errors.New("mail broadcast is invalid")
	ErrMailBroadcastNotEditable  = errors.New("mail broadcast cannot be changed in its current state")
	ErrMailBroadcastNoRecipients = errors.New("mail broadcast has no subscribed recipients")
	ErrMailBroadcastDisabled     = errors.New("mail broadcasts are not configured")
	ErrMailUnsubscribeInvalid    = errors.New("unsubscribe link is invalid")
)

// MailBroadcastInput is the editable content of a draft broadcast.
type MailBroadcastInput struct {
	Name             string
	MailboxID        uint
	TemplateID       uint
	ListID           uint
	UnsubscribeLabel string
	TestPercent      int
	TestWaitMinutes  int
	Variables        map[string]string
}

// MailBroadcastPort is the hexagonal port exposed to the top-level broadcast module.
type MailBroadcastPort interface {
	ListBroadcastsPort(ctx context.Context, viewer OrganizationContext, page, perPage int) ([]*MailBroadcast, int64, error)
	GetBroadcastPort(ctx context.Context, viewer OrganizationContext, id uint) (*MailBroadcast, error)
	BroadcastProgressPort(ctx context.Context, broadcast *MailBroadcast) (*MailBroadcastProgress, error)
	CreateBroadcastPort(ctx context.Context, viewer OrganizationContext, input MailBroadcastInput) (*MailBroadcast, error)
	UpdateBroadcastPort(ctx context.Context, viewer OrganizationContext, id uint, input MailBroadcastInput) (*MailBroadcast, error)
	DeleteBroadcastPort(ctx context.Context, viewer OrganizationContext, id uint) error
	SendBroadcastPort(ctx context.Context, viewer OrganizationContext, id uint, scheduledAt *time.Time) (*MailBroadcast, error)
	CancelBroadcastPort(ctx context.Context, viewer OrganizationContext, id uint) (*MailBroadcast, bool, error)
	PauseBroadcastPort(ctx context.Context, viewer OrganizationContext, id uint) (*MailBroadcast, bool, error)
	ResumeBroadcastPort(ctx context.Context, viewer OrganizationContext, id uint) (*MailBroadcast, error)
	ListBroadcastRecipientsPort(ctx context.Context, viewer OrganizationContext, id uint, status MailRecipientStatus, page, perPage int) ([]*MailBroadcastRecipient, int64, error)
	UnsubscribePort(ctx context.Context, token string) error
}
