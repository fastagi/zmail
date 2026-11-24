package domain

import (
	"context"
	"errors"
	"time"
)

// Mail triage sentinel errors shared across the triage and mailbox modules.
var (
	ErrMailTriageUnavailable = errors.New("ai triage is not configured")
	ErrMailTriageModelFailed = errors.New("ai triage model failed")
)

// MailTriageSettingView is an organization's AI triage configuration along with instance limits.
type MailTriageSettingView struct {
	MailTriageSettings
	Available        bool
	InstanceDailyCap int
}

// MailTriageSettingInput replaces an organization's AI triage switches.
type MailTriageSettingInput struct {
	Enabled         bool
	DailyMessageCap int
	DraftEnabled    bool
}

// MailTriageOverrideInput is a reader's manual triage decision on one inbound message.
type MailTriageOverrideInput struct {
	Category MailTriageCategory
	DueAt    *time.Time
}

// MailReplyDraftBasis identifies one message or template cited by a generated reply draft.
type MailReplyDraftBasis struct {
	Kind  string
	ID    uint
	Label string
}

// MailReplyDraft is a one-shot AI-generated reply draft; it is never persisted.
type MailReplyDraft struct {
	Body  string
	Basis []MailReplyDraftBasis
}

// MailTriagePort is the hexagonal port exposed to the top-level triage module.
type MailTriagePort interface {
	GetTriageSettingPort(ctx context.Context, viewer OrganizationContext) (MailTriageSettingView, error)
	SaveTriageSettingPort(ctx context.Context, viewer OrganizationContext, input MailTriageSettingInput) (MailTriageSettingView, error)
	OverrideTriagePort(ctx context.Context, viewer OrganizationContext, mailboxID, messageID uint, input MailTriageOverrideInput) (*MailTriage, error)
	DraftReplyPort(ctx context.Context, viewer OrganizationContext, mailboxID, messageID uint) (MailReplyDraft, error)
}
