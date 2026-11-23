package mailbox

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

// inboxRecorder is the in-memory repository with the unified inbox seam. It records the inbox
// queries it serves, so a test can prove a query never ran.
type inboxRecorder struct {
	*fakeRepository
	queries []domain.InboxQuery
	// listErr, when set, is what ListInbox answers instead of mail.
	listErr error
	// unread is what CountUnreadByMailboxIDs answers, keyed by mailbox id.
	unread map[uint]int
	// views is what CountInboxViews answers.
	views domain.InboxViewCounts
	// unreadCalls and viewCalls record the mailbox ids each count query was asked for.
	unreadCalls [][]uint
	viewCalls   []inboxViewCall
}

// inboxViewCall is one CountInboxViews call.
type inboxViewCall struct {
	membershipID     uint
	mailboxIDs       []uint
	dayStart, dayEnd time.Time
}

var _ domain.MailInboxRepository = (*inboxRecorder)(nil)

func newInboxRecorder() *inboxRecorder {
	return &inboxRecorder{fakeRepository: newFakeRepository()}
}

func (r *inboxRecorder) ListMailboxAccessRows(_ context.Context, organizationID, membershipID, _ uint) ([]domain.MailboxAccessRow, error) {
	rows := []domain.MailboxAccessRow{}
	for _, mailbox := range r.mailboxes {
		if mailbox.OrganizationID != organizationID {
			continue
		}
		copied := *mailbox
		row := domain.MailboxAccessRow{Mailbox: &copied}
		if member, ok := r.members[[2]uint{mailbox.ID, membershipID}]; ok {
			memberCopy := *member
			row.Member = &memberCopy
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b domain.MailboxAccessRow) int { return int(a.Mailbox.ID) - int(b.Mailbox.ID) })
	return rows, nil
}

func (r *inboxRecorder) CountUnreadByMailboxIDs(_ context.Context, _, _ uint, mailboxIDs []uint) (map[uint]int, error) {
	r.unreadCalls = append(r.unreadCalls, slices.Clone(mailboxIDs))
	counts := map[uint]int{}
	for _, id := range mailboxIDs {
		if unread, ok := r.unread[id]; ok {
			counts[id] = unread
		}
	}
	return counts, nil
}

// ListInbox serves the all and sent views of the stored messages, newest first.
func (r *inboxRecorder) ListInbox(_ context.Context, query domain.InboxQuery) ([]*domain.MailMessage, error) {
	r.queries = append(r.queries, query)
	if r.listErr != nil {
		return nil, r.listErr
	}
	direction := domain.MailDirectionInbound
	if query.View == domain.InboxViewSent {
		direction = domain.MailDirectionOutbound
	}
	listed := []*domain.MailMessage{}
	for _, message := range r.messages {
		if !slices.Contains(query.MailboxIDs, message.MailboxID) || message.Direction != direction ||
			placementOrDefault(message.Placement) != domain.MailPlacementDefault {
			continue
		}
		if query.After != nil && !inboxBefore(message, *query.After) {
			continue
		}
		copied := *message
		copied.MailboxAddress = r.mailboxes[message.MailboxID].Address
		if query.GroupThreads {
			copied.Thread = &domain.MailThreadInfo{MessageCount: 1, MessageIDs: []uint{message.ID}, LabelIDs: []uint{}}
		}
		listed = append(listed, &copied)
	}
	slices.SortFunc(listed, func(a, b *domain.MailMessage) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return b.CreatedAt.Compare(a.CreatedAt)
		}
		return int(b.ID) - int(a.ID)
	})
	if len(listed) > query.Limit {
		listed = listed[:query.Limit]
	}
	return listed, nil
}

// inboxBefore reports whether message is strictly older than the cursor.
func inboxBefore(message *domain.MailMessage, cursor domain.InboxCursor) bool {
	if !message.CreatedAt.Equal(cursor.CreatedAt) {
		return message.CreatedAt.Before(cursor.CreatedAt)
	}
	return message.ID < cursor.ID
}

