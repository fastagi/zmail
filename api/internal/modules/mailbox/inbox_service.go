package mailbox

import (
	"context"
	"fmt"
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
	MailboxID    uint
	Unread       *bool
	Query        string
	GroupThreads bool
	// Location defines today for the today view; nil is UTC.
	Location *time.Location
	// After is the cursor of the previous page; nil for the newest page.
	After   *domain.InboxCursor
	PerPage int
}

// InboxItem is one row of the unified inbox: a message summary without bodies, the mail role of
// its mailbox, and a short text snippet.
type InboxItem struct {
	Message *domain.MailMessage
	// RoleID is the mailbox's mail role now; nil when it has none.
	RoleID  *uint
	Snippet string
}

// InboxPage is one page of the unified inbox. Next is nil on the last page.
type InboxPage struct {
	Items []InboxItem
	Next  *domain.InboxCursor
	// ReadableMailboxes is how many mailboxes the page was read from, after the filters; it is
	// for logs only.
	ReadableMailboxes int
}

// ListInbox lists one page of the mail the viewer reads across mailboxes. Only mailboxes the
// viewer has view on are read; a mailbox or role filter the viewer cannot read, including
// another organization's ids, narrows the set to nothing rather than failing.
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

	query := domain.InboxQuery{
		MailboxIDs: ids, MembershipID: viewer.MembershipID, View: request.View,
		Unread: request.Unread, Query: request.Query, GroupThreads: request.GroupThreads,
		After: request.After, Limit: request.PerPage + 1,
	}
	if request.View == domain.InboxViewToday {
		query.DayStart, query.DayEnd = dayBounds(s.now(), request.Location)
	}
	messages, err := s.inbox.ListInbox(ctx, query)
	if err != nil {
		return InboxPage{}, err
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
		page.Items = append(page.Items, InboxItem{Message: message, RoleID: roleID, Snippet: inboxSnippet(snippets[message.ID])})
	}
	return page, nil
}

// accessRows lists the organization's live mailboxes with the viewer's access to each, without
// counting unread mail.
func (s *service) accessRows(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.Mailbox, error) {
	if s.inbox == nil {
		return nil, domain.ErrServiceUnavailable
	}
	rows, err := s.inbox.ListMailboxAccessRows(ctx, viewer.OrganizationID, viewer.MembershipID)
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

// dayBounds is the viewer's day containing now in location: [start, end). The end is the next
// local midnight, so a day around a daylight saving change is 23 or 25 hours long.
func dayBounds(now time.Time, location *time.Location) (start, end time.Time) {
	if location == nil {
		location = time.UTC
	}
	local := now.In(location)
	start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	end = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location)
	return start, end
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
