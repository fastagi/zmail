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
	today := func(q *domain.InboxQuery) {
		q.View, q.DayStart, q.DayEnd = domain.InboxViewToday, minutes(0), minutes(24*60)
	}
	refused := map[string]func(*domain.InboxQuery){
		"unknown view":          func(q *domain.InboxQuery) { q.View = "bogus" },
		"today without its day": func(q *domain.InboxQuery) { q.View = domain.InboxViewToday },
		"search outside all":    func(q *domain.InboxQuery) { q.View, q.Query = domain.InboxViewSent, "quote" },
		"grouped today":         func(q *domain.InboxQuery) { today(q); q.GroupThreads = true },
		"grouped pending":       func(q *domain.InboxQuery) { q.View, q.GroupThreads = domain.InboxViewPending, true },
		"grouped unread":        func(q *domain.InboxQuery) { q.Unread, q.GroupThreads = boolPtr(true), true },
		"grouped search":        func(q *domain.InboxQuery) { q.Query, q.GroupThreads = "quote", true },
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

// trash moves messages to the trash directly, as the retention tests do.
func (f *accessFixture) trash(t *testing.T, ids ...uint) {
	t.Helper()
	if err := f.inboxRepo().db.Exec(`UPDATE mail_messages SET placement = 'trash', trashed_at = now() WHERE id IN ?`, ids).Error; err != nil {
		t.Fatalf("trash messages %v: %v", ids, err)
	}
}

// seal hides the mailbox's messages created before at.
func (f *accessFixture) seal(t *testing.T, mailbox *domain.Mailbox, at time.Time) {
	t.Helper()
	if err := f.inboxRepo().db.Exec(`UPDATE mailboxes SET sealed_before = ? WHERE id = ?`, at, mailbox.ID).Error; err != nil {
		t.Fatalf("seal mailbox %d: %v", mailbox.ID, err)
	}
}

// queue marks outbound messages as waiting for the send limit, as a send over the limit would.
func (f *accessFixture) queue(t *testing.T, ids ...uint) {
	t.Helper()
	if err := f.inboxRepo().db.Exec(`UPDATE mail_messages SET status = 'queued', queued_reason = 'send_limit' WHERE id IN ?`, ids).Error; err != nil {
		t.Fatalf("queue messages %v: %v", ids, err)
	}
}

// pages lists the query page by page, limit rows at a time, and returns every row in order.
func (f *accessFixture) pages(t *testing.T, query domain.InboxQuery, limit int) []uint {
	t.Helper()
	query.Limit = limit
	ids := []uint{}
	for range 20 {
		rows := f.inbox(t, query)
		ids = append(ids, idsOf(rows)...)
		if len(rows) < limit {
			return ids
		}
		last := rows[len(rows)-1]
		query.After = &domain.InboxCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	t.Fatalf("paging %+v never ended", query)
	return nil
}

func TestInboxTodayIncludesArchivedNotTrash(t *testing.T) {
	f := newInboxFixture(t)
	dayStart, dayEnd := minutes(60), minutes(60+24*60)
	f.deliverAt(t, f.team, delivery{subject: "yesterday"}, minutes(30))
	atStart := f.deliverAt(t, f.team, delivery{subject: "at start"}, dayStart)
	archived := f.deliverAt(t, f.team, delivery{subject: "archived"}, minutes(90))
	trashed := f.deliverAt(t, f.team, delivery{subject: "trashed"}, minutes(100))
	folder := f.folder(t, f.e, f.team.ID, "Invoices")
	filed := f.deliverAt(t, f.team, delivery{subject: "filed"}, minutes(110))
	mine := f.deliverAt(t, f.personal, delivery{subject: "mine"}, minutes(120))
	f.sendAt(t, f.team, "sent today", minutes(130))
	f.deliverAt(t, f.team, delivery{subject: "at end"}, dayEnd)
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)
	f.move(t, domain.MailPlacementFolder, folder.ID, filed.ID)
	f.trash(t, trashed.ID)

	query := domain.InboxQuery{
		MailboxIDs: []uint{f.personal.ID, f.team.ID}, MembershipID: f.b.MembershipID,
		View: domain.InboxViewToday, DayStart: dayStart, DayEnd: dayEnd, Limit: 10,
	}
	expectIDs(t, "today", idsOf(f.inbox(t, query)), mine.ID, filed.ID, archived.ID, atStart.ID)
	expectIDs(t, "today page by page", f.pages(t, query, 1), mine.ID, filed.ID, archived.ID, atStart.ID)

	query.Unread = boolPtr(true)
	f.change(t, f.b, f.team.ID, MessageUpdate{Read: boolPtr(true)}, archived.ID)
	expectIDs(t, "today unread", idsOf(f.inbox(t, query)), mine.ID, filed.ID, atStart.ID)
}

func TestInboxPendingIsMyStarsOutsideArchiveAndTrash(t *testing.T) {
	f := newInboxFixture(t)
	starredIn := f.deliverAt(t, f.team, delivery{subject: "in"}, minutes(1))
	starredOut := f.sendAt(t, f.team, "out", minutes(2))
	folder := f.folder(t, f.e, f.team.ID, "Invoices")
	starredFiled := f.deliverAt(t, f.team, delivery{subject: "filed"}, minutes(3))
	starredArchived := f.deliverAt(t, f.team, delivery{subject: "archived"}, minutes(4))
	starredTrashed := f.deliverAt(t, f.team, delivery{subject: "trashed"}, minutes(5))
	theirs := f.deliverAt(t, f.team, delivery{subject: "their star"}, minutes(6))
	f.deliverAt(t, f.team, delivery{subject: "not starred"}, minutes(7))
	star := MessageUpdate{Starred: boolPtr(true)}
	f.change(t, f.c, f.team.ID, star, starredIn.ID, starredOut.ID, starredFiled.ID, starredArchived.ID, starredTrashed.ID, f.teamMessage.ID)
	f.change(t, f.b, f.team.ID, star, theirs.ID)
	f.move(t, domain.MailPlacementFolder, folder.ID, starredFiled.ID)
	f.move(t, domain.MailPlacementArchive, 0, starredArchived.ID)
	f.trash(t, starredTrashed.ID)
	// The seal hides the starred seeded message.
	f.seal(t, f.team, inboxBase)

	query := domain.InboxQuery{MailboxIDs: []uint{f.team.ID}, MembershipID: f.c.MembershipID, View: domain.InboxViewPending, Limit: 10}
	expectIDs(t, "pending", idsOf(f.inbox(t, query)), starredFiled.ID, starredOut.ID, starredIn.ID)
	expectIDs(t, "pending page by page", f.pages(t, query, 2), starredFiled.ID, starredOut.ID, starredIn.ID)

	// A star in a mailbox outside the given ids never lists.
	query.MailboxIDs = []uint{f.personal.ID}
	expectIDs(t, "pending outside the mailboxes", idsOf(f.inbox(t, query)))
}

func TestInboxSearchSpansPlacementsButTrash(t *testing.T) {
	f := newInboxFixture(t)
	alpha := f.deliverAt(t, f.team, delivery{subject: "Quote alpha"}, minutes(1))
	beta := f.deliverAt(t, f.team, delivery{subject: "Quote beta"}, minutes(2))
	folder := f.folder(t, f.e, f.team.ID, "Invoices")
	gamma := f.deliverAt(t, f.team, delivery{subject: "Quote gamma"}, minutes(3))
	delta := f.sendAt(t, f.team, "Quote delta", minutes(4))
	trashed := f.deliverAt(t, f.team, delivery{subject: "Quote trashed"}, minutes(5))
	f.deliverAt(t, f.team, delivery{subject: "Other"}, minutes(6))
	f.deliverAt(t, f.personal, delivery{subject: "Quote elsewhere"}, minutes(7))
	f.move(t, domain.MailPlacementArchive, 0, beta.ID)
	f.move(t, domain.MailPlacementFolder, folder.ID, gamma.ID)
	f.trash(t, trashed.ID)

	query := domain.InboxQuery{MailboxIDs: []uint{f.team.ID}, MembershipID: f.c.MembershipID, View: domain.InboxViewAll, Query: "quote", Limit: 10}
	expectIDs(t, "search", idsOf(f.inbox(t, query)), delta.ID, gamma.ID, beta.ID, alpha.ID)
	expectIDs(t, "search page by page", f.pages(t, query, 3), delta.ID, gamma.ID, beta.ID, alpha.ID)

	f.change(t, f.c, f.team.ID, MessageUpdate{Read: boolPtr(true)}, alpha.ID)
	query.Unread = boolPtr(true)
	expectIDs(t, "unread search", idsOf(f.inbox(t, query)), gamma.ID, beta.ID)
	query.Unread = boolPtr(false)
	expectIDs(t, "read search", idsOf(f.inbox(t, query)), delta.ID, alpha.ID)

	// The search text matches literally: a wildcard matches only itself.
	query.Unread, query.Query = nil, "%"
	expectIDs(t, "wildcard search", idsOf(f.inbox(t, query)))
}

func TestInboxSearchTimeoutIsTooBroad(t *testing.T) {
	f := newInboxFixture(t)
	repo := f.inboxRepo()
	widenPool(t, repo.db)
	repo.searchTimeout = 50 * time.Millisecond

	holder := repo.db.Begin()
	// A failing assertion must still release the table, or dropping the test schema waits forever.
	released := false
	release := func() {
		if !released {
			released = true
			if err := holder.Rollback().Error; err != nil {
				t.Errorf("release mail_messages: %v", err)
			}
		}
	}
	t.Cleanup(release)
	if err := holder.Exec(`LOCK TABLE mail_messages IN ACCESS EXCLUSIVE MODE`).Error; err != nil {
		t.Fatalf("lock mail_messages: %v", err)
	}
	query := domain.InboxQuery{MailboxIDs: []uint{f.team.ID}, MembershipID: f.c.MembershipID, View: domain.InboxViewAll, Query: "Team", Limit: 10}
	_, err := repo.ListInbox(context.Background(), query)
	expectError(t, "an inbox search that times out", err, ErrMailSearchTooBroad)
	release()

	expectIDs(t, "the same search once the table is free", idsOf(f.inbox(t, query)), f.teamMessage.ID)
}

func TestInboxUnreadNeverListsOutbound(t *testing.T) {
	f := newInboxFixture(t)
	unread := f.deliverAt(t, f.team, delivery{subject: "unread"}, minutes(1))
	read := f.deliverAt(t, f.team, delivery{subject: "read"}, minutes(2))
	sent := f.sendAt(t, f.team, "sent", minutes(3))
	f.change(t, f.c, f.team.ID, MessageUpdate{Read: boolPtr(true)}, read.ID, f.teamMessage.ID)
	f.change(t, f.c, f.team.ID, MessageUpdate{Starred: boolPtr(true)}, unread.ID, read.ID, sent.ID)

	base := domain.InboxQuery{MailboxIDs: []uint{f.team.ID}, MembershipID: f.c.MembershipID, Limit: 10}
	cases := []struct {
		name   string
		view   domain.InboxView
		unread bool
		want   []uint
	}{
		{"all unread", domain.InboxViewAll, true, []uint{unread.ID}},
		{"all read", domain.InboxViewAll, false, []uint{read.ID, f.teamMessage.ID}},
		{"today unread", domain.InboxViewToday, true, []uint{unread.ID}},
		{"pending unread", domain.InboxViewPending, true, []uint{unread.ID}},
		{"pending read", domain.InboxViewPending, false, []uint{sent.ID, read.ID}},
		{"sent unread", domain.InboxViewSent, true, nil},
		{"sent read", domain.InboxViewSent, false, []uint{sent.ID}},
	}
	for _, tc := range cases {
		query := base
		query.View, query.Unread = tc.view, boolPtr(tc.unread)
		if tc.view == domain.InboxViewToday {
			query.DayStart, query.DayEnd = minutes(0), minutes(24*60)
		}
		expectIDs(t, tc.name, idsOf(f.inbox(t, query)), tc.want...)
	}
}

func TestInboxCountUnreadOnlyGivenMailboxes(t *testing.T) {
	f := newInboxFixture(t)
	ctx := context.Background()
	repo := f.inboxRepo()
	createOwner(t, repo.db, 99)
	foreign := &domain.Mailbox{OrganizationID: 99, Address: "foreign@mail.example", Type: domain.MailboxTypeShared}
	if err := repo.CreateMailbox(ctx, foreign, domain.MaxMailboxesPerOrganization); err != nil {
		t.Fatalf("create foreign mailbox: %v", err)
	}
	f.deliver(t, foreign, delivery{subject: "foreign"})
	f.deliver(t, f.personal, delivery{subject: "mine"})

	counts, err := repo.CountUnreadByMailboxIDs(ctx, accessOrg, f.b.MembershipID, []uint{f.personal.ID, foreign.ID})
	if err != nil {
		t.Fatalf("CountUnreadByMailboxIDs: %v", err)
	}
	if len(counts) != 1 || counts[f.personal.ID] != 2 {
		t.Fatalf("counts = %v; want only b@ with 2", counts)
	}

	counts, err = repo.CountUnreadByMailboxIDs(ctx, accessOrg, f.b.MembershipID, []uint{f.team.ID})
	if err != nil {
		t.Fatalf("CountUnreadByMailboxIDs: %v", err)
	}
	if len(counts) != 1 || counts[f.team.ID] != 1 {
		t.Fatalf("counts = %v; want only team@ with 1", counts)
	}

	empty, err := (&repository{}).CountUnreadByMailboxIDs(ctx, 1, 1, nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("CountUnreadByMailboxIDs(nil) = %v, %v; want an empty map", empty, err)
	}
	if _, err := (&repository{}).CountUnreadByMailboxIDs(ctx, 1, 1, []uint{1}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CountUnreadByMailboxIDs without a database error = %v; want ErrServiceUnavailable", err)
	}
}

func TestInboxCountUnreadMatchesListMailboxes(t *testing.T) {
	f := newInboxFixture(t)
	ctx := context.Background()
	repo := f.inboxRepo()
	folder := f.folder(t, f.e, f.team.ID, "Invoices")
	read := f.deliverAt(t, f.team, delivery{subject: "read by C"}, minutes(1))
	archived := f.deliverAt(t, f.team, delivery{subject: "archived"}, minutes(2))
	filed := f.deliverAt(t, f.team, delivery{subject: "filed"}, minutes(3))
	trashed := f.deliverAt(t, f.team, delivery{subject: "trashed"}, minutes(4))
	f.deliverAt(t, f.team, delivery{subject: "unread"}, minutes(5))
	f.sendAt(t, f.team, "sent", minutes(6))
	f.deliverAt(t, f.personal, delivery{subject: "mine"}, minutes(7))
	f.change(t, f.c, f.team.ID, MessageUpdate{Read: boolPtr(true)}, read.ID)
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)
	f.move(t, domain.MailPlacementFolder, folder.ID, filed.ID)
	f.trash(t, trashed.ID)
	f.seal(t, f.team, inboxBase)

	for _, viewer := range []domain.OrganizationContext{f.b, f.c} {
		listed, err := repo.ListMailboxes(ctx, accessOrg, viewer.MembershipID)
		if err != nil {
			t.Fatalf("ListMailboxes: %v", err)
		}
		ids := make([]uint, 0, len(listed))
		for _, mailbox := range listed {
			ids = append(ids, mailbox.ID)
		}
		counts, err := repo.CountUnreadByMailboxIDs(ctx, accessOrg, viewer.MembershipID, ids)
		if err != nil {
			t.Fatalf("CountUnreadByMailboxIDs: %v", err)
		}
		for _, mailbox := range listed {
			if counts[mailbox.ID] != mailbox.UnreadCount {
				t.Fatalf("membership %d, mailbox %d: count %d; ListMailboxes has %d",
					viewer.MembershipID, mailbox.ID, counts[mailbox.ID], mailbox.UnreadCount)
			}
		}
	}
	counts, err := repo.CountUnreadByMailboxIDs(ctx, accessOrg, f.c.MembershipID, []uint{f.team.ID})
	if err != nil || counts[f.team.ID] != 1 {
		t.Fatalf("C's team@ count = %v, %v; want 1, the unread Inbox message after the seal", counts, err)
	}
}