func (r *inboxRecorder) MessageSnippets(_ context.Context, ids []uint) (map[uint]string, error) {
	snippets := map[uint]string{}
	for _, id := range ids {
		if message, ok := r.messages[id]; ok {
			body := []rune(message.TextBody)
			snippets[id] = string(body[:min(len(body), inboxSnippetSource)])
		}
	}
	return snippets, nil
}

func (r *inboxRecorder) CountInboxViews(_ context.Context, membershipID uint, mailboxIDs []uint, dayStart, dayEnd time.Time) (domain.InboxViewCounts, error) {
	r.viewCalls = append(r.viewCalls, inboxViewCall{
		membershipID: membershipID, mailboxIDs: slices.Clone(mailboxIDs), dayStart: dayStart, dayEnd: dayEnd,
	})
	return r.views, nil
}

func newInboxRecorderService(repo *inboxRecorder) *service {
	return NewService(repo, newAccessMemberships(), repo, newFakeContacts(), &stubBroadcasts{}, nil, nil, nil, nil,
		&fakeTransport{}, &fakeNotifier{}, accessSettings(), nil, nil, nil, nil)
}

func listInbox(t *testing.T, svc *service, viewer domain.OrganizationContext, request InboxRequest) InboxPage {
	t.Helper()
	if request.View == "" {
		request.View = domain.InboxViewAll
	}
	if request.PerPage == 0 {
		request.PerPage = 15
	}
	page, err := svc.ListInbox(context.Background(), viewer, request)
	if err != nil {
		t.Fatalf("ListInbox(%+v): %v", request, err)
	}
	return page
}

func inboxIDs(page InboxPage) []uint {
	ids := make([]uint, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.Message.ID)
	}
	return ids
}

func TestInboxListsOnlyReadableMailboxes(t *testing.T) {
	f := newPostgresAccessFixture(t)

	b := listInbox(t, f.service, f.b, InboxRequest{})
	// Newest first: team@'s message arrived after b@'s.
	expectIDs(t, "B inbox", inboxIDs(b), f.teamMessage.ID, f.personalMessage.ID)
	if b.Next != nil || b.ReadableMailboxes != 2 {
		t.Fatalf("B page next = %v, readable = %d; want no next page over 2 mailboxes", b.Next, b.ReadableMailboxes)
	}
	for _, item := range b.Items {
		want := f.personal.Address
		if item.Message.MailboxID == f.team.ID {
			want = f.team.Address
		}
		if item.Message.MailboxAddress != want || item.RoleID != nil || item.Snippet != "body" {
			t.Fatalf("B item = address %q, role %v, snippet %q; want %q, no role, %q",
				item.Message.MailboxAddress, item.RoleID, item.Snippet, want, "body")
		}
		if item.Message.TextBody != "" || item.Message.HTMLBody != "" {
			t.Fatalf("B item %d carries a body", item.Message.ID)
		}
	}

	c := listInbox(t, f.service, f.c, InboxRequest{})
	expectIDs(t, "C inbox", inboxIDs(c), f.teamMessage.ID)

	admin := listInbox(t, f.service, f.admin, InboxRequest{})
	if len(admin.Items) != 0 || admin.Next != nil || admin.ReadableMailboxes != 0 {
		t.Fatalf("admin page = %+v; an administrator reads no mail", admin)
	}

	// Pages follow (created_at, id) without gaps or repeats.
	first := listInbox(t, f.service, f.b, InboxRequest{PerPage: 1})
	if len(first.Items) != 1 || first.Next == nil {
		t.Fatalf("first page = %d items, next %v; want 1 and a next cursor", len(first.Items), first.Next)
	}
	second := listInbox(t, f.service, f.b, InboxRequest{PerPage: 1, After: first.Next})
	if len(second.Items) != 1 || second.Next != nil {
		t.Fatalf("second page = %d items, next %v; want 1 and the last page", len(second.Items), second.Next)
	}
	expectIDs(t, "paged B inbox", append(inboxIDs(first), inboxIDs(second)...), f.teamMessage.ID, f.personalMessage.ID)

	// Another organization's mailbox id filters to nothing, never an error.
	repo := f.inboxRepo()
	createOwner(t, repo.db, 99)
	foreign := &domain.Mailbox{OrganizationID: 99, Address: "foreign@mail.example", Type: domain.MailboxTypeShared}
	if err := repo.CreateMailbox(context.Background(), foreign, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("create foreign mailbox: %v", err)
	}
	f.receive(t, foreign, "Foreign news")
	if page := listInbox(t, f.service, f.b, InboxRequest{MailboxID: foreign.ID}); len(page.Items) != 0 {
		t.Fatalf("B filtered on a foreign mailbox = %v; want empty", inboxIDs(page))
	}
	if page := listInbox(t, f.service, f.b, InboxRequest{MailboxID: 999_999}); len(page.Items) != 0 {
		t.Fatalf("B filtered on a missing mailbox = %v; want empty", inboxIDs(page))
	}
	if page := listInbox(t, f.service, f.b, InboxRequest{MailboxID: f.team.ID}); !slices.Equal(inboxIDs(page), []uint{f.teamMessage.ID}) {
		t.Fatalf("B filtered on team@ = %v; want only team@'s message", inboxIDs(page))
	}
}

