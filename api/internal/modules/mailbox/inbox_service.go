package mailbox

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

// inboxSnippetLength is how many characters of collapsed text a snippet keeps.
const inboxSnippetLength = 140

// InboxRequest is a validated request for one page of the unified inbox.
type InboxRequest struct {
	View domain.InboxView
	// RoleID keeps only the mailboxes of one mail role; 0 applies no role filter.
	RoleID uint
	// Ungrouped keeps only the mailboxes without a role (role_id=none).
	Ungrouped bool
	// MailboxID keeps only one mailbox; 0 applies no mailbox filter.
	MailboxID uint
	Unread    *bool
	// HasAttachments keeps only messages with (or without) files; nil keeps both.
	HasAttachments *bool
	Query          string
	GroupThreads   bool
	// Location defines today for the today view; nil is UTC.
	Location *time.Location
	// After is the cursor of the previous page; nil for the newest page.
	After   *domain.InboxCursor
	PerPage int
	// Triage keeps only messages triaged into one of these categories; empty applies no filter.
	Triage []domain.MailTriageCategory
	// Assignment keeps only conversations in one assignment state; empty applies no filter.
	Assignment domain.InboxAssignment
	// SinceDate and UntilDate are inclusive calendar days in Location, parsed at UTC midnight.
	SinceDate, UntilDate *time.Time
}

// InboxItem is one row of the unified inbox: a message summary without bodies, the mail role of
// its mailbox, and a short text snippet.
type InboxItem struct {
	Message *domain.MailMessage
	// RoleID is the mailbox's mail role now; nil when it has none.
	RoleID  *uint
	Snippet string
	// Triage is the message's AI triage result; nil without one or with the organization's AI
	// triage off.
	Triage *domain.MailTriage
}

// InboxPage is one page of the unified inbox. Next is nil on the last page.
type InboxPage struct {
	Items []InboxItem
	Next  *domain.InboxCursor
	// ReadableMailboxes is how many mailboxes the page was read from, after the filters; it is
	// for logs only.
	ReadableMailboxes int
}

// InboxCounts are the viewer's own live counts over the mailboxes they read.
type InboxCounts struct {
	// AllUnread is Σ Roles + UngroupedUnread, the unread Inbox mail of every readable mailbox.
	AllUnread, Today, TodayUnread, Pending, SentWaiting, Snoozed int
	// Roles has one entry per mail role with a readable mailbox, role id ascending.
	Roles           []InboxRoleCount
	UngroupedUnread int
	// Mailboxes has one entry per readable mailbox, mailbox id ascending.
	Mailboxes []InboxMailboxCount
	// TodayStartsAt and TodayEndsAt bound the viewer's day [start, end), in UTC.
	TodayStartsAt, TodayEndsAt time.Time
	// Triage counts unread Inbox mail per AI triage category; nil with the organization's AI
	// triage off.
	Triage *domain.MailTriageCounts
}

// InboxRoleCount is the unread Inbox mail of a mail role's readable mailboxes.
type InboxRoleCount struct {
	RoleID uint
	Unread int
}

// InboxMailboxCount is the unread Inbox mail of one readable mailbox.
type InboxMailboxCount struct {
	MailboxID uint
	Unread    int
}

