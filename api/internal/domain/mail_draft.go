package domain

import (
	"context"
	"time"
)

// Limits on a saved draft, in characters.
const (
	MailDraftRecipientsMaxLength = 4000
	MailDraftSubjectMaxLength    = 998
	MailDraftBodyMaxLength       = 100000
	MailDraftInReplyToMaxLength  = 998
	// MailDraftsPerMember caps the drafts one member keeps in an organization.
	MailDraftsPerMember = 200
)

// MailDraft is a message a member started and saved without sending. It belongs to its writer
// alone; To and Cc are kept as written, valid or not, and checked only when the message is sent.
type MailDraft struct {
	ID             uint
	OrganizationID uint
	UserID         uint
	MailboxID      uint
	To             string
	Cc             string
	Subject        string
	Body           string
	InReplyTo      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// MailDraftRepository stores members' drafts.
type MailDraftRepository interface {
	// ListMailDrafts lists a member's drafts in an organization, the latest change first.
	ListMailDrafts(ctx context.Context, organizationID, userID uint, limit int) ([]MailDraft, error)
	// FindMailDraft reads one of a member's drafts; another's is not found.
	FindMailDraft(ctx context.Context, organizationID, userID, id uint) (MailDraft, error)
	// CountMailDrafts counts a member's drafts in an organization.
	CountMailDrafts(ctx context.Context, organizationID, userID uint) (int64, error)
	// CreateMailDraft stores a new draft and returns it with its id.
	CreateMailDraft(ctx context.Context, draft MailDraft) (MailDraft, error)
	// UpdateMailDraft replaces what a draft holds; another member's is not found.
	UpdateMailDraft(ctx context.Context, draft MailDraft) (MailDraft, error)
	// DeleteMailDraft removes one of a member's drafts; another's is not found.
	DeleteMailDraft(ctx context.Context, organizationID, userID, id uint) error
}
