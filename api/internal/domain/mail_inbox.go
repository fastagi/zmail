package domain

import (
	"context"
	"time"
)

// InboxView is one view of the unified inbox, which merges every mailbox the viewer reads.
type InboxView string

const (
	// InboxViewAll is inbound mail in default (each mailbox's Inbox).
	InboxViewAll InboxView = "all"
	// InboxViewToday is inbound mail outside the trash that arrived during the viewer's day.
	InboxViewToday InboxView = "today"
	// InboxViewPending is mail of either direction the viewer starred, outside the archive and
	// the trash.
	InboxViewPending InboxView = "pending"
	// InboxViewSent is outbound mail in default (each mailbox's Sent).
	InboxViewSent InboxView = "sent"
)

// Valid reports whether the view is a known value.
func (v InboxView) Valid() bool {
	switch v {
	case InboxViewAll, InboxViewToday, InboxViewPending, InboxViewSent:
		return true
	}
	return false
}

// InboxCursor is the position after the last row of an inbox page, in (created_at DESC, id DESC)
// order: the next page holds the rows strictly older than it.
type InboxCursor struct {
	CreatedAt time.Time
	ID        uint
}

// MailboxAccessRow is one live mailbox of an organization with the viewer's member row, from which
// MailboxAccessFor derives the viewer's access. Member is nil without a member row.
type MailboxAccessRow struct {
	Mailbox *Mailbox
	Member  *MailboxMember
}

// InboxAssignment narrows the unified inbox by the assignment of a shared-mailbox conversation.
type InboxAssignment string

const (
	// InboxAssignmentMine keeps open conversations assigned to the viewer.
	InboxAssignmentMine InboxAssignment = "mine"
	// InboxAssignmentUnassigned keeps open conversations without an assignee.
	InboxAssignmentUnassigned InboxAssignment = "unassigned"
	// InboxAssignmentClosed keeps conversations marked done.
	InboxAssignmentClosed InboxAssignment = "closed"
)

// Valid reports whether the value is a known assignment filter.
func (a InboxAssignment) Valid() bool {
	switch a {
	case InboxAssignmentMine, InboxAssignmentUnassigned, InboxAssignmentClosed:
		return true
	}
	return false
}

// InboxQuery selects one page of the unified inbox.
type InboxQuery struct {
	// MailboxIDs is the set to list, already narrowed to the mailboxes the viewer reads; never empty.
	MailboxIDs []uint
	// MembershipID is the viewer, whose stars and reads the view and filters use.
	MembershipID uint
	View         InboxView
	// Unread keeps only messages the viewer has (not) read; nil keeps both.
	Unread *bool
	// HasAttachments keeps only messages with (or without) files; nil keeps both.
	HasAttachments *bool
	// Query is search text; empty for no search.
	Query string
	// GroupThreads lists the newest message of each thread in the view, per mailbox.
	GroupThreads bool
	// DayStart and DayEnd bound the today view, [DayStart, DayEnd); other views ignore them.
	DayStart, DayEnd time.Time
	// After is the cursor of the previous page; nil for the newest page.
	After *InboxCursor
	// Triage keeps only messages with a visible AI triage result of one of these categories;
	// empty applies no triage filter. Only the all and today views take it.
	Triage []MailTriageCategory
	// Assignment keeps only conversations in one assignment state; empty applies no filter.
	Assignment InboxAssignment
	// ViewerUserID is the viewer, whom InboxAssignmentMine matches.
	ViewerUserID uint
	// Since keeps messages created at or after it; nil applies no bound.
	Since *time.Time
	// Until keeps messages created before it; nil applies no bound.
	Until *time.Time
	// Limit is how many rows to return at most: the page size plus one, to tell whether more follow.
	Limit int
}

// InboxViewCounts are the viewer's counts of the unified inbox views over the readable mailboxes.
type InboxViewCounts struct {
	Today       int
	TodayUnread int
	Pending     int
	SentWaiting int
}

// MailInboxRepository reads the unified inbox across mailboxes.
type MailInboxRepository interface {
	// ListMailboxAccessRows returns the organization's live mailboxes, id ascending, with the
	// membership's member rows and no unread counts.
	ListMailboxAccessRows(ctx context.Context, organizationID, membershipID, viewerUserID uint) ([]MailboxAccessRow, error)
	// CountUnreadByMailboxIDs returns the reader's unread Inbox count of each given mailbox of the
	// organization.
	CountUnreadByMailboxIDs(ctx context.Context, organizationID, readerMembershipID uint, mailboxIDs []uint) (map[uint]int, error)
	// ListInbox returns up to query.Limit unsealed messages without bodies, newest first by
	// (created_at, id), with MailboxAddress, and with Thread when grouped by thread.
	ListInbox(ctx context.Context, query InboxQuery) ([]*MailMessage, error)
	// MessageSnippets returns the first 400 characters of the text body of each message.
	MessageSnippets(ctx context.Context, ids []uint) (map[uint]string, error)
	// CountInboxViews counts the views of the given mailboxes for the membership.
	CountInboxViews(ctx context.Context, membershipID uint, mailboxIDs []uint, dayStart, dayEnd time.Time) (InboxViewCounts, error)
}
