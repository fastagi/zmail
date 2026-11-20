package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// importLine is a row whose one member is B (u2@people.test) unless members is given.
func importLine(address string, members ...string) MailboxImportRow {
	cell := "u2@people.test"
	if len(members) > 0 {
		cell = members[0]
	}
	return MailboxImportRow{Address: address, Members: cell}
}

func runImport(t *testing.T, svc *service, viewer domain.OrganizationContext, dryRun bool, rows ...MailboxImportRow) *MailboxImportResult {
	t.Helper()
	result, err := svc.ImportMailboxes(context.Background(), viewer, rows, dryRun)
	if err != nil {
		t.Fatalf("ImportMailboxes: %v", err)
	}
	return result
}

// outcomes renders each row as status or status:code, for compact assertions.
func outcomes(result *MailboxImportResult) []string {
	rendered := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		if row.ErrorCode != "" {
			rendered = append(rendered, string(row.Status)+":"+row.ErrorCode)
			continue
		}
		rendered = append(rendered, string(row.Status))
	}
	return rendered
}

func organizationMailboxCount(t *testing.T, f *accessFixture) int {
	t.Helper()
	mailboxes, err := f.service.ListMailboxes(context.Background(), f.admin, MailboxListScopeAll)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	return len(mailboxes)
}

func TestImportDryRunCreatesNothing(t *testing.T) {
	f := newAccessFixture(t)
	before := organizationMailboxCount(t, f)
	result := runImport(t, f.service, f.admin, true,
		importLine("sales@mail.example"), importLine("admin@mail.example"), importLine("team@mail.example"))
	want := []string{"valid", "failed:MAILBOX.ADDRESS_RESERVED", "failed:MAILBOX.ADDRESS_TAKEN"}
	if got := outcomes(result); !slices.Equal(got, want) {
		t.Fatalf("outcomes = %v; want %v", got, want)
	}
	if !result.DryRun || result.Succeeded != 1 || result.Failed != 2 || result.Rows[0].Row != 1 || result.Rows[2].Row != 3 {
		t.Fatalf("result = %+v", result)
	}
	if after := organizationMailboxCount(t, f); after != before {
		t.Fatalf("a dry run created %d mailboxes", after-before)
	}
}

// TestImportDuplicateRowIgnoresCase is Review Focus 3.
func TestImportDuplicateRowIgnoresCase(t *testing.T) {
	f := newAccessFixture(t)
	rows := []MailboxImportRow{importLine("Sales@mail.example"), importLine("sales@mail.example")}
	want := []string{"valid", "failed:MAILBOX.IMPORT_DUPLICATE_ROW"}
	if got := outcomes(runImport(t, f.service, f.admin, true, rows...)); !slices.Equal(got, want) {
		t.Fatalf("dry run = %v; want %v", got, want)
	}
	before := organizationMailboxCount(t, f)
	real := runImport(t, f.service, f.admin, false, rows...)
	if got := outcomes(real); !slices.Equal(got, []string{"created", "failed:MAILBOX.IMPORT_DUPLICATE_ROW"}) {
		t.Fatalf("real run = %v", got)
	}
	if after := organizationMailboxCount(t, f); after != before+1 || real.Rows[0].MailboxID == 0 {
		t.Fatalf("real run created %d mailboxes (id %d); want exactly one", after-before, real.Rows[0].MailboxID)
	}
}

func TestImportMemberNotInOrganization(t *testing.T) {
	f := newAccessFixture(t)
	result := runImport(t, f.service, f.admin, true,
		importLine("sales@mail.example", "u2@people.test:send;stranger@else.test;other@people.test"))
	row := result.Rows[0]
	if row.Status != MailboxImportFailed || row.ErrorCode != domain.CodeMailboxImportMemberNotFound ||
		!slices.Equal(row.MissingEmails, []string{"stranger@else.test", "other@people.test"}) {
		t.Fatalf("row = %+v", row)
	}
	if valid := runImport(t, f.service, f.admin, true, importLine("ok@mail.example")).Rows[0]; valid.MissingEmails == nil || len(valid.MissingEmails) != 0 {
		t.Fatalf("missing emails of a valid row = %#v; want an empty list", valid.MissingEmails)
	}
}