func TestInboxEmptyReadableSetSkipsQuery(t *testing.T) {
	repo := newInboxRecorder()
	svc := newInboxRecorderService(repo)
	ctx := context.Background()
	admin := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleAdmin)
	b := viewerOf(accessOrg, accessB, domain.OrganizationRoleMember)
	personal, err := svc.CreateMailbox(ctx, admin, CreateMailboxInput{
		LocalPart: "b", Type: domain.MailboxTypePersonal, OwnerUserID: accessB,
	})
	if err != nil {
		t.Fatalf("create personal mailbox: %v", err)
	}
	if _, err := repo.CreateInboundMessage(ctx, &domain.MailMessage{
		MailboxID: personal.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		Subject: "Hello", DedupeKey: "hello", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}

	if page := listInbox(t, svc, admin, InboxRequest{}); len(page.Items) != 0 || page.Next != nil {
		t.Fatalf("admin page = %+v; want empty", page)
	}
	if page := listInbox(t, svc, b, InboxRequest{RoleID: 3}); len(page.Items) != 0 {
		t.Fatalf("B page for an unknown role = %+v; want empty", page)
	}
	if len(repo.queries) != 0 {
		t.Fatalf("inbox queries = %+v; an empty readable set must not read mail", repo.queries)
	}

	page := listInbox(t, svc, b, InboxRequest{Ungrouped: true, PerPage: 5})
	if len(page.Items) != 1 || len(repo.queries) != 1 {
		t.Fatalf("B ungrouped page = %d items after %d queries; want 1 and 1", len(page.Items), len(repo.queries))
	}
	query := repo.queries[0]
	if !slices.Equal(query.MailboxIDs, []uint{personal.ID}) || query.MembershipID != b.MembershipID || query.Limit != 6 {
		t.Fatalf("query = %+v; want b@ for B with limit per_page+1", query)
	}
}

func TestInboxFilterOnUnreadableMailboxIsEmpty(t *testing.T) {
	f := newPostgresAccessFixture(t)
	page, err := f.service.ListInbox(context.Background(), f.c, InboxRequest{
		View: domain.InboxViewAll, MailboxID: f.personal.ID, PerPage: 15,
	})
	if err != nil {
		t.Fatalf("C filtered on b@: %v; want an empty page, not an error", err)
	}
	if len(page.Items) != 0 || page.Next != nil {
		t.Fatalf("C filtered on b@ = %v; want empty", inboxIDs(page))
	}
}

