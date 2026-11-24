package mailbox

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// ledgerFixture is the Postgres access scenario (A admin; B owns b@ and sends from team@; C views
// team@; D holds nothing) with the ledger tables and three accounts: "Medium blog" registered with
// b@, "Behance studio" registered with team@ and owned by D, and "Dribbble shots" without a
// mailbox, owned by C.
type ledgerFixture struct {
	*accessFixture
	blog, studio, shots *domain.PlatformAccount
	audited             []auditstarter.Change
}

func newLedgerFixture(t *testing.T) *ledgerFixture {
	t.Helper()
	f := &ledgerFixture{accessFixture: newPostgresAccessFixture(t)}
	f.service.recordAudit = func(_ context.Context, change auditstarter.Change) {
		f.audited = append(f.audited, change)
	}
	f.blog = f.create(t, PlatformAccountInput{Platform: "Medium", Handle: "blog", MailboxID: &f.personal.ID})
	f.studio = f.create(t, PlatformAccountInput{Platform: "Behance", Handle: "studio", MailboxID: &f.team.ID, OwnerUserID: new(uint(accessD))})
	f.shots = f.create(t, PlatformAccountInput{Platform: "Dribbble", Handle: "shots", OwnerUserID: new(uint(accessC))})
	f.audited = nil
	return f
}

func (f *ledgerFixture) create(t *testing.T, input PlatformAccountInput) *domain.PlatformAccount {
	t.Helper()
	account, err := f.service.CreatePlatformAccount(context.Background(), f.admin, input)
	if err != nil {
		t.Fatalf("CreatePlatformAccount(%s %s): %v", input.Platform, input.Handle, err)
	}
	return account
}

// listed lists every account the viewer sees, by handle.
func (f *ledgerFixture) listed(t *testing.T, viewer domain.OrganizationContext, filter domain.PlatformAccountFilter) []string {
	t.Helper()
	filter.Page, filter.PerPage = 1, 100
	accounts, total, err := f.service.ListPlatformAccounts(context.Background(), viewer, filter)
	if err != nil {
		t.Fatalf("ListPlatformAccounts: %v", err)
	}
	if total != int64(len(accounts)) {
		t.Fatalf("total = %d; want %d", total, len(accounts))
	}
	handles := make([]string, 0, len(accounts))
	for _, account := range accounts {
		handles = append(handles, account.Handle)
	}
	return handles
}

func expectFields(t *testing.T, what string, err error, fields ...string) {
	t.Helper()
	validation, ok := asValidationError(err)
	if !ok {
		t.Fatalf("%s error = %v; want field errors %v", what, err, fields)
	}
	for _, field := range fields {
		if _, present := validation.Fields[field]; !present {
			t.Fatalf("%s errors = %v; want key %q", what, validation.Fields, field)
		}
	}
	if len(validation.Fields) != len(fields) {
		t.Fatalf("%s errors = %v; want exactly %v", what, validation.Fields, fields)
	}
}

func TestPlatformAccountWriteNeedsOwnerOrAdmin(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	input := PlatformAccountInput{Platform: "X", Handle: "acme"}

	_, err := f.service.CreatePlatformAccount(ctx, f.b, input)
	expectError(t, "member creates", err, domain.ErrPermissionDenied)
	_, err = f.service.UpdatePlatformAccount(ctx, f.d, f.studio.ID, input)
	expectError(t, "member owner updates", err, domain.ErrPermissionDenied)
	_, err = f.service.UpdatePlatformAccount(ctx, f.b, 9999, PlatformAccountInput{})
	expectError(t, "403 comes before 404 and 422", err, domain.ErrPermissionDenied)
	expectError(t, "member deletes", f.service.DeletePlatformAccount(ctx, f.b, f.blog.ID), domain.ErrPermissionDenied)
	_, err = f.service.HandOverPlatformAccounts(ctx, f.c, accessD, accessC)
	expectError(t, "member hands over", err, domain.ErrPermissionDenied)

	owner := viewerOf(accessOrg, 99, domain.OrganizationRoleOwner)
	if _, err := f.service.CreatePlatformAccount(ctx, owner, input); err != nil {
		t.Fatalf("owner creates: %v", err)
	}
}