func TestImportMembersCellGrammar(t *testing.T) {
	f := newAccessFixture(t)
	hundredAndOne := make([]string, 0, 101)
	for i := 1000; i < 1101; i++ {
		hundredAndOne = append(hundredAndOne, fmt.Sprintf("u%d@people.test", i))
	}
	for cell, want := range map[string]string{
		"":                               "failed:MAILBOX.ACCESS_INVALID",
		" ; ; ":                          "failed:MAILBOX.ACCESS_INVALID",
		"u2@people.test:owner":           "failed:MAILBOX.ACCESS_INVALID",
		"u2@people.test;U2@people.test":  "failed:MAILBOX.ACCESS_INVALID",
		"u2@people.test:view:send":       "failed:MAILBOX.ACCESS_INVALID",
		"not-an-email":                   "failed:MAILBOX.ACCESS_INVALID",
		strings.Join(hundredAndOne, ";"): "failed:MAILBOX.ACCESS_INVALID",
		" u2@people.test ; u3@people.test:view ;": "valid",
	} {
		if got := outcomes(runImport(t, f.service, f.admin, true, importLine("sales@mail.example", cell))); !slices.Equal(got, []string{want}) {
			t.Fatalf("members %q = %v; want %s", cell, got, want)
		}
	}
	members, err := parseImportMembers(" u2@people.test ; U3@People.test:VIEW ;")
	if err != nil || len(members) != 2 || members[0] != (importMember{email: "u2@people.test", permission: domain.MailboxPermissionSend}) ||
		members[1] != (importMember{email: "u3@people.test", permission: domain.MailboxPermissionView}) {
		t.Fatalf("parseImportMembers = %+v, %v", members, err)
	}
}

func TestImportLimitCountsRowOrder(t *testing.T) {
	f := newAccessFixture(t)
	f.service.settings.MaxMailboxes = 4
	rows := []MailboxImportRow{importLine("one@mail.example"), importLine("two@mail.example"), importLine("three@mail.example")}
	if got := outcomes(runImport(t, f.service, f.admin, true, rows...)); !slices.Equal(got, []string{"valid", "valid", "failed:MAILBOX.LIMIT_REACHED"}) {
		t.Fatalf("dry run = %v", got)
	}
	if got := outcomes(runImport(t, f.service, f.admin, false, rows...)); !slices.Equal(got, []string{"created", "created", "failed:MAILBOX.LIMIT_REACHED"}) {
		t.Fatalf("real run = %v", got)
	}
}

func TestImportRowsCommitIndependently(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.connect(t, "news.example.com")
	fixture.zones.ruleErrFor = map[string]error{
		"two@news.example.com": &mailtransport.DomainError{Operation: "create routing rule", Message: "rule limit"},
	}
	owner := as(7)
	result := runImport(t, fixture.service, owner, false,
		importLine("one@news.example.com", "u2@people.test;u3@people.test:view"),
		importLine("two@news.example.com"),
		importLine("three@news.example.com"))
	if got := outcomes(result); !slices.Equal(got, []string{"created", "failed:MAILBOX.DOMAIN_PROVIDER_FAILED", "created"}) {
		t.Fatalf("outcomes = %v", got)
	}
	if _, err := fixture.repo.FindMailboxByAddress(context.Background(), "two@news.example.com"); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("the failed row was stored: %v", err)
	}
	for index, members := range map[int]int{0: 2, 2: 1} {
		events, _, err := fixture.repo.ListMailboxAccessEvents(context.Background(), result.Rows[index].MailboxID, 1, 100)
		if err != nil {
			t.Fatalf("ListMailboxAccessEvents: %v", err)
		}
		counts := map[domain.MailboxAccessEventType]int{}
		for _, event := range events {
			counts[event.Type]++
		}
		created, set := counts[domain.MailboxAccessCreated], counts[domain.MailboxAccessMemberSet]
		if created != 1 || set != members {
			t.Fatalf("row %d history: %d created, %d member_set; want 1 and %d", index+1, created, set, members)
		}
	}
}

func TestImportCommitSeesLaterChanges(t *testing.T) {
	f := newAccessFixture(t)
	row := importLine("sales@mail.example")
	if got := outcomes(runImport(t, f.service, f.admin, true, row)); !slices.Equal(got, []string{"valid"}) {
		t.Fatalf("dry run = %v", got)
	}
	if _, err := f.service.CreateMailbox(context.Background(), f.admin, sharedFor("sales")); err != nil {
		t.Fatalf("create meanwhile: %v", err)
	}
	if got := outcomes(runImport(t, f.service, f.admin, false, row)); !slices.Equal(got, []string{"failed:MAILBOX.ADDRESS_TAKEN"}) {
		t.Fatalf("real run = %v", got)
	}
}

