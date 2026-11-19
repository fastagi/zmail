package mailbox

import (
	"context"
	"encoding/base64"
	"errors"
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
}

var _ domain.MailInboxRepository = (*inboxRecorder)(nil)

func newInboxRecorder() *inboxRecorder {
	return &inboxRecorder{fakeRepository: newFakeRepository()}
}

func (r *inboxRecorder) ListMailboxAccessRows(_ context.Context, organizationID, membershipID uint) ([]domain.MailboxAccessRow, error) {
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

func (r *inboxRecorder) CountUnreadByMailboxIDs(context.Context, uint, uint, []uint) (map[uint]int, error) {
	return map[uint]int{}, nil
}

// ListInbox serves the all and sent views of the stored messages, newest first.
func (r *inboxRecorder) ListInbox(_ context.Context, query domain.InboxQuery) ([]*domain.MailMessage, error) {
	r.queries = append(r.queries, query)
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

func (r *inboxRecorder) CountInboxViews(context.Context, uint, []uint, time.Time, time.Time) (domain.InboxViewCounts, error) {
	return domain.InboxViewCounts{}, nil
}

func newInboxRecorderService(repo *inboxRecorder) *service {
	return NewService(repo, newAccessMemberships(), repo, newFakeContacts(), &stubBroadcasts{}, nil, nil, nil, nil,
		&fakeTransport{}, &fakeNotifier{}, accessSettings())
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
	if err := repo.CreateMailbox(context.Background(), foreign, domain.MaxMailboxesPerOrganization); err != nil {
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