// ListInbox lists one page of the mail the viewer reads across mailboxes. Only mailboxes the
// viewer has view on are read; a mailbox or role filter the viewer cannot read, including
// another organization's ids, narrows the set to nothing rather than failing. When reading the
// mail fails, the page still carries ReadableMailboxes for the caller's log.
func (s *service) ListInbox(ctx context.Context, viewer domain.OrganizationContext, request InboxRequest) (InboxPage, error) {
	if !request.View.Valid() || request.PerPage < 1 {
		return InboxPage{}, fmt.Errorf("%w: inbox request", domain.ErrInvalidInput)
	}
	mailboxes, err := s.accessRows(ctx, viewer)
	if err != nil {
		return InboxPage{}, err
	}
	readable := make(map[uint]*domain.Mailbox, len(mailboxes))
	ids := make([]uint, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		switch {
		case !mailbox.Access.View:
		case request.MailboxID != 0 && mailbox.ID != request.MailboxID:
		case request.Ungrouped && mailbox.RoleID != nil:
		case request.RoleID != 0 && (mailbox.RoleID == nil || *mailbox.RoleID != request.RoleID):
		default:
			readable[mailbox.ID] = mailbox
			ids = append(ids, mailbox.ID)
		}
	}
	page := InboxPage{Items: []InboxItem{}, ReadableMailboxes: len(ids)}
	if len(ids) == 0 {
		return page, nil
	}
	triageOn, err := s.triageOn(ctx, viewer.OrganizationID)
	if err != nil {
		return page, err
	}
	if len(request.Triage) > 0 && !triageOn {
		// Without AI triage nothing has a visible result, so the filter keeps nothing.
		return page, nil
	}

	s.wakeSnoozedOpportunistically(ctx)
	now := s.now().UTC()
	query := domain.InboxQuery{
		MailboxIDs: ids, MembershipID: viewer.MembershipID, View: request.View,
		Unread: request.Unread, HasAttachments: request.HasAttachments, Query: request.Query, GroupThreads: request.GroupThreads,
		After: request.After, Limit: request.PerPage + 1, Triage: request.Triage,
		Assignment: request.Assignment, ViewerUserID: viewer.UserID, Now: now,
	}
	if request.View == domain.InboxViewToday {
		query.DayStart, query.DayEnd = inboxDay(now, request.Location)
	}
	query.Since = inboxDateBound(request.SinceDate, request.Location, 0)
	query.Until = inboxDateBound(request.UntilDate, request.Location, 1)
	messages, err := s.inbox.ListInbox(ctx, query)
	if err != nil {
		return InboxPage{ReadableMailboxes: page.ReadableMailboxes}, err
	}
	if len(messages) > request.PerPage {
		messages = messages[:request.PerPage]
		last := messages[len(messages)-1]
		page.Next = &domain.InboxCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if len(messages) == 0 {
		return page, nil
	}
	if err = s.fillViewerState(ctx, viewer, messages...); err != nil {
		return InboxPage{}, err
	}
	messageIDs := make([]uint, 0, len(messages))
	for _, message := range messages {
		messageIDs = append(messageIDs, message.ID)
	}
	snippets, err := s.inbox.MessageSnippets(ctx, messageIDs)
	if err != nil {
		return InboxPage{}, err
	}
	triage := map[uint]*domain.MailTriage{}
	if triageOn {
		if triage, err = s.triage.ListTriage(ctx, messageIDs); err != nil {
			return InboxPage{}, err
		}
	}
	for _, message := range messages {
		mailbox := readable[message.MailboxID]
		if mailbox == nil {
			// The repository only reads the given mailboxes; a stray row is never shown.
			continue
		}
		if message.Direction == domain.MailDirectionOutbound {
			message.Read = true
		}
		message.MailboxAddress = mailbox.Address
		var roleID *uint
		if mailbox.RoleID != nil {
			roleID = new(*mailbox.RoleID)
		}
		page.Items = append(page.Items, InboxItem{
			Message: message, RoleID: roleID, Snippet: inboxItemSnippet(message, snippets[message.ID]), Triage: triage[message.ID],
		})
	}
	return page, nil
}

// InboxCounts counts the viewer's unified inbox over the mailboxes they have view on, with today
// as the viewer's day in location (nil is UTC). Only readable mailbox ids reach the count
// queries; with none, the counts are zero without reading mail.
func (s *service) InboxCounts(ctx context.Context, viewer domain.OrganizationContext, location *time.Location) (InboxCounts, error) {
	mailboxes, err := s.accessRows(ctx, viewer)
	if err != nil {
		return InboxCounts{}, err
	}
	counts := InboxCounts{Roles: []InboxRoleCount{}, Mailboxes: []InboxMailboxCount{}}
	counts.TodayStartsAt, counts.TodayEndsAt = inboxDay(s.now(), location)
	readable := make([]*domain.Mailbox, 0, len(mailboxes))
	ids := make([]uint, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		if mailbox.Access.View {
			readable = append(readable, mailbox)
			ids = append(ids, mailbox.ID)
		}
	}
	if len(ids) == 0 {
		return counts, nil
	}
	slices.Sort(ids)
	slices.SortFunc(readable, func(a, b *domain.Mailbox) int { return cmp.Compare(a.ID, b.ID) })

	s.wakeSnoozedOpportunistically(ctx)
	unread, err := s.inbox.CountUnreadByMailboxIDs(ctx, viewer.OrganizationID, viewer.MembershipID, ids)
	if err != nil {
		return InboxCounts{}, err
	}
	views, err := s.inbox.CountInboxViews(ctx, viewer.MembershipID, ids, counts.TodayStartsAt, counts.TodayEndsAt)
	if err != nil {
		return InboxCounts{}, err
	}
	counts.Today, counts.TodayUnread, counts.Pending, counts.SentWaiting, counts.Snoozed =
		views.Today, views.TodayUnread, views.Pending, views.SentWaiting, views.Snoozed
	if counts.Triage, err = s.triageCounts(ctx, viewer, ids); err != nil {
		return InboxCounts{}, err
	}

	roles := map[uint]int{}
	for _, mailbox := range readable {
		count := unread[mailbox.ID]
		counts.Mailboxes = append(counts.Mailboxes, InboxMailboxCount{MailboxID: mailbox.ID, Unread: count})
		if mailbox.RoleID == nil {
			counts.UngroupedUnread += count
		} else {
			roles[*mailbox.RoleID] += count
		}
	}
	counts.AllUnread = counts.UngroupedUnread
	for _, roleID := range slices.Sorted(maps.Keys(roles)) {
		counts.Roles = append(counts.Roles, InboxRoleCount{RoleID: roleID, Unread: roles[roleID]})
		counts.AllUnread += roles[roleID]
	}
	return counts, nil
}

// accessRows lists the organization's live mailboxes with the viewer's access to each, without
// counting unread mail.
func (s *service) accessRows(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.Mailbox, error) {
	if s.inbox == nil {
		return nil, domain.ErrServiceUnavailable
	}
	rows, err := s.inbox.ListMailboxAccessRows(ctx, viewer.OrganizationID, viewer.MembershipID, viewer.UserID)
	if err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	for _, row := range rows {
		if row.Mailbox == nil {
			continue
		}
		row.Mailbox.Access = domain.MailboxAccessFor(row.Mailbox, viewer, row.Member)
		mailboxes = append(mailboxes, row.Mailbox)
	}
	return mailboxes, nil
}

// inboxDay is the viewer's day containing now in location (nil is UTC) as [start, end) in UTC.
// The end is the next local midnight, so a day around a daylight saving change is 23 or 25
// hours long.
func inboxDay(now time.Time, location *time.Location) (start, end time.Time) {
	if location == nil {
		location = time.UTC
	}
	local := now.In(location)
	start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	end = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location)
	return start.UTC(), end.UTC()
}

// inboxSnippet collapses each run of whitespace to one space, trims the ends, and keeps the first
// inboxSnippetLength characters.
func inboxSnippet(raw string) string {
	collapsed := strings.Join(strings.Fields(raw), " ")
	if utf8.RuneCountInString(collapsed) <= inboxSnippetLength {
		return collapsed
	}
	runes := []rune(collapsed)
	return strings.TrimRightFunc(string(runes[:inboxSnippetLength]), unicode.IsSpace)
}

// inboxDateBound is the start of the calendar day of date, plus days, in location; nil stays nil.
func inboxDateBound(date *time.Time, location *time.Location, days int) *time.Time {
	if date == nil {
		return nil
	}
	if location == nil {
		location = time.UTC
	}
	bound := time.Date(date.Year(), date.Month(), date.Day()+days, 0, 0, 0, 0, location)
	if bound.Hour() != 0 {
		// A clock change skipped midnight: the day starts when the clock jumps forward.
		_, before := bound.Zone()
		_, after := bound.Add(24 * time.Hour).Zone()
		bound = bound.Add(time.Duration(after-before) * time.Second)
	}
	return &bound
}
