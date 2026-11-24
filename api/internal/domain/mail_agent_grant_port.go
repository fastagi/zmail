package domain

import (
	"context"
	"errors"
	"time"
)

// Mail agent grant sentinel errors shared across the agentgrant and mailbox modules.
var (
	ErrMailAgentGrantNotFound = errors.New("agent grant not found")
	ErrMailAgentGrantInvalid  = errors.New("agent grant is invalid")
	ErrMailAgentGrantLimit    = errors.New("mailbox agent grant limit reached")
	ErrMailAgentGrantExists   = errors.New("agent grant already exists for this api key")
	ErrMailAgentRateLimited   = errors.New("agent mail rate limit exceeded")
)

// IssueMailAgentGrantInput carries the parameters to issue a scoped agent grant on a mailbox.
type IssueMailAgentGrantInput struct {
	APIKeyID      uint
	Capabilities  []string
	RevealSecrets bool
	Senders       []string
	LabelIDs      []uint
	ExpiresAt     time.Time
}

// MailAgentDraftInput is an agent's reply draft awaiting human review.
type MailAgentDraftInput struct {
	MessageID uint
	Text      string
	HTML      string
}

// MailAgentFeedPage is one cursor page of agent mail events.
type MailAgentFeedPage struct {
	Events     []*MailAgentEvent
	NextCursor uint
	HasMore    bool
}

// MailAgentGrantPort is the hexagonal port exposed to the top-level agentgrant module.
type MailAgentGrantPort interface {
	IssueAgentGrantPort(ctx context.Context, viewer OrganizationContext, mailboxID uint, input IssueMailAgentGrantInput) (*MailAgentGrant, error)
	ListAgentGrantsPort(ctx context.Context, viewer OrganizationContext, mailboxID uint, page, perPage int) ([]*MailAgentGrant, int64, error)
	RevokeAgentGrantPort(ctx context.Context, viewer OrganizationContext, mailboxID, grantID uint) error
	AgentGrantActivityPort(ctx context.Context, viewer OrganizationContext, mailboxID, grantID uint) ([]*MailAgentAuditEntry, error)
	ListAgentDraftsPort(ctx context.Context, viewer OrganizationContext, mailboxID uint, page, perPage int) ([]*MailAgentDraft, int64, error)
	AgentMailEventsPort(ctx context.Context, viewer OrganizationContext, apiKeyID, after uint, limit int) (MailAgentFeedPage, error)
	SaveAgentDraftPort(ctx context.Context, viewer OrganizationContext, apiKeyID uint, input MailAgentDraftInput) (*MailAgentDraft, bool, error)
}