func TestPlatformAccountValidation(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()

	created, err := f.service.CreatePlatformAccount(ctx, f.admin, PlatformAccountInput{
		Platform: "  Substack ", Handle: " acme ", ProfileURL: " https://acme.substack.com/ ", Notes: " weekly ",
	})
	if err != nil {
		t.Fatalf("create with spaces: %v", err)
	}
	if created.Platform != "Substack" || created.Handle != "acme" || created.ProfileURL != "https://acme.substack.com/" ||
		created.Notes != "weekly" || created.Status != domain.PlatformAccountStatusActive {
		t.Fatalf("created = %+v; want trimmed fields and the active default", created)
	}

	missingRole, otherOrgMailbox := uint(9999), uint(9998)
	for name, test := range map[string]struct {
		input  PlatformAccountInput
		fields []string
	}{
		"blank platform and handle": {PlatformAccountInput{Platform: " ", Handle: ""}, []string{"platform", "handle"}},
		"too long platform":         {PlatformAccountInput{Platform: strings.Repeat("p", 65), Handle: "a"}, []string{"platform"}},
		"too long handle":           {PlatformAccountInput{Platform: "p", Handle: strings.Repeat("h", 201)}, []string{"handle"}},
		"control characters":        {PlatformAccountInput{Platform: "p\x00", Handle: "a\x01b"}, []string{"platform", "handle"}},
		"not http":                  {PlatformAccountInput{Platform: "p", Handle: "a", ProfileURL: "javascript:alert(1)"}, []string{"profile_url"}},
		"no host":                   {PlatformAccountInput{Platform: "p", Handle: "a", ProfileURL: "https:///path"}, []string{"profile_url"}},
		"relative url":              {PlatformAccountInput{Platform: "p", Handle: "a", ProfileURL: "/acme"}, []string{"profile_url"}},
		"too long url":              {PlatformAccountInput{Platform: "p", Handle: "a", ProfileURL: "https://a.example/" + strings.Repeat("x", 2048)}, []string{"profile_url"}},
		"unknown status":            {PlatformAccountInput{Platform: "p", Handle: "a", Status: "lost"}, []string{"status"}},
		"too long notes":            {PlatformAccountInput{Platform: "p", Handle: "a", Notes: strings.Repeat("n", 2001)}, []string{"notes"}},
		"missing role":              {PlatformAccountInput{Platform: "p", Handle: "a", RoleID: &missingRole}, []string{"role_id"}},
		"missing mailbox":           {PlatformAccountInput{Platform: "p", Handle: "a", MailboxID: &otherOrgMailbox}, []string{"mailbox_id"}},
		"zero ids":                  {PlatformAccountInput{Platform: "p", Handle: "a", RoleID: new(uint(0)), MailboxID: new(uint(0)), OwnerUserID: new(uint(0))}, []string{"role_id", "mailbox_id", "owner_user_id"}},
		"owner not a member":        {PlatformAccountInput{Platform: "p", Handle: "a", OwnerUserID: new(uint(notMember))}, []string{"owner_user_id"}},
	} {
		_, createErr := f.service.CreatePlatformAccount(ctx, f.admin, test.input)
		expectFields(t, name, createErr, test.fields...)
	}

	_, err = f.service.CreatePlatformAccount(ctx, f.admin, PlatformAccountInput{Platform: "MEDIUM", Handle: "Blog"})
	expectError(t, "taken ignoring case", err, ErrPlatformAccountTaken)
	_, err = f.service.UpdatePlatformAccount(ctx, f.admin, 9999, PlatformAccountInput{Platform: "p", Handle: "a"})
	expectError(t, "update a missing account", err, ErrPlatformAccountNotFound)
	_, err = f.service.UpdatePlatformAccount(ctx, f.admin, 9999, PlatformAccountInput{})
	expectError(t, "404 comes before 422", err, ErrPlatformAccountNotFound)
}

