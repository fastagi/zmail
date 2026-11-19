package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

// inboxBase is a fixed past instant the inbox tests date their messages from, so a message stored
// during a test (created now) is always newer than all of them.
var inboxBase = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

// newInboxFixture is the organize scenario with the two seeded messages dated well before
// inboxBase: b@ (B's personal mailbox) and team@ (B send, C view, E manage).
func newInboxFixture(t *testing.T) *organizeFixture {
	t.Helper()
	f := newOrganizeFixture(t)
	f.dateMessage(t, f.personalMessage, inboxBase.Add(-2*time.Hour))
	f.dateMessage(t, f.teamMessage, inboxBase.Add(-time.Hour))
	return f
}

func (f *accessFixture) inboxRepo() *repository {
	return f.repo.(*repository)
}

// dateMessage sets the stored created_at of a message and the copy the test holds.
func (f *accessFixture) dateMessage(t *testing.T, message *domain.MailMessage, at time.Time) {
	t.Helper()
	if err := f.inboxRepo().db.Exec(`UPDATE mail_messages SET created_at = ? WHERE id = ?`, at, message.ID).Error; err != nil {
		t.Fatalf("date message %d: %v", message.ID, err)
	}
	message.CreatedAt = at
}

// deliverAt stores an inbound message created at the given time.
func (f *accessFixture) deliverAt(t *testing.T, mailbox *domain.Mailbox, in delivery, at time.Time) *domain.MailMessage {
	t.Helper()
	message := f.deliver(t, mailbox, in)
	f.dateMessage(t, message, at)
	return message
}

// sendAt stores an outbound message of mailbox created at the given time, as a send would.
func (f *accessFixture) sendAt(t *testing.T, mailbox *domain.Mailbox, subject string, at time.Time) *domain.MailMessage {
	t.Helper()
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusSent,
		FromAddress: mailbox.Address, To: []string{"x@example.com"}, Subject: subject, TextBody: "body",
	}
	if _, err := f.inboxRepo().CreateMessage(context.Background(), message); err != nil {
		t.Fatalf("CreateMessage(%q): %v", subject, err)
	}
	f.dateMessage(t, message, at)
	return message
}

func (f *accessFixture) inbox(t *testing.T, query domain.InboxQuery) []*domain.MailMessage {
	t.Helper()
	messages, err := f.inboxRepo().ListInbox(context.Background(), query)
	if err != nil {
		t.Fatalf("ListInbox(%+v): %v", query, err)
	}
	return messages
}

func minutes(n int) time.Time {
	return inboxBase.Add(time.Duration(n) * time.Minute)
}

func TestInboxAccessRowsListMemberRowsWithoutCounts(t *testing.T) {
	f := newInboxFixture(t)
	ctx := context.Background()
	repo := f.inboxRepo()

	gone, err := f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{
		LocalPart: "gone", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessC, Permission: domain.MailboxPermissionView}},
	})
	if err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	if err := repo.db.Exec(`UPDATE mailboxes SET deleted_at = now() WHERE id = ?`, gone.ID).Error; err != nil {
		t.Fatalf("delete mailbox: %v", err)
	}
	createOwner(t, repo.db, 99)
	foreign := &domain.Mailbox{OrganizationID: 99, Address: "foreign@mail.example", Type: domain.MailboxTypeShared}
	if err := repo.CreateMailbox(ctx, foreign, domain.MaxMailboxesPerOrganization); err != nil {
		t.Fatalf("create foreign mailbox: %v", err)
	}

	cases := []struct {
		name   string
		viewer domain.OrganizationContext
		member map[uint]domain.MailboxPermission // mailbox id -> the viewer's member row
	}{
		{"B, owner of b@ and send member of team@", f.b, map[uint]domain.MailboxPermission{f.team.ID: domain.MailboxPermissionSend}},
		{"C, view member of team@", f.c, map[uint]domain.MailboxPermission{f.team.ID: domain.MailboxPermissionView}},
		{"D, member of nothing", f.d, map[uint]domain.MailboxPermission{}},
	}
	for _, tc := range cases {
		rows, err := repo.ListMailboxAccessRows(ctx, accessOrg, tc.viewer.MembershipID)
		if err != nil {
			t.Fatalf("%s: ListMailboxAccessRows: %v", tc.name, err)
		}
		ids := make([]uint, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.Mailbox.ID)
			if row.Mailbox.UnreadCount != 0 {
				t.Fatalf("%s: mailbox %d has unread count %d; want none counted", tc.name, row.Mailbox.ID, row.Mailbox.UnreadCount)
			}
			if row.Mailbox.Address == "" || row.Mailbox.OrganizationID != accessOrg {
				t.Fatalf("%s: mailbox row %+v is incomplete", tc.name, row.Mailbox)
			}
			permission, want := tc.member[row.Mailbox.ID]
			switch {
			case want && (row.Member == nil || row.Member.Permission != permission || row.Member.MembershipID != tc.viewer.MembershipID):
				t.Fatalf("%s: mailbox %d member = %+v; want %s of the viewer", tc.name, row.Mailbox.ID, row.Member, permission)
			case !want && row.Member != nil:
				t.Fatalf("%s: mailbox %d member = %+v; want none", tc.name, row.Mailbox.ID, row.Member)
			}
		}
		expectIDs(t, tc.name+": mailboxes", ids, f.personal.ID, f.team.ID)
	}
}