func TestInboxSnippetCollapsesAndCuts(t *testing.T) {
	if got := inboxSnippet("a \n\t b"); got != "a b" {
		t.Fatalf("snippet = %q; want %q", got, "a b")
	}
	if got := inboxSnippet("  　lead and trail \r\n"); got != "lead and trail" {
		t.Fatalf("snippet = %q; want trimmed", got)
	}
	if got := inboxSnippet("   "); got != "" {
		t.Fatalf("blank snippet = %q; want empty", got)
	}
	mixed := strings.Repeat("邮a", 100)
	got := inboxSnippet(mixed)
	if utf8.RuneCountInString(got) != 140 || !utf8.ValidString(got) || !strings.HasPrefix(mixed, got) {
		t.Fatalf("snippet of 200 mixed runes = %d runes %q; want the first 140", utf8.RuneCountInString(got), got)
	}
}

func TestInboxCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 11, 19, 6, 22, 0, 123456000, time.UTC)
	token := encodeInboxCursor(domain.InboxCursor{CreatedAt: at, ID: 9021})
	cursor, err := decodeInboxCursor(token)
	if err != nil {
		t.Fatalf("decode %q: %v", token, err)
	}
	if !cursor.CreatedAt.Equal(at) || cursor.ID != 9021 {
		t.Fatalf("cursor = %+v; want %s and 9021", cursor, at)
	}
	if len(token) > maxInboxCursorLength {
		t.Fatalf("token %q is longer than %d", token, maxInboxCursorLength)
	}

	encode := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	for _, bad := range []string{
		"", "%%%", encode("x.y"), encode("1"), encode("1.0"), encode("1.-2"), encode("1.2.3"), encode(".5"),
		encode("1.18446744073709551616"), encode("+1.2"), encode("01.2"), encode("1.02"),
		base64.URLEncoding.EncodeToString([]byte("1.23")), strings.Repeat("A", maxInboxCursorLength+1),
	} {
		if _, err := decodeInboxCursor(bad); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("decode %q = %v; want invalid input", bad, err)
		}
	}
}

func TestInboxDayBounds(t *testing.T) {
	load := func(name string) *time.Location {
		t.Helper()
		location, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		return location
	}
	utc := func(value string) time.Time {
		t.Helper()
		at, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatalf("parse %s: %v", value, err)
		}
		return at
	}
	cases := []struct {
		name       string
		now        time.Time
		location   *time.Location
		start, end time.Time
	}{
		{"UTC", utc("2026-11-18T23:30:00Z"), time.UTC, utc("2026-11-18T00:00:00Z"), utc("2026-11-19T00:00:00Z")},
		{"nil is UTC", utc("2026-11-18T23:30:00Z"), nil, utc("2026-11-18T00:00:00Z"), utc("2026-11-19T00:00:00Z")},
		{"Asia/Shanghai", utc("2026-11-18T23:30:00Z"), load("Asia/Shanghai"), utc("2026-11-18T16:00:00Z"), utc("2026-11-19T16:00:00Z")},
		// Daylight saving starts: the day has 23 hours.
		{"New York spring", utc("2026-03-08T15:00:00Z"), load("America/New_York"), utc("2026-03-08T05:00:00Z"), utc("2026-03-09T04:00:00Z")},
		// Daylight saving ends: the day has 25 hours.
		{"New York autumn", utc("2026-11-01T15:00:00Z"), load("America/New_York"), utc("2026-11-01T04:00:00Z"), utc("2026-11-02T05:00:00Z")},
	}
	for _, tc := range cases {
		start, end := inboxDay(tc.now, tc.location)
		if !start.Equal(tc.start) || !end.Equal(tc.end) || start.Location() != time.UTC || end.Location() != time.UTC {
			t.Fatalf("%s: day = [%s, %s); want [%s, %s) in UTC", tc.name, start, end, tc.start, tc.end)
		}
	}
	spring, _ := time.Parse(time.RFC3339, "2026-03-08T15:00:00Z")
	if start, end := inboxDay(spring, load("America/New_York")); end.Sub(start) != 23*time.Hour {
		t.Fatalf("New York day of 2026-03-08 = %s; want 23h", end.Sub(start))
	}
	autumn, _ := time.Parse(time.RFC3339, "2026-11-01T15:00:00Z")
	if start, end := inboxDay(autumn, load("America/New_York")); end.Sub(start) != 25*time.Hour {
		t.Fatalf("New York day of 2026-11-01 = %s; want 25h", end.Sub(start))
	}
}