func TestPlatformAccountMemberVisibility(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()

	if got := f.listed(t, f.admin, domain.PlatformAccountFilter{}); !slices.Equal(got, []string{"studio", "shots", "blog"}) {
		t.Fatalf("admin sees %v; want every account", got)
	}
	// B reads b@ and team@; C reads team@ and owns shots; D reads nothing but owns studio.
	for name, test := range map[string]struct {
		viewer domain.OrganizationContext
		want   []string
	}{
		"B reads both mailboxes":          {f.b, []string{"studio", "blog"}},
		"C reads team@ and owns shots":    {f.c, []string{"studio", "shots"}},
		"D owns studio only":              {f.d, []string{"studio"}},
		"a stranger in the org sees none": {viewerOf(accessOrg, 55, domain.OrganizationRoleMember), []string{}},
	} {
		if got := f.listed(t, test.viewer, domain.PlatformAccountFilter{}); !slices.Equal(got, test.want) {
			t.Errorf("%s: listing = %v; want %v", name, got, test.want)
		}
	}

	// Searching by a registration address never reaches past what the member sees.
	if got := f.listed(t, f.d, domain.PlatformAccountFilter{Query: f.personal.Address}); len(got) != 0 {
		t.Fatalf("D searching b@ sees %v; want nothing", got)
	}
	if got := f.listed(t, f.b, domain.PlatformAccountFilter{Query: f.personal.Address}); !slices.Equal(got, []string{"blog"}) {
		t.Fatalf("B searching b@ sees %v; want blog", got)
	}
	if got := f.listed(t, f.admin, domain.PlatformAccountFilter{Query: strings.ToUpper(f.team.Address)}); !slices.Equal(got, []string{"studio"}) {
		t.Fatalf("admin searching team@ sees %v; want studio", got)
	}

	// D owns studio, registered with team@ which D cannot see: the address stays hidden.
	accounts, _, err := f.service.ListPlatformAccounts(ctx, f.d, domain.PlatformAccountFilter{Page: 1, PerPage: 10})
	if err != nil || len(accounts) != 1 {
		t.Fatalf("D listing = %v, %v", accounts, err)
	}
	if accounts[0].MailboxAddress != "" || accounts[0].MailboxID == nil {
		t.Fatalf("D sees studio = %+v; want the mailbox id without its address", accounts[0])
	}
	if got := f.listed(t, f.d, domain.PlatformAccountFilter{Query: "team"}); len(got) != 0 {
		t.Fatalf("D probing team@ sees %v; want nothing", got)
	}

	// Events follow the same rule: a hidden account is not found.
	_, _, err = f.service.ListPlatformAccountEvents(ctx, f.d, f.blog.ID, 1, 10)
	expectError(t, "D reads blog's events", err, ErrPlatformAccountNotFound)
	if _, _, err = f.service.ListPlatformAccountEvents(ctx, f.d, f.studio.ID, 1, 10); err != nil {
		t.Fatalf("D reads studio's events: %v", err)
	}
	if _, _, err = f.service.ListPlatformAccountEvents(ctx, f.c, f.studio.ID, 1, 10); err != nil {
		t.Fatalf("C reads studio's events through team@: %v", err)
	}

	_, _, err = f.service.ListPlatformAccounts(ctx, f.b, domain.PlatformAccountFilter{Query: strings.Repeat("q", 101)})
	expectError(t, "search text too long", err, domain.ErrInvalidInput)
	_, _, err = f.service.ListPlatformAccounts(ctx, f.b, domain.PlatformAccountFilter{Status: "lost"})
	expectError(t, "unknown status filter", err, domain.ErrInvalidInput)
}

func TestPlatformAccountUpdateRecordsEvents(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()

	updated, err := f.service.UpdatePlatformAccount(ctx, f.admin, f.blog.ID, PlatformAccountInput{
		Platform: "Medium", Handle: "blog", MailboxID: &f.team.ID, OwnerUserID: new(uint(accessB)),
		Status: domain.PlatformAccountStatusNeedsReview,
	})
	if err != nil {
		t.Fatalf("UpdatePlatformAccount: %v", err)
	}
	if updated.MailboxAddress != f.team.Address || updated.Status != domain.PlatformAccountStatusNeedsReview {
		t.Fatalf("updated = %+v", updated)
	}
	events, total, err := f.service.ListPlatformAccountEvents(ctx, f.admin, f.blog.ID, 1, 10)
	if err != nil {
		t.Fatalf("ListPlatformAccountEvents: %v", err)
	}
	want := "mailbox_changed:" + uintText(f.personal.ID) + ">" + uintText(f.team.ID) + ",owner_changed:>2,status_changed:active>needs_review,created:>active"
	if got := eventKinds(events); got != want || total != 4 {
		t.Fatalf("events = %s (%d); want %s", got, total, want)
	}
	if events[0].ActorUserID == nil || *events[0].ActorUserID != accessAdmin {
		t.Fatalf("actor = %v; want the admin", events[0].ActorUserID)
	}
	if len(f.audited) != 1 || f.audited[0].Resource != "platform_accounts" || f.audited[0].TargetID != uintText(f.blog.ID) {
		t.Fatalf("audit = %+v; want one platform_accounts update", f.audited)
	}
}