func TestInboxListMergesMailboxesNewestFirst(t *testing.T) {
	f := newInboxFixture(t)
	p1 := f.deliverAt(t, f.personal, delivery{subject: "p1"}, minutes(1))
	t1 := f.deliverAt(t, f.team, delivery{subject: "t1"}, minutes(2))
	p2 := f.deliverAt(t, f.personal, delivery{subject: "p2"}, minutes(3))
	t2 := f.deliverAt(t, f.team, delivery{subject: "t2"}, minutes(4))

	rows := f.inbox(t, domain.InboxQuery{
		MailboxIDs: []uint{f.personal.ID, f.team.ID}, MembershipID: f.b.MembershipID, View: domain.InboxViewAll, Limit: 10,
	})
	expectIDs(t, "merged inbox", idsOf(rows), t2.ID, p2.ID, t1.ID, p1.ID, f.teamMessage.ID, f.personalMessage.ID)
	for _, row := range rows {
		want := f.personal.Address
		if row.MailboxID == f.team.ID {
			want = f.team.Address
		}
		if row.MailboxAddress != want {
			t.Fatalf("message %d mailbox address = %q; want %q", row.ID, row.MailboxAddress, want)
		}
		if row.TextBody != "" || row.HTMLBody != "" {
			t.Fatalf("message %d carries its body in a listing", row.ID)
		}
		if row.Thread != nil {
			t.Fatalf("message %d has a thread outside a grouped listing", row.ID)
		}
	}

	// The limit applies to the merged list, not per mailbox.
	rows = f.inbox(t, domain.InboxQuery{
		MailboxIDs: []uint{f.personal.ID, f.team.ID}, MembershipID: f.b.MembershipID, View: domain.InboxViewAll, Limit: 3,
	})
	expectIDs(t, "first three", idsOf(rows), t2.ID, p2.ID, t1.ID)
}

func TestInboxCursorPagesWithoutGapsOrRepeats(t *testing.T) {
	f := newInboxFixture(t)
	m1 := f.deliverAt(t, f.team, delivery{subject: "m1"}, minutes(1))
	m2 := f.deliverAt(t, f.team, delivery{subject: "m2"}, minutes(3))
	m3 := f.deliverAt(t, f.team, delivery{subject: "m3"}, minutes(3)) // same instant as m2
	m4 := f.deliverAt(t, f.team, delivery{subject: "m4"}, minutes(4))
	m5 := f.deliverAt(t, f.team, delivery{subject: "m5"}, minutes(5))

	query := domain.InboxQuery{MailboxIDs: []uint{f.team.ID}, MembershipID: f.c.MembershipID, View: domain.InboxViewAll, Limit: 3}
	first := f.inbox(t, query)
	expectIDs(t, "page one", idsOf(first), m5.ID, m4.ID, m3.ID)

	// Mail that arrives between the pages belongs before page one and never shifts page two.
	f.deliver(t, f.team, delivery{subject: "late"})

	last := first[len(first)-1]
	query.After = &domain.InboxCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	second := f.inbox(t, query)
	expectIDs(t, "page two", idsOf(second), m2.ID, m1.ID, f.teamMessage.ID)

	last = second[len(second)-1]
	query.After = &domain.InboxCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	expectIDs(t, "page three", idsOf(f.inbox(t, query)))
}

