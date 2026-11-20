package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestDirectoryCursorRoundTrip(t *testing.T) {
	id, err := decodeDirectoryCursor(encodeDirectoryCursor(42))
	if err != nil || id != 42 {
		t.Fatalf("round trip = %d, %v; want 42", id, err)
	}
	for _, token := range []string{"%%", strings.Repeat("A", 129), "MA", "LTE"} {
		if _, err := decodeDirectoryCursor(token); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("decode(%q) error = %v; want ErrInvalidInput", token, err)
		}
	}
}

// directoryFixture is an admin with a fresh PostgreSQL organization and a fixed clock.
type directoryFixture struct {
	service *service
	repo    *repository
	admin   domain.OrganizationContext
	now     time.Time
}

func newDirectoryFixture(t *testing.T) *directoryFixture {
	t.Helper()
	repo, db := openRepository(t)
	createOwner(t, db, accessOrg)
	svc := limitService(repo, newAccessMemberships(), 60)
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	return &directoryFixture{service: svc, repo: repo, admin: viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleAdmin), now: now}
}

func (f *directoryFixture) create(t *testing.T, localPart, displayName string) *domain.Mailbox {
	t.Helper()
	input := sharedFor(localPart)
	input.DisplayName = displayName
	mailbox, err := f.service.CreateMailbox(context.Background(), f.admin, input)
	if err != nil {
		t.Fatalf("create %s: %v", localPart, err)
	}
	return mailbox
}

func (f *directoryFixture) page(t *testing.T, filter domain.MailboxDirectoryFilter) *MailboxDirectoryPage {
	t.Helper()
	page, err := f.service.MailboxDirectory(context.Background(), f.admin, filter)
	if err != nil {
		t.Fatalf("MailboxDirectory: %v", err)
	}
	return page
}

func entryAddresses(page *MailboxDirectoryPage) []string {
	addresses := make([]string, 0, len(page.Entries))
	for _, entry := range page.Entries {
		addresses = append(addresses, entry.Mailbox.Address)
	}
	return addresses
}

// attempt stores one outbound attempt of the mailbox at the given time.
func attempt(t *testing.T, repo domain.MailboxRepository, mailbox *domain.Mailbox, at time.Time, status domain.MailStatus, lane domain.SendLane) {
	t.Helper()
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: status,
		FromAddress: mailbox.Address, To: []string{"bob@example.com"}, Subject: "Out",
		SendAttemptedAt: &at, SendLane: lane,
	}
	if _, err := repo.CreateMessage(context.Background(), message); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
}

func TestDirectoryCounts(t *testing.T) {
	f := newPostgresAccessFixture(t)
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	f.service.now = func() time.Time { return now }
	attempt(t, f.repo, f.team, now.Add(-time.Hour), domain.MailStatusSent, domain.SendLanePersonal)
	attempt(t, f.repo, f.team, now.Add(-2*time.Hour), domain.MailStatusFailed, domain.SendLanePersonal)
	attempt(t, f.repo, f.team, now.Add(-23*time.Hour), domain.MailStatusSent, domain.SendLanePersonal)
	attempt(t, f.repo, f.team, now.Add(-25*time.Hour), domain.MailStatusSent, domain.SendLanePersonal)
	// Broadcast recipients count against their own lane, not the personal limit shown here.
	attempt(t, f.repo, f.team, now.Add(-time.Hour), domain.MailStatusSent, domain.SendLaneBroadcast)
	gone, err := f.service.CreateMailbox(context.Background(), f.admin, sharedFor("gone"))
	if err != nil {
		t.Fatalf("create gone: %v", err)
	}
	attempt(t, f.repo, gone, now.Add(-time.Hour), domain.MailStatusSent, domain.SendLanePersonal)
	if err = f.service.DeleteMailbox(context.Background(), f.admin, gone.ID); err != nil {
		t.Fatalf("delete gone: %v", err)
	}

	page, err := f.service.MailboxDirectory(context.Background(), f.admin, domain.MailboxDirectoryFilter{})
	if err != nil {
		t.Fatalf("MailboxDirectory: %v", err)
	}
	if got := entryAddresses(page); !slices.Equal(got, []string{"team@mail.example", "b@mail.example"}) {
		t.Fatalf("entries = %v; a deleted mailbox has no row", got)
	}
	team, personal := page.Entries[0], page.Entries[1]
	if team.MemberCount != 2 || team.SentLast24h != 3 {
		t.Fatalf("team@ = %d members, %d sent; want 2 and 3", team.MemberCount, team.SentLast24h)
	}
	if personal.MemberCount != 0 || personal.SentLast24h != 0 {
		t.Fatalf("b@ = %d members, %d sent; want 0 and 0", personal.MemberCount, personal.SentLast24h)
	}
	total, err := f.repo.CountOutboundSince(context.Background(), accessOrg, domain.SendLanePersonal, now.Add(-24*time.Hour))
	if err != nil || page.SentLast24h != total || page.SentLast24h != 3 {
		t.Fatalf("organization total = %d; CountOutboundSince = %d, %v; want both 3", page.SentLast24h, total, err)
	}
	if page.MailboxCount != 2 || page.NextBeforeID != 0 {
		t.Fatalf("mailbox count = %d, next = %d; want 2 and no next page", page.MailboxCount, page.NextBeforeID)
	}
}