func TestInboxTodayUsesViewerTimezone(t *testing.T) {
	f := newInboxFixture(t)
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	// 01:00 UTC on 19 November is 09:00 in Shanghai: both zones are on 19 November, and
	// Shanghai's day began at 16:00 UTC on the 18th.
	now := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)
	f.service.now = func() time.Time { return now }
	evening := f.deliverAt(t, f.team, delivery{subject: "evening"}, time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC))

	today := func(location *time.Location) InboxPage {
		return listInbox(t, f.service, f.c, InboxRequest{View: domain.InboxViewToday, Location: location})
	}
	expectIDs(t, "C's today in Shanghai", inboxIDs(today(shanghai)), evening.ID)
	expectIDs(t, "C's today in UTC", inboxIDs(today(time.UTC)))

	counts, err := f.service.InboxCounts(context.Background(), f.c, shanghai)
	if err != nil {
		t.Fatalf("InboxCounts(Shanghai): %v", err)
	}
	wantStart, wantEnd := time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC), time.Date(2026, 11, 19, 16, 0, 0, 0, time.UTC)
	if counts.Today != 1 || counts.TodayUnread != 1 || !counts.TodayStartsAt.Equal(wantStart) || !counts.TodayEndsAt.Equal(wantEnd) {
		t.Fatalf("Shanghai counts = %+v; want today 1, unread 1 over [%s, %s)", counts, wantStart, wantEnd)
	}
	// The unread counts agree with the mailbox list's unread_count of the same mailbox.
	team := listedMailbox(t, f.service, f.c, f.team.ID)
	if team == nil || len(counts.Mailboxes) != 1 || counts.Mailboxes[0] != (InboxMailboxCount{MailboxID: f.team.ID, Unread: team.UnreadCount}) ||
		counts.AllUnread != team.UnreadCount || counts.UngroupedUnread != team.UnreadCount || len(counts.Roles) != 0 {
		t.Fatalf("C's counts = %+v; want team@ alone with the listed unread count %+v", counts, team)
	}

	counts, err = f.service.InboxCounts(context.Background(), f.c, time.UTC)
	if err != nil {
		t.Fatalf("InboxCounts(UTC): %v", err)
	}
	if counts.Today != 0 || counts.TodayUnread != 0 || !counts.TodayStartsAt.Equal(time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("UTC counts = %+v; the evening message belongs to UTC's yesterday", counts)
	}
}

// newInboxCountsScenario is the access scenario on the inbox recorder.
func newInboxCountsScenario(t *testing.T) (*inboxRecorder, *service, *accessFixture) {
	t.Helper()
	repo := newInboxRecorder()
	svc := newInboxRecorderService(repo)
	return repo, svc, seedAccessFixture(t, svc, repo, &fakeTransport{}, newAccessMemberships())
}