// TestImportStripsFormulaGuard is Review Focus 4.
func TestImportStripsFormulaGuard(t *testing.T) {
	f := newAccessFixture(t)
	result := runImport(t, f.service, f.admin, false,
		MailboxImportRow{Address: "sales@mail.example", DisplayName: "'=Sales", Members: "u2@people.test"},
		MailboxImportRow{Address: "quote@mail.example", DisplayName: "'x", Members: "u2@people.test"})
	if got := outcomes(result); !slices.Equal(got, []string{"created", "created"}) {
		t.Fatalf("outcomes = %v", got)
	}
	names := map[string]string{}
	for _, row := range result.Rows {
		mailbox, err := f.repo.FindMailbox(context.Background(), accessOrg, row.MailboxID)
		if err != nil {
			t.Fatalf("FindMailbox: %v", err)
		}
		names[mailbox.Address] = mailbox.DisplayName
	}
	if names["sales@mail.example"] != "=Sales" || names["quote@mail.example"] != "'x" {
		t.Fatalf("display names = %v", names)
	}
	for raw, want := range map[string]string{"'=1": "=1", "'+1": "+1", "'-1": "-1", "'@x": "@x", "'x": "'x", "''=1": "''=1", "=1": "=1", "'": "'"} {
		if got := stripFormulaGuard(raw); got != want {
			t.Fatalf("stripFormulaGuard(%q) = %q; want %q", raw, got, want)
		}
	}
}

func TestImportRequiresManager(t *testing.T) {
	f := newAccessFixture(t)
	before := organizationMailboxCount(t, f)
	if _, err := f.service.ImportMailboxes(context.Background(), f.b, []MailboxImportRow{importLine("sales@mail.example")}, false); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member error = %v; want ErrPermissionDenied", err)
	}
	if after := organizationMailboxCount(t, f); after != before {
		t.Fatalf("a member's import created %d mailboxes", after-before)
	}
}

func TestImportMailboxesHandler(t *testing.T) {
	f := newHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	rows := make([]string, 0, 501)
	for i := 0; i < 501; i++ {
		rows = append(rows, fmt.Sprintf(`{"address":"box%d@mail.example","members":"u2@people.test"}`, i))
	}
	for name, body := range map[string]string{
		"501 rows":        `{"dry_run":true,"rows":[` + strings.Join(rows, ",") + `]}`,
		"missing dry_run": `{"rows":[{"address":"a@mail.example","members":"u2@people.test"}]}`,
		"no rows":         `{"dry_run":true,"rows":[]}`,
		"missing members": `{"dry_run":true,"rows":[{"address":"a@mail.example"}]}`,
	} {
		if recorder, decoded := f.do(http.MethodPost, "/mailbox-imports", body); recorder.Code != http.StatusUnprocessableEntity || decoded["error_code"] != "COMMON.VALIDATION_FAILED" {
			t.Fatalf("%s = %d %v; want 422 COMMON.VALIDATION_FAILED", name, recorder.Code, decoded)
		}
	}
	recorder, _ := f.do(http.MethodPost, "/mailbox-imports",
		`{"dry_run":true,"rows":[{"address":"sales@mail.example","members":"u2@people.test"},{"address":"admin@mail.example","members":"u2@people.test"}]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("import = %d %s", recorder.Code, recorder.Body.String())
	}
	want := `{"row":2,"address":"admin@mail.example","status":"failed","mailbox_id":null,"error_code":"MAILBOX.ADDRESS_RESERVED","missing_emails":[]}`
	if !strings.Contains(recorder.Body.String(), want) {
		t.Fatalf("body = %s; want it to hold %s", recorder.Body.String(), want)
	}
	if !strings.Contains(recorder.Body.String(), `"dry_run":true,"succeeded":1,"failed":1`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}

	member := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if recorder, decoded := member.do(http.MethodPost, "/mailbox-imports", `{"dry_run":true,"rows":[{"address":"a@mail.example","members":"u2@people.test"}]}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("member = %d %v; want 403", recorder.Code, decoded)
	}
}

// A deleted mailbox keeps its address, so a dry run answers what the real run would.
func TestImportDryRunSeesDeletedAddresses(t *testing.T) {
	f := newPostgresAccessFixture(t)
	if err := f.service.DeleteMailbox(context.Background(), f.admin, f.team.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	for _, dryRun := range []bool{true, false} {
		if got := outcomes(runImport(t, f.service, f.admin, dryRun, importLine("team@mail.example"))); !slices.Equal(got, []string{"failed:MAILBOX.ADDRESS_TAKEN"}) {
			t.Fatalf("dry run %v = %v", dryRun, got)
		}
	}
}