func TestPlatformAccountHandover(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	second := f.create(t, PlatformAccountInput{Platform: "YouTube", Handle: "channel", OwnerUserID: new(uint(accessD))})

	_, err := f.service.HandOverPlatformAccounts(ctx, f.admin, accessD, notMember)
	expectFields(t, "successor not a member", err, "to_user_id")
	_, err = f.service.HandOverPlatformAccounts(ctx, f.admin, accessD, accessD)
	expectFields(t, "same user", err, "to_user_id")
	_, err = f.service.HandOverPlatformAccounts(ctx, f.admin, 0, accessB)
	expectFields(t, "missing from", err, "from_user_id")

	result, err := f.service.HandOverPlatformAccounts(ctx, f.admin, accessD, accessB)
	if err != nil {
		t.Fatalf("HandOverPlatformAccounts: %v", err)
	}
	if !slices.Equal(result.AccountIDs, []uint{f.studio.ID, second.ID}) || result.FromUserID != accessD || result.ToUserID != accessB {
		t.Fatalf("handover = %+v; want studio and channel moved from D to B", result)
	}
	if got := f.listed(t, f.d, domain.PlatformAccountFilter{}); len(got) != 0 {
		t.Fatalf("D after handover sees %v; want nothing", got)
	}
	if got := f.listed(t, f.admin, domain.PlatformAccountFilter{OwnerUserID: accessB}); !slices.Equal(got, []string{"studio", "channel"}) {
		t.Fatalf("B owns %v; want studio and channel", got)
	}
	events, _, err := f.service.ListPlatformAccountEvents(ctx, f.admin, second.ID, 1, 10)
	if err != nil || eventKinds(events) != "owner_changed:4>2,created:>active" {
		t.Fatalf("channel events = %s, %v", eventKinds(events), err)
	}

	// A former member's accounts can still be handed over: from need not be a member.
	f.memberships.absent[accessC] = true
	result, err = f.service.HandOverPlatformAccounts(ctx, f.admin, accessC, accessB)
	if err != nil || !slices.Equal(result.AccountIDs, []uint{f.shots.ID}) {
		t.Fatalf("handover from a former member = %+v, %v; want shots", result, err)
	}
}

func TestPlatformAccountDelete(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()

	if err := f.service.DeletePlatformAccount(ctx, f.admin, f.blog.ID); err != nil {
		t.Fatalf("DeletePlatformAccount: %v", err)
	}
	expectError(t, "delete again", f.service.DeletePlatformAccount(ctx, f.admin, f.blog.ID), ErrPlatformAccountNotFound)
	_, _, err := f.service.ListPlatformAccountEvents(ctx, f.admin, f.blog.ID, 1, 10)
	expectError(t, "events of a deleted account", err, ErrPlatformAccountNotFound)
	if len(f.audited) != 1 || f.audited[0].Action != "delete" {
		t.Fatalf("audit = %+v; want one delete", f.audited)
	}
}

func TestPlatformAccountServiceUnavailableWithoutSeam(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	_, _, err := f.service.ListPlatformAccounts(ctx, f.admin, domain.PlatformAccountFilter{})
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("list without the ledger = %v; want ErrServiceUnavailable", err)
	}
	_, err = f.service.HandOverPlatformAccounts(ctx, f.admin, 1, 2)
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("handover without the ledger = %v; want ErrServiceUnavailable", err)
	}
}
