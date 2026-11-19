package mailbox

import (
	"context"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

// readRows counts the stored read rows, optionally of one membership.
func readRows(t *testing.T, f *accessFixture, membershipID uint) int64 {
	t.Helper()
	query := f.repo.(*repository).db.Model(&MailMessageReadPO{})
	if membershipID != 0 {
		query = query.Where("membership_id = ?", membershipID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		t.Fatalf("count reads: %v", err)
	}
	return count
}

func readOf(t *testing.T, f *accessFixture, viewer domain.OrganizationContext, mailboxID, messageID uint) bool {
	t.Helper()
	message, err := f.service.GetMessage(context.Background(), viewer, mailboxID, messageID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	return message.Read
}

func unreadOf(t *testing.T, f *accessFixture, viewer domain.OrganizationContext, mailboxID uint) int {
	t.Helper()
	mailbox := listedMailbox(t, f.service, viewer, mailboxID)
	if mailbox == nil {
		t.Fatalf("mailbox %d not listed", mailboxID)
	}
	return mailbox.UnreadCount
}

// AC-5: B reads a message of the shared mailbox; C's read state does not change.
func TestReadIsPerPerson(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box, message := f.team.ID, f.teamMessage.ID

	marked, err := f.service.SetMessageRead(ctx, f.b, box, message, true)
	if err != nil {
		t.Fatalf("B SetMessageRead: %v", err)
	}
	if !marked.Read {
		t.Fatal("B's answer after marking read says unread")
	}
	if !readOf(t, f, f.b, box, message) {
		t.Fatal("B's message is not read after B read it")
	}
	if readOf(t, f, f.c, box, message) {
		t.Fatal("C's message turned read when B read it")
	}
	listed, _, err := f.service.ListMessages(ctx, f.c, box, domain.MailMessageFilter{})
	if err != nil || len(listed) != 1 || listed[0].Read {
		t.Fatalf("C's listing = %+v, %v; want the message unread", listed, err)
	}
	log, _, err := f.service.ListLog(ctx, f.c, domain.MailLogFilter{MailboxID: box})
	if err != nil || len(log) != 1 || log[0].Read {
		t.Fatalf("C's log = %+v, %v; want the message unread", log, err)
	}
	log, _, err = f.service.ListLog(ctx, f.b, domain.MailLogFilter{MailboxID: box})
	if err != nil || len(log) != 1 || !log[0].Read {
		t.Fatalf("B's log = %+v, %v; want the message read", log, err)
	}
}

// unread_count is the viewer's own: one person's reading leaves everyone else's count alone.
func TestUnreadCountIsPerViewer(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box := f.team.ID
	f.receive(t, f.team, "Second update")

	if unreadOf(t, f, f.b, box) != 2 || unreadOf(t, f, f.c, box) != 2 {
		t.Fatalf("unread before reading = B %d, C %d; want 2 and 2", unreadOf(t, f, f.b, box), unreadOf(t, f, f.c, box))
	}
	if _, err := f.service.SetMessageRead(ctx, f.b, box, f.teamMessage.ID, true); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}
	if got := unreadOf(t, f, f.b, box); got != 1 {
		t.Fatalf("B unread = %d; want 1", got)
	}
	if got := unreadOf(t, f, f.c, box); got != 2 {
		t.Fatalf("C unread = %d; want 2", got)
	}
	// Outbound mail never counts.
	if _, _, err := f.service.SendMessage(ctx, f.b, box, sendInput()); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := unreadOf(t, f, f.c, box); got != 2 {
		t.Fatalf("C unread after a send = %d; want 2", got)
	}
}

// Marking unread deletes only the viewer's read row.
func TestMarkUnreadDeletesOnlyTheViewersRow(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box, message := f.team.ID, f.teamMessage.ID
	for _, viewer := range []domain.OrganizationContext{f.b, f.c} {
		if _, err := f.service.SetMessageRead(ctx, viewer, box, message, true); err != nil {
			t.Fatalf("SetMessageRead: %v", err)
		}
	}
	// Marking read twice keeps one row.
	if _, err := f.service.SetMessageRead(ctx, f.b, box, message, true); err != nil {
		t.Fatalf("SetMessageRead again: %v", err)
	}
	if got := readRows(t, f, 0); got != 2 {
		t.Fatalf("read rows = %d; want 2", got)
	}
	unmarked, err := f.service.SetMessageRead(ctx, f.b, box, message, false)
	if err != nil || unmarked.Read {
		t.Fatalf("B mark unread = %+v, %v; want unread", unmarked, err)
	}
	if readRows(t, f, f.b.MembershipID) != 0 || readRows(t, f, f.c.MembershipID) != 1 {
		t.Fatal("marking unread touched another person's read row")
	}
	if !readOf(t, f, f.c, box, message) {
		t.Fatal("C's message turned unread when B marked it unread")
	}
}

// Outbound messages are read for everyone, and marking one changes nothing.
func TestOutboundAlwaysRead(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box := f.team.ID
	sent, _, err := f.service.SendMessage(ctx, f.b, box, sendInput())
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !sent.Read {
		t.Fatal("a sent message answers unread")
	}
	if !readOf(t, f, f.c, box, sent.ID) {
		t.Fatal("C sees B's sent message unread")
	}
	unmarked, err := f.service.SetMessageRead(ctx, f.c, box, sent.ID, false)
	if err != nil || !unmarked.Read {
		t.Fatalf("marking a sent message unread = %+v, %v; want still read", unmarked, err)
	}
	if got := readRows(t, f, 0); got != 0 {
		t.Fatalf("read rows = %d; outbound mail stores none", got)
	}
}

// Reading mail with an API key never changes read state.
func TestApiKeyReadDoesNotMarkRead(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	sent, _, err := f.service.SendFromAddress(ctx, f.b, f.team.Address, sendInput())
	if err != nil {
		t.Fatalf("SendFromAddress: %v", err)
	}
	read, err := f.service.GetOutboundMessage(ctx, f.b, sent.ID)
	if err != nil || !read.Read {
		t.Fatalf("GetOutboundMessage = %+v, %v; want the sent message, read", read, err)
	}
	if got := readRows(t, f, 0); got != 0 {
		t.Fatalf("read rows after an API key read = %d; want 0", got)
	}
	if got := unreadOf(t, f, f.b, f.team.ID); got != 1 {
		t.Fatalf("B unread after an API key read = %d; want 1", got)
	}
}

// Review focus 1: removing a member deletes their read rows for the mailbox in the same
// transaction, so access granted again starts with everything unread.
func TestRemovingMemberDeletesTheirReads(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box, message := f.team.ID, f.teamMessage.ID
	if _, err := f.service.SetMessageRead(ctx, f.c, box, message, true); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}
	if _, err := f.service.SetMessageRead(ctx, f.c, f.personal.ID, f.personalMessage.ID, true); err == nil {
		t.Fatal("C marked a message of B's personal mailbox")
	}
	if _, err := f.service.SetMessageRead(ctx, f.b, box, message, true); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}

	if err := f.service.RemoveMailboxMember(ctx, f.admin, box, accessC); err != nil {
		t.Fatalf("RemoveMailboxMember: %v", err)
	}
	if got := readRows(t, f, f.c.MembershipID); got != 0 {
		t.Fatalf("C's read rows after removal = %d; want 0", got)
	}
	if got := readRows(t, f, f.b.MembershipID); got != 1 {
		t.Fatalf("B's read rows after C's removal = %d; want 1", got)
	}
	_, err := f.service.GetMessage(ctx, f.c, box, message)
	expectError(t, "removed member GetMessage", err, ErrMailboxNotFound)

	if _, err := f.service.SetMailboxMember(ctx, f.admin, box, accessC, domain.MailboxPermissionView); err != nil {
		t.Fatalf("SetMailboxMember: %v", err)
	}
	if readOf(t, f, f.c, box, message) {
		t.Fatal("C's old read state came back with the new grant")
	}
}