func TestDirectoryPagesNewestFirst(t *testing.T) {
	f := newDirectoryFixture(t)
	ids := make([]uint, 0, 5)
	for i := 1; i <= 5; i++ {
		ids = append(ids, f.create(t, fmt.Sprintf("box%d", i), "").ID)
	}
	idsOf := func(page *MailboxDirectoryPage) []uint {
		got := make([]uint, 0, len(page.Entries))
		for _, entry := range page.Entries {
			got = append(got, entry.Mailbox.ID)
		}
		return got
	}
	first := f.page(t, domain.MailboxDirectoryFilter{Limit: 2})
	if !slices.Equal(idsOf(first), []uint{ids[4], ids[3]}) || first.NextBeforeID != ids[3] {
		t.Fatalf("first page = %v next %d", idsOf(first), first.NextBeforeID)
	}
	second := f.page(t, domain.MailboxDirectoryFilter{Limit: 2, BeforeID: first.NextBeforeID})
	if !slices.Equal(idsOf(second), []uint{ids[2], ids[1]}) || second.NextBeforeID != ids[1] {
		t.Fatalf("second page = %v next %d", idsOf(second), second.NextBeforeID)
	}
	last := f.page(t, domain.MailboxDirectoryFilter{Limit: 2, BeforeID: second.NextBeforeID})
	if !slices.Equal(idsOf(last), []uint{ids[0]}) || last.NextBeforeID != 0 {
		t.Fatalf("last page = %v next %d", idsOf(last), last.NextBeforeID)
	}
}

func TestDirectorySearch(t *testing.T) {
	f := newDirectoryFixture(t)
	f.create(t, "sales", "")
	f.create(t, "trade", "Wholesale")
	f.create(t, "docs", "Docs")
	if _, err := f.service.CreateMailbox(context.Background(), f.admin, CreateMailboxInput{
		LocalPart: "salesb", Type: domain.MailboxTypePersonal, OwnerUserID: accessB,
	}); err != nil {
		t.Fatalf("create personal: %v", err)
	}
	got := entryAddresses(f.page(t, domain.MailboxDirectoryFilter{Query: "SAL"}))
	if !slices.Equal(got, []string{"salesb@mail.example", "trade@mail.example", "sales@mail.example"}) {
		t.Fatalf("q SAL = %v", got)
	}
	got = entryAddresses(f.page(t, domain.MailboxDirectoryFilter{Query: "  SAL ", Type: domain.MailboxTypeShared}))
	if !slices.Equal(got, []string{"trade@mail.example", "sales@mail.example"}) {
		t.Fatalf("shared q SAL = %v; the personal mailbox must be excluded", got)
	}
	got = entryAddresses(f.page(t, domain.MailboxDirectoryFilter{Type: domain.MailboxTypePersonal}))
	if !slices.Equal(got, []string{"salesb@mail.example"}) {
		t.Fatalf("personal = %v", got)
	}
}