func TestInboxCountsGroupByRole(t *testing.T) {
	repo, svc, f := newInboxCountsScenario(t)
	ctx := context.Background()
	shared := func(localPart string, roleID uint, members ...MailboxMemberInput) *domain.Mailbox {
		t.Helper()
		mailbox, err := svc.CreateMailbox(ctx, f.admin, CreateMailboxInput{
			LocalPart: localPart, Type: domain.MailboxTypeShared, Members: members,
		})
		if err != nil {
			t.Fatalf("create %s: %v", localPart, err)
		}
		if roleID != 0 {
			repo.mailboxes[mailbox.ID].RoleID = new(roleID)
		}
		return mailbox
	}
	cView := MailboxMemberInput{UserID: accessC, Permission: domain.MailboxPermissionView}
	sales := shared("sales", 5, cView)
	helpdesk := shared("helpdesk", 3, cView)
	billing := shared("billing", 5, cView)
	// Only B reads it, so for C role 9 never appears.
	hidden := shared("hidden", 9, MailboxMemberInput{UserID: accessB, Permission: domain.MailboxPermissionView})
	repo.unread = map[uint]int{f.team.ID: 3, sales.ID: 2, helpdesk.ID: 4, hidden.ID: 7}
	repo.views = domain.InboxViewCounts{Today: 4, TodayUnread: 2, Pending: 1, SentWaiting: 1}

	counts, err := svc.InboxCounts(ctx, f.c, time.UTC)
	if err != nil {
		t.Fatalf("InboxCounts: %v", err)
	}
	wantRoles := []InboxRoleCount{{RoleID: 3, Unread: 4}, {RoleID: 5, Unread: 2}}
	wantMailboxes := []InboxMailboxCount{
		{MailboxID: f.team.ID, Unread: 3}, {MailboxID: sales.ID, Unread: 2},
		{MailboxID: helpdesk.ID, Unread: 4}, {MailboxID: billing.ID, Unread: 0},
	}
	if !slices.Equal(counts.Roles, wantRoles) || !slices.Equal(counts.Mailboxes, wantMailboxes) ||
		counts.UngroupedUnread != 3 || counts.AllUnread != 9 {
		t.Fatalf("counts = %+v; want roles %v, ungrouped 3, all 9, mailboxes %v", counts, wantRoles, wantMailboxes)
	}
	sum := counts.UngroupedUnread
	for _, role := range counts.Roles {
		sum += role.Unread
	}
	if sum != counts.AllUnread {
		t.Fatalf("Σ roles + ungrouped = %d; want all_unread %d", sum, counts.AllUnread)
	}
	if counts.Today != 4 || counts.TodayUnread != 2 || counts.Pending != 1 || counts.SentWaiting != 1 {
		t.Fatalf("view counts = %+v; want the repository's %+v", counts, repo.views)
	}
}

func TestInboxCountsOnlyQueryReadableMailboxes(t *testing.T) {
	repo, svc, f := newInboxCountsScenario(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 23, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}

	counts, err := svc.InboxCounts(ctx, f.c, shanghai)
	if err != nil {
		t.Fatalf("C InboxCounts: %v", err)
	}
	if len(repo.unreadCalls) != 1 || !slices.Equal(repo.unreadCalls[0], []uint{f.team.ID}) {
		t.Fatalf("unread count calls = %v; want only C's readable team@", repo.unreadCalls)
	}
	wantStart, wantEnd := time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC), time.Date(2026, 11, 19, 16, 0, 0, 0, time.UTC)
	if len(repo.viewCalls) != 1 {
		t.Fatalf("view count calls = %+v; want one", repo.viewCalls)
	}
	call := repo.viewCalls[0]
	if !slices.Equal(call.mailboxIDs, []uint{f.team.ID}) || call.membershipID != f.c.MembershipID ||
		!call.dayStart.Equal(wantStart) || !call.dayEnd.Equal(wantEnd) {
		t.Fatalf("view count call = %+v; want team@ for C over [%s, %s)", call, wantStart, wantEnd)
	}
	if !counts.TodayStartsAt.Equal(wantStart) || !counts.TodayEndsAt.Equal(wantEnd) {
		t.Fatalf("C's day = [%s, %s); want [%s, %s)", counts.TodayStartsAt, counts.TodayEndsAt, wantStart, wantEnd)
	}

	admin, err := svc.InboxCounts(ctx, f.admin, shanghai)
	if err != nil {
		t.Fatalf("admin InboxCounts: %v", err)
	}
	if len(repo.unreadCalls) != 1 || len(repo.viewCalls) != 1 {
		t.Fatalf("after the admin: %d unread and %d view count calls; an empty readable set must not count mail",
			len(repo.unreadCalls), len(repo.viewCalls))
	}
	if admin.Roles == nil || admin.Mailboxes == nil || len(admin.Roles) != 0 || len(admin.Mailboxes) != 0 ||
		admin.AllUnread != 0 || admin.Today != 0 || admin.TodayUnread != 0 || admin.Pending != 0 ||
		admin.SentWaiting != 0 || admin.UngroupedUnread != 0 {
		t.Fatalf("admin counts = %+v; want zeros and empty arrays", admin)
	}
	if !admin.TodayStartsAt.Equal(wantStart) || !admin.TodayEndsAt.Equal(wantEnd) {
		t.Fatalf("admin day = [%s, %s); want the requested day still", admin.TodayStartsAt, admin.TodayEndsAt)
	}
}