func TestInboxExcludesSealedArchivedAndTrash(t *testing.T) {
	f := newInboxFixture(t)
	db := f.inboxRepo().db
	kept := f.deliverAt(t, f.team, delivery{subject: "kept"}, minutes(1))
	archived := f.deliverAt(t, f.team, delivery{subject: "archived"}, minutes(2))
	trashed := f.deliverAt(t, f.team, delivery{subject: "trashed"}, minutes(3))
	folder := f.folder(t, f.e, f.team.ID, "Invoices")
	filed := f.deliverAt(t, f.team, delivery{subject: "filed"}, minutes(4))
	f.sendAt(t, f.team, "sent", minutes(5))
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)
	f.move(t, domain.MailPlacementFolder, folder.ID, filed.ID)
	if err := db.Exec(`UPDATE mail_messages SET placement = 'trash', trashed_at = now() WHERE id = ?`, trashed.ID).Error; err != nil {
		t.Fatalf("trash message: %v", err)
	}
	// The seal hides everything created before it: the seeded team message.
	if err := db.Exec(`UPDATE mailboxes SET sealed_before = ? WHERE id = ?`, inboxBase, f.team.ID).Error; err != nil {
		t.Fatalf("seal mailbox: %v", err)
	}

	for _, grouped := range []bool{false, true} {
		rows := f.inbox(t, domain.InboxQuery{
			MailboxIDs: []uint{f.team.ID}, MembershipID: f.c.MembershipID, View: domain.InboxViewAll, GroupThreads: grouped, Limit: 10,
		})
		expectIDs(t, fmt.Sprintf("inbox (grouped %v)", grouped), idsOf(rows), kept.ID)
	}
}

func TestInboxThreadHeadIsNewestInView(t *testing.T) {
	f := newInboxFixture(t)
	ctx := context.Background()
	root := f.deliverAt(t, f.team, delivery{subject: "Quote", rfcID: "root@sender.test"}, minutes(1))
	other := f.deliverAt(t, f.team, delivery{subject: "Other", rfcID: "other@sender.test"}, minutes(2))
	reply := f.deliverAt(t, f.team, delivery{subject: "Re: Quote", rfcID: "r1@sender.test", inReplyTo: "root@sender.test"}, minutes(3))
	archived := f.deliverAt(t, f.team, delivery{subject: "Re: Re: Quote", rfcID: "r2@sender.test", inReplyTo: "r1@sender.test"}, minutes(4))
	if reply.ThreadID != root.ID || archived.ThreadID != root.ID {
		t.Fatalf("thread ids = %d, %d; want %d", reply.ThreadID, archived.ThreadID, root.ID)
	}
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)
	urgent := f.label(t, f.e, f.team.ID, "Urgent")
	f.change(t, f.e, f.team.ID, MessageUpdate{AddLabelIDs: []uint{urgent.ID}}, root.ID)
	f.change(t, f.c, f.team.ID, MessageUpdate{Starred: boolPtr(true)}, root.ID)

	rows := f.inbox(t, domain.InboxQuery{
		MailboxIDs: []uint{f.team.ID}, MembershipID: f.c.MembershipID, View: domain.InboxViewAll, GroupThreads: true, Limit: 10,
	})
	expectIDs(t, "thread heads", idsOf(rows), reply.ID, other.ID, f.teamMessage.ID)

	view := domain.MailMessageFilter{Placement: domain.MailPlacementDefault, Direction: domain.MailDirectionInbound, GroupThreads: true}
	threads, _, err := f.inboxRepo().ListThreads(ctx, f.team.ID, f.c.MembershipID, view)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != len(rows) {
		t.Fatalf("ListThreads has %d rows; the inbox has %d", len(threads), len(rows))
	}
	for i, row := range rows {
		if row.Thread == nil {
			t.Fatalf("row %d has no thread", row.ID)
		}
		want := threads[i]
		if want.Latest.ID != row.ID {
			t.Fatalf("row %d: ListThreads has %d at that position", row.ID, want.Latest.ID)
		}
		expectIDs(t, fmt.Sprintf("thread of %d", row.ID), row.Thread.MessageIDs, want.MessageIDs...)
		expectIDs(t, fmt.Sprintf("labels of %d", row.ID), row.Thread.LabelIDs, want.LabelIDs...)
		if row.Thread.MessageCount != want.MessageCount || row.Thread.Unread != want.Unread || row.Thread.Starred != want.Starred {
			t.Fatalf("thread of %d = %+v; want %+v", row.ID, *row.Thread, want.MailThreadInfo)
		}
	}
	if head := rows[0].Thread; head.MessageCount != 2 || !head.Starred || !head.Unread {
		t.Fatalf("root thread = %+v; want two messages, starred, unread", *head)
	}
}

func TestInboxThreadsNeverCrossMailboxes(t *testing.T) {
	f := newInboxFixture(t)
	mine := f.deliverAt(t, f.personal, delivery{subject: "Hello", rfcID: "a@sender.test", inReplyTo: "b@sender.test"}, minutes(1))
	theirs := f.deliverAt(t, f.team, delivery{subject: "Re: Hello", rfcID: "b@sender.test", inReplyTo: "a@sender.test"}, minutes(2))
	if mine.ThreadID == theirs.ThreadID {
		t.Fatalf("messages of two mailboxes share thread %d", mine.ThreadID)
	}

	rows := f.inbox(t, domain.InboxQuery{
		MailboxIDs: []uint{f.personal.ID, f.team.ID}, MembershipID: f.b.MembershipID, View: domain.InboxViewAll, GroupThreads: true, Limit: 10,
	})
	expectIDs(t, "thread heads", idsOf(rows), theirs.ID, mine.ID, f.teamMessage.ID, f.personalMessage.ID)
	for _, row := range rows[:2] {
		if row.Thread == nil || row.Thread.MessageCount != 1 {
			t.Fatalf("row %d thread = %+v; want one message", row.ID, row.Thread)
		}
		expectIDs(t, fmt.Sprintf("thread of %d", row.ID), row.Thread.MessageIDs, row.ID)
	}
}