// TestDirectorySearchEscapesWildcards is Review Focus 2.
func TestDirectorySearchEscapesWildcards(t *testing.T) {
	f := newDirectoryFixture(t)
	f.create(t, "deal", "50% off")
	f.create(t, "club", "500 club")
	f.create(t, "axb", "")
	f.create(t, "slash", `back\slash`)
	if got := entryAddresses(f.page(t, domain.MailboxDirectoryFilter{Query: "50%"})); !slices.Equal(got, []string{"deal@mail.example"}) {
		t.Fatalf("q 50%% = %v", got)
	}
	if got := entryAddresses(f.page(t, domain.MailboxDirectoryFilter{Query: "a_b"})); len(got) != 0 {
		t.Fatalf("q a_b = %v; _ must match itself", got)
	}
	if got := entryAddresses(f.page(t, domain.MailboxDirectoryFilter{Query: `k\s`})); !slices.Equal(got, []string{"slash@mail.example"}) {
		t.Fatalf(`q k\s = %v`, got)
	}
}

func TestDirectoryRejectsBadFilters(t *testing.T) {
	f := newAccessFixture(t)
	for name, filter := range map[string]domain.MailboxDirectoryFilter{
		"type":    {Type: "group"},
		"control": {Query: "a\x00b"},
		"long":    {Query: strings.Repeat("x", 121)},
	} {
		if _, err := f.service.MailboxDirectory(context.Background(), f.admin, filter); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("%s: error = %v; want ErrInvalidInput", name, err)
		}
	}
}

func TestDirectoryRequiresManager(t *testing.T) {
	f := newAccessFixture(t)
	if _, err := f.service.MailboxDirectory(context.Background(), f.b, domain.MailboxDirectoryFilter{}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member error = %v; want ErrPermissionDenied", err)
	}
	page, err := f.service.MailboxDirectory(context.Background(), f.admin, domain.MailboxDirectoryFilter{})
	if err != nil || len(page.Entries) != 2 {
		t.Fatalf("admin page = %+v, %v", page, err)
	}
}

func TestRenameMailbox(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	renamed, err := f.service.RenameMailbox(ctx, f.admin, f.team.ID, "  Support  ")
	if err != nil || renamed.DisplayName != "Support" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	if renamed.Access.View || renamed.Access.Send || !renamed.Access.Administer {
		t.Fatalf("access after rename = %+v; renaming grants no mail access", renamed.Access)
	}
	events := accessEvents(t, f, f.admin, f.team.ID)
	if events[0].Type != domain.MailboxAccessRenamed || events[0].Detail["from"] != "" || events[0].Detail["to"] != "Support" ||
		events[0].SubjectUserID != nil || events[0].ActorUserID == nil || *events[0].ActorUserID != accessAdmin {
		t.Fatalf("latest event = %+v", events[0])
	}
	if _, err = f.service.RenameMailbox(ctx, f.admin, f.team.ID, "Support"); err != nil {
		t.Fatalf("same name: %v", err)
	}
	if again := accessEvents(t, f, f.admin, f.team.ID); len(again) != len(events) {
		t.Fatalf("an unchanged name wrote an event: %d -> %d", len(events), len(again))
	}
	if _, err = f.service.RenameMailbox(ctx, f.admin, f.team.ID, "<x>"); !errors.Is(err, ErrMailboxAddressInvalid) {
		t.Fatalf("bad name error = %v", err)
	}
	if _, err = f.service.RenameMailbox(ctx, f.b, f.team.ID, "Mine"); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member error = %v", err)
	}
	if _, err = f.service.RenameMailbox(ctx, f.admin, 9999, "Nope"); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("missing mailbox error = %v", err)
	}
	cleared, err := f.service.RenameMailbox(ctx, f.admin, f.team.ID, "")
	if err != nil || cleared.DisplayName != "" {
		t.Fatalf("clear = %+v, %v", cleared, err)
	}
}