// withLooseMailbox adds loose@, a shared mailbox B views that has no mail role, with one message.
func (f *roleFixture) withLooseMailbox(t *testing.T) (*domain.Mailbox, *domain.MailMessage) {
	t.Helper()
	loose, err := f.service.CreateMailbox(context.Background(), f.admin, CreateMailboxInput{
		LocalPart: "loose", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessB, Permission: domain.MailboxPermissionView}},
	})
	if err != nil {
		t.Fatalf("create loose@: %v", err)
	}
	return loose, f.receive(t, loose, "Loose note")
}

func TestInboxRoleFilter(t *testing.T) {
	f := newRoleFixture(t)
	_, looseMessage := f.withLooseMailbox(t)

	official := listInbox(t, f.service, f.b, InboxRequest{RoleID: f.official.ID})
	if ids := inboxIDs(official); !slices.Equal(ids, []uint{f.teamMessage.ID}) {
		t.Fatalf("B with role Official = %v; want only team@'s message %d", ids, f.teamMessage.ID)
	}
	if item := official.Items[0]; item.RoleID == nil || *item.RoleID != f.official.ID {
		t.Fatalf("team@ item role = %v; want Official", item.RoleID)
	}
	if ids := inboxIDs(listInbox(t, f.service, f.b, InboxRequest{Ungrouped: true})); !slices.Equal(ids, []uint{looseMessage.ID}) {
		t.Fatalf("B with role_id=none = %v; want only loose@'s message", ids)
	}

	status, body := f.call(t, f.c, http.MethodGet, fmt.Sprintf("/inbox-messages?role_id=%d", f.design.ID), "")
	if items, ok := body["data"].([]any); status != http.StatusOK || !ok || len(items) != 0 {
		t.Fatalf("C with Design desk = %d %v; want an empty 200", status, body)
	}
	status, body = f.call(t, f.b, http.MethodGet, fmt.Sprintf("/inbox-messages?role_id=%d", f.design.ID), "")
	items, _ := body["data"].([]any)
	if status != http.StatusOK || len(items) != 1 {
		t.Fatalf("B with Design desk = %d %v; want b@'s message", status, body)
	}
	if item, _ := items[0].(map[string]any); item["role_id"] != float64(f.design.ID) {
		t.Fatalf("b@ item = %v; want role_id %d", item, f.design.ID)
	}
}

func TestInboxCountsMoveWhenRoleDeleted(t *testing.T) {
	f := newRoleFixture(t)
	ctx := context.Background()
	before, err := f.service.InboxCounts(ctx, f.b, nil)
	if err != nil {
		t.Fatalf("InboxCounts: %v", err)
	}
	wantRoles := []InboxRoleCount{{RoleID: f.design.ID, Unread: 1}, {RoleID: f.official.ID, Unread: 1}}
	if before.AllUnread != 2 || before.UngroupedUnread != 0 || !slices.Equal(before.Roles, wantRoles) {
		t.Fatalf("counts before = %+v", before)
	}

	if err = f.service.DeleteMailRole(ctx, f.admin, f.official.ID); err != nil {
		t.Fatalf("delete Official: %v", err)
	}
	after, err := f.service.InboxCounts(ctx, f.b, nil)
	if err != nil {
		t.Fatalf("InboxCounts: %v", err)
	}
	// UI-8: the totals hold and team@'s unread moves to ungrouped.
	if after.AllUnread != before.AllUnread || after.UngroupedUnread != 1 ||
		!slices.Equal(after.Roles, wantRoles[:1]) || !slices.Equal(after.Mailboxes, before.Mailboxes) {
		t.Fatalf("counts after deleting Official = %+v; before %+v", after, before)
	}
}