func TestInboxCountViews(t *testing.T) {
	f := newInboxFixture(t)
	ctx := context.Background()
	repo := f.inboxRepo()
	dayStart, dayEnd := minutes(60), minutes(60+24*60)
	ids := []uint{f.personal.ID, f.team.ID}

	// Today: inbound outside the trash within [dayStart, dayEnd).
	f.deliverAt(t, f.team, delivery{subject: "yesterday"}, minutes(30))
	unread := f.deliverAt(t, f.team, delivery{subject: "unread"}, dayStart)
	read := f.deliverAt(t, f.team, delivery{subject: "read"}, minutes(70))
	archived := f.deliverAt(t, f.team, delivery{subject: "archived"}, minutes(80))
	trashed := f.deliverAt(t, f.team, delivery{subject: "trashed"}, minutes(90))
	f.deliverAt(t, f.personal, delivery{subject: "mine"}, minutes(100))
	sent := f.sendAt(t, f.team, "sent today", minutes(110))
	f.deliverAt(t, f.team, delivery{subject: "tomorrow"}, dayEnd)
	f.change(t, f.b, f.team.ID, MessageUpdate{Read: boolPtr(true)}, read.ID)
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)
	f.trash(t, trashed.ID)

	// Pending: B's stars outside the archive and the trash, either direction, unsealed.
	f.change(t, f.b, f.team.ID, MessageUpdate{Starred: boolPtr(true)}, unread.ID, sent.ID, archived.ID, f.teamMessage.ID)
	f.change(t, f.c, f.team.ID, MessageUpdate{Starred: boolPtr(true)}, read.ID)

	// Waiting: queued for the send limit and unsealed.
	waitingTeam := f.sendAt(t, f.team, "waiting", minutes(120))
	waitingMine := f.sendAt(t, f.personal, "waiting mine", minutes(130))
	sealedWaiting := f.sendAt(t, f.team, "sealed waiting", inboxBase.Add(-30*time.Minute))
	f.queue(t, waitingTeam.ID, waitingMine.ID, sealedWaiting.ID)
	f.seal(t, f.team, inboxBase)

	counts, err := repo.CountInboxViews(ctx, f.b.MembershipID, ids, dayStart, dayEnd)
	if err != nil {
		t.Fatalf("CountInboxViews: %v", err)
	}
	want := domain.InboxViewCounts{Today: 4, TodayUnread: 3, Pending: 2, SentWaiting: 2}
	if counts != want {
		t.Fatalf("counts = %+v; want %+v", counts, want)
	}

	counts, err = repo.CountInboxViews(ctx, f.b.MembershipID, []uint{f.personal.ID}, dayStart, dayEnd)
	if err != nil {
		t.Fatalf("CountInboxViews: %v", err)
	}
	want = domain.InboxViewCounts{Today: 1, TodayUnread: 1, Pending: 0, SentWaiting: 1}
	if counts != want {
		t.Fatalf("b@ counts = %+v; want %+v", counts, want)
	}

	empty, err := (&repository{}).CountInboxViews(ctx, 1, nil, dayStart, dayEnd)
	if err != nil || empty != (domain.InboxViewCounts{}) {
		t.Fatalf("CountInboxViews(nil) = %+v, %v; want zero counts", empty, err)
	}
}