func TestListMailboxDirectoryHandler(t *testing.T) {
	f := newHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	if _, err := f.service.CreateMailbox(context.Background(), *membership(domain.OrganizationRoleAdmin), sharedWithHandlerUser("desk")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	recorder, body := f.do(http.MethodGet, "/mailbox-directory?type=shared&q=des", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list = %d %v", recorder.Code, body)
	}
	items := body["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	keys := make([]string, 0)
	for key := range items[0].(map[string]any) {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	want := []string{"address", "awaiting_handover", "created_at", "display_name", "id", "member_count", "owner_user_id", "sent_last_24h", "type"}
	if !slices.Equal(keys, want) {
		t.Fatalf("item keys = %v; want %v", keys, want)
	}
	meta := body["meta"].(map[string]any)
	for _, key := range []string{"per_page", "has_more", "next_cursor", "daily_send_limit", "sent_last_24h", "mailbox_count", "max_mailboxes"} {
		if _, present := meta[key]; !present {
			t.Fatalf("meta = %v; missing %s", meta, key)
		}
	}
	if meta["per_page"] != float64(15) || meta["daily_send_limit"] != float64(5) || meta["max_mailboxes"] != float64(50) || meta["mailbox_count"] != float64(1) {
		t.Fatalf("meta = %v", meta)
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}

	for path, want := range map[string]int{
		"/mailbox-directory?page=2":                                        http.StatusBadRequest,
		"/mailbox-directory?cursor=%25%25":                                 http.StatusBadRequest,
		"/mailbox-directory?q=" + strings.Repeat("x", 121):                 http.StatusUnprocessableEntity,
		"/mailbox-directory?type=group":                                    http.StatusUnprocessableEntity,
		"/mailbox-directory?per_page=101":                                  http.StatusUnprocessableEntity,
		"/mailbox-directory?per_page=1&cursor=" + encodeDirectoryCursor(1): http.StatusOK,
	} {
		recorder, body := f.do(http.MethodGet, path, "")
		if recorder.Code != want {
			t.Fatalf("GET %s = %d %v; want %d", path, recorder.Code, body, want)
		}
		switch want {
		case http.StatusBadRequest:
			if body["error_code"] != "COMMON.INVALID_INPUT" {
				t.Fatalf("GET %s error = %v", path, body)
			}
		case http.StatusUnprocessableEntity:
			if body["error_code"] != "COMMON.VALIDATION_FAILED" {
				t.Fatalf("GET %s error = %v", path, body)
			}
		}
	}

	member := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if recorder, body := member.do(http.MethodGet, "/mailbox-directory", ""); recorder.Code != http.StatusForbidden || body["error_code"] != "PERMISSION.DENIED" {
		t.Fatalf("member = %d %v; want 403", recorder.Code, body)
	}
}

func TestUpdateMailboxHandler(t *testing.T) {
	f := newHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	mailbox, err := f.service.CreateMailbox(context.Background(), *membership(domain.OrganizationRoleAdmin), sharedWithHandlerUser("desk"))
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	path := fmt.Sprintf("/mailboxes/%d", mailbox.ID)
	if recorder, body := f.do(http.MethodPut, path, `{}`); recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != "COMMON.VALIDATION_FAILED" {
		t.Fatalf("missing display_name = %d %v", recorder.Code, body)
	}
	if recorder, body := f.do(http.MethodPut, path, `{"display_name":"<x>"}`); recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailboxAddressInvalid {
		t.Fatalf("bad display_name = %d %v", recorder.Code, body)
	}
	recorder, body := f.do(http.MethodPut, path, `{"display_name":"Billing"}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["display_name"] != "Billing" {
		t.Fatalf("rename = %d %v", recorder.Code, body)
	}
	if recorder, body := f.do(http.MethodPut, "/mailboxes/9999", `{"display_name":"x"}`); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing mailbox = %d %v", recorder.Code, body)
	}

	member := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if recorder, body := member.do(http.MethodPut, path, `{"display_name":"x"}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("member = %d %v; want 403", recorder.Code, body)
	}
}