func TestInboxSentListsOutboundDefault(t *testing.T) {
	f := newInboxFixture(t)
	first := f.sendAt(t, f.team, "first", minutes(1))
	archived := f.sendAt(t, f.team, "archived", minutes(2))
	mine := f.sendAt(t, f.personal, "mine", minutes(3))
	f.deliverAt(t, f.team, delivery{subject: "inbound"}, minutes(4))
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)

	for _, grouped := range []bool{false, true} {
		rows := f.inbox(t, domain.InboxQuery{
			MailboxIDs: []uint{f.personal.ID, f.team.ID}, MembershipID: f.b.MembershipID, View: domain.InboxViewSent, GroupThreads: grouped, Limit: 10,
		})
		expectIDs(t, fmt.Sprintf("sent (grouped %v)", grouped), idsOf(rows), mine.ID, first.ID)
		for _, row := range rows {
			if row.Direction != domain.MailDirectionOutbound {
				t.Fatalf("sent lists %s message %d", row.Direction, row.ID)
			}
			if grouped != (row.Thread != nil) {
				t.Fatalf("sent row %d thread = %+v (grouped %v)", row.ID, row.Thread, grouped)
			}
		}
	}
}

func TestInboxSnippetsReadFirst400Characters(t *testing.T) {
	f := newInboxFixture(t)
	ctx := context.Background()
	repo := f.inboxRepo()
	long := f.deliver(t, f.team, delivery{subject: "Long", body: strings.Repeat("界a", 2500)})
	htmlOnly := &domain.MailMessage{
		MailboxID: f.team.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "alice@sender.test", To: []string{f.team.Address}, Subject: "HTML", HTMLBody: "<p>Hello</p>",
		DedupeKey: "html-only",
	}
	if _, err := repo.CreateInboundMessage(ctx, htmlOnly); err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}

	snippets, err := repo.MessageSnippets(ctx, []uint{long.ID, htmlOnly.ID})
	if err != nil {
		t.Fatalf("MessageSnippets: %v", err)
	}
	if got := snippets[long.ID]; utf8.RuneCountInString(got) != 400 || got != strings.Repeat("界a", 200) {
		t.Fatalf("long snippet has %d characters; want the first 400", utf8.RuneCountInString(got))
	}
	if got, ok := snippets[htmlOnly.ID]; !ok || got != "" {
		t.Fatalf("HTML-only snippet = %q (present %v); want an empty string", got, ok)
	}

	empty, err := repo.MessageSnippets(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("MessageSnippets(nil) = %v, %v; want an empty map", empty, err)
	}
}

func TestInboxRefusesQueriesItDoesNotServe(t *testing.T) {
	f := newInboxFixture(t)
	ctx := context.Background()
	base := domain.InboxQuery{MailboxIDs: []uint{f.team.ID}, MembershipID: f.c.MembershipID, View: domain.InboxViewAll, Limit: 10}
	refused := map[string]func(*domain.InboxQuery){
		"unknown view": func(q *domain.InboxQuery) { q.View = "bogus" },
		"today":        func(q *domain.InboxQuery) { q.View = domain.InboxViewToday },
		"pending":      func(q *domain.InboxQuery) { q.View = domain.InboxViewPending },
		"unread":       func(q *domain.InboxQuery) { q.Unread = boolPtr(true) },
		"search":       func(q *domain.InboxQuery) { q.Query = "quote" },
	}
	for name, change := range refused {
		query := base
		change(&query)
		if _, err := f.inboxRepo().ListInbox(ctx, query); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("%s: ListInbox error = %v; want ErrInvalidInput", name, err)
		}
	}

	empty := base
	empty.MailboxIDs = nil
	if rows, err := f.inboxRepo().ListInbox(ctx, empty); err != nil || len(rows) != 0 {
		t.Fatalf("ListInbox without mailboxes = %v, %v; want an empty list", idsOf(rows), err)
	}
	if _, err := (&repository{}).ListInbox(ctx, base); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListInbox without a database error = %v; want ErrServiceUnavailable", err)
	}
}
