package domain

import (
	"context"
	"time"
)

// MailTriageCategory is what AI triage decided an inbound message asks of its readers.
type MailTriageCategory string

const (
	// MailTriageReply needs a reply.
	MailTriageReply MailTriageCategory = "reply"
	// MailTriageDeadline has a deadline; the result carries it when known.
	MailTriageDeadline MailTriageCategory = "deadline"
	// MailTriageFYI only needs to be known.
	MailTriageFYI MailTriageCategory = "fyi"
	// MailTriageReview needs a person to decide: the model was unsure, its answer was invalid, or
	// it failed.
	MailTriageReview MailTriageCategory = "review"
)

// MailTriageCategories lists every category in display order.
var MailTriageCategories = []MailTriageCategory{MailTriageReply, MailTriageDeadline, MailTriageFYI, MailTriageReview}

// Valid reports whether the category is a known value.
func (c MailTriageCategory) Valid() bool {
	switch c {
	case MailTriageReply, MailTriageDeadline, MailTriageFYI, MailTriageReview:
		return true
	}
	return false
}

// MailTriageStatus is where a message is in triage.
type MailTriageStatus string

const (
	// MailTriagePending waits for the worker.
	MailTriagePending MailTriageStatus = "pending"
	// MailTriageDone has a result.
	MailTriageDone MailTriageStatus = "done"
	// MailTriageFailed used its attempts; its category is review.
	MailTriageFailed MailTriageStatus = "failed"
	// MailTriageSkipped was dropped without a model call because a switch was turned off.
	MailTriageSkipped MailTriageStatus = "skipped"
)

// MailTriageSource says who decided a result.
type MailTriageSource string

const (
	MailTriageSourceAI     MailTriageSource = "ai"
	MailTriageSourceManual MailTriageSource = "manual"
)

// MaxMailTriageReasonLength bounds the reason line, in characters.
const MaxMailTriageReasonLength = 120

// MailTriage is the triage state of one inbound message. Results are shared by every reader of
// the message's mailbox.
type MailTriage struct {
	MessageID     uint
	MailboxID     uint
	Category      MailTriageCategory
	Reason        string
	DueAt         *time.Time
	Source        MailTriageSource
	Status        MailTriageStatus
	Attempts      int
	LastErrorCode string
	Model         string
	// DecidedBy is who overrode the result; nil for a model result.
	DecidedBy     *uint
	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Visible reports whether the triage has a result readers see.
func (t *MailTriage) Visible() bool {
	return t != nil && (t.Status == MailTriageDone || t.Status == MailTriageFailed) && t.Category.Valid()
}

// MailTriageSettings are an organization's AI triage switches. A missing row reads as the
// defaults with Saved false; the model is never called for an organization without a saved row.
type MailTriageSettings struct {
	OrganizationID  uint
	Enabled         bool
	DailyMessageCap int
	DraftEnabled    bool
	UpdatedBy       *uint
	Saved           bool
	UpdatedAt       time.Time
}

// DefaultMailTriageDailyMessageCap is the cap an organization's settings start with.
const DefaultMailTriageDailyMessageCap = 500

// MailTriageCounts are unread Inbox messages per triage category.
type MailTriageCounts struct {
	Reply, Deadline, FYI, Review int
}

// MailTriageOverride is a person's decision on one message.
type MailTriageOverride struct {
	MessageID uint
	MailboxID uint
	Category  MailTriageCategory
	DueAt     *time.Time
	DecidedBy uint
	At        time.Time
}

// MailTriageExample is one recent override used as a few-shot example.
type MailTriageExample struct {
	Subject  string
	Category MailTriageCategory
}

// MailTriageClaim is a pending row the worker holds, with what it needs to decide whether to call
// the model.
type MailTriageClaim struct {
	Triage           MailTriage
	OrganizationID   uint
	OrganizationOn   bool
	MailboxOn        bool
	MailboxPersonal  bool
	MailboxOwnerLeft bool
	MailboxRoleName  string
	// OrganizationCap is the organization's daily cap; 0 without saved settings.
	OrganizationCap int
}

// MailTriageRepository stores triage rows and settings.
type MailTriageRepository interface {
	FindTriageSettings(ctx context.Context, organizationID uint) (MailTriageSettings, error)
	SaveTriageSettings(ctx context.Context, settings MailTriageSettings, now time.Time) (MailTriageSettings, error)
	// SetMailboxTriage changes a mailbox's switch.
	SetMailboxTriage(ctx context.Context, mailboxID uint, enabled bool) error
	// StopMailboxTriage turns a mailbox's switch off and deletes its pending rows, so nothing of it
	// reaches the model any more; decided rows stay.
	StopMailboxTriage(ctx context.Context, mailboxID uint) error
	// EnqueueTriage adds a pending row for an inbound message when its organization's and its
	// mailbox's switches are on, in the caller's transaction. It reports whether it added one.
	EnqueueTriage(ctx context.Context, messageID, mailboxID uint, now time.Time) (bool, error)
	// ClaimTriage locks up to limit due pending rows with FOR UPDATE SKIP LOCKED, oldest due
	// first, and leases them to the caller.
	ClaimTriage(ctx context.Context, now time.Time, limit int) ([]MailTriageClaim, error)
	// ReserveTriageCall takes one model call of the organization's day; false when the day's cap
	// is reached or the organization switch is off.
	ReserveTriageCall(ctx context.Context, organizationID uint, limit int, now time.Time) (bool, error)
	// SaveTriage writes the worker's outcome for a claimed row.
	SaveTriage(ctx context.Context, triage MailTriage) error
	// OverrideTriage upserts a person's decision.
	OverrideTriage(ctx context.Context, override MailTriageOverride) (*MailTriage, error)
	FindTriage(ctx context.Context, messageID uint) (*MailTriage, error)
	// ListTriage returns the visible triage of each given message; others are absent.
	ListTriage(ctx context.Context, messageIDs []uint) (map[uint]*MailTriage, error)
	// RecentTriageExamples returns the mailbox's newest manual overrides, newest first.
	RecentTriageExamples(ctx context.Context, mailboxID uint, limit int) ([]MailTriageExample, error)
	// CountTriage counts the membership's unread Inbox messages of the given mailboxes per
	// category.
	CountTriage(ctx context.Context, membershipID uint, mailboxIDs []uint) (MailTriageCounts, error)
}

// MailTriageWorker runs AI triage outside request paths.
type MailTriageWorker interface {
	// DispatchTriage classifies up to limit due pending messages and returns how many it handled.
	DispatchTriage(ctx context.Context, limit int) (int, error)
}
