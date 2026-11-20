package mailbox

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

var exportClock = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

// exportFixture is the access scenario on a fixed clock, with team@ created at that instant and
// one personal-lane send attempt an hour before it.
func exportFixture(t *testing.T) *accessFixture {
	t.Helper()
	f := newAccessFixture(t)
	f.service.now = func() time.Time { return exportClock }
	fake := f.repo.(*fakeRepository)
	fake.mailboxes[f.team.ID].CreatedAt = exportClock
	fake.mailboxes[f.personal.ID].CreatedAt = exportClock.Add(-time.Hour)
	attempt(t, f.repo, f.team, exportClock.Add(-time.Hour), domain.MailStatusSent, domain.SendLanePersonal)
	return f
}

func exportCSV(t *testing.T, f *accessFixture, mailboxType domain.MailboxType) string {
	t.Helper()
	rows, err := f.service.ExportMailboxes(context.Background(), f.admin, mailboxType)
	if err != nil {
		t.Fatalf("ExportMailboxes: %v", err)
	}
	var out bytes.Buffer
	if err = writeMailboxCSV(&out, rows); err != nil {
		t.Fatalf("writeMailboxCSV: %v", err)
	}
	return out.String()
}

func TestExportCSVShape(t *testing.T) {
	f := exportFixture(t)
	body := exportCSV(t, f, domain.MailboxTypeShared)
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatalf("body does not start with a byte order mark: %q", body)
	}
	want := "\xEF\xBB\xBF" +
		"address,display_name,members,member_count,sent_last_24h,created_at\r\n" +
		"team@mail.example,,u2@people.test:send;u3@people.test:view,2,1,2026-11-19T10:00:00Z\r\n"
	if body != want {
		t.Fatalf("body = %q; want %q", body, want)
	}
}

func TestExportPersonalListsTheOwner(t *testing.T) {
	f := exportFixture(t)
	want := "b@mail.example,,u2@people.test,0,0,2026-11-19T09:00:00Z\r\n"
	if body := exportCSV(t, f, domain.MailboxTypePersonal); !strings.HasSuffix(body, want) || strings.Contains(body, "team@") {
		t.Fatalf("body = %q; want one b@ line %q", body, want)
	}
}

// TestExportGuardsFormulaCells is Review Focus 4.
func TestExportGuardsFormulaCells(t *testing.T) {
	f := exportFixture(t)
	for localPart, name := range map[string]string{"sum": "=SUM(A1)", "plus": "+1", "minus": "-x", "at": "@x"} {
		input := sharedFor(localPart)
		input.DisplayName = name
		if _, err := f.service.CreateMailbox(context.Background(), f.admin, input); err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
	}
	body := exportCSV(t, f, domain.MailboxTypeShared)
	for _, cell := range []string{",'=SUM(A1),", ",'+1,", ",'-x,", ",'@x,"} {
		if !strings.Contains(body, cell) {
			t.Fatalf("body = %q; want it to hold %s", body, cell)
		}
	}
	for raw, want := range map[string]string{"=1": "'=1", "x": "x", "": "", "'=1": "'=1", "a=1": "a=1"} {
		if got := csvCell(raw); got != want {
			t.Fatalf("csvCell(%q) = %q; want %q", raw, got, want)
		}
	}
}

func TestExportOnlyRequestedType(t *testing.T) {
	f := exportFixture(t)
	if body := exportCSV(t, f, domain.MailboxTypeShared); strings.Contains(body, "b@mail.example") {
		t.Fatalf("a shared export holds the personal mailbox: %q", body)
	}
}

func TestExportRequiresManager(t *testing.T) {
	f := exportFixture(t)
	if _, err := f.service.ExportMailboxes(context.Background(), f.b, domain.MailboxTypeShared); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member error = %v; want ErrPermissionDenied", err)
	}
	if _, err := f.service.ExportMailboxes(context.Background(), f.admin, ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("no type = %v; want ErrInvalidInput", err)
	}
}

func TestExportFilename(t *testing.T) {
	if got := exportFilename(domain.MailboxTypeShared, exportClock); got != "shared-mailboxes-20261119.csv" {
		t.Fatalf("shared = %q", got)
	}
	if got := exportFilename(domain.MailboxTypePersonal, exportClock.Add(15*time.Hour)); got != "personal-mailboxes-20261120.csv" {
		t.Fatalf("personal = %q", got)
	}
}

func TestExportMailboxesHandler(t *testing.T) {
	f := newHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	f.service.now = func() time.Time { return exportClock }
	if _, err := f.service.CreateMailbox(context.Background(), viewerOf(7, handlerUser, domain.OrganizationRoleAdmin), sharedWithHandlerUser("team")); err != nil {
		t.Fatalf("create: %v", err)
	}
	recorder, _ := f.do(http.MethodGet, "/mailbox-exports?type=shared", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("export = %d %s", recorder.Code, recorder.Body.String())
	}
	headers := map[string]string{
		"Content-Type":           "text/csv; charset=utf-8",
		"Content-Disposition":    `attachment; filename="shared-mailboxes-20261119.csv"`,
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "private, no-store",
	}
	for name, want := range headers {
		if got := recorder.Header().Get(name); got != want {
			t.Fatalf("%s = %q; want %q", name, got, want)
		}
	}
	if !strings.Contains(recorder.Body.String(), "team@mail.example,") {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	for _, path := range []string{"/mailbox-exports", "/mailbox-exports?type=all"} {
		if recorder, decoded := f.do(http.MethodGet, path, ""); recorder.Code != http.StatusUnprocessableEntity || decoded["error_code"] != "COMMON.VALIDATION_FAILED" {
			t.Fatalf("%s = %d %v; want 422", path, recorder.Code, decoded)
		}
	}
	member := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if recorder, decoded := member.do(http.MethodGet, "/mailbox-exports?type=shared", ""); recorder.Code != http.StatusForbidden || decoded["error_code"] != "PERMISSION.DENIED" {
		t.Fatalf("member = %d %v; want 403 PERMISSION.DENIED", recorder.Code, decoded)
	}
}

func TestExportReadsMembersFromPostgres(t *testing.T) {
	f := newPostgresAccessFixture(t)
	rows, err := f.service.ExportMailboxes(context.Background(), f.admin, domain.MailboxTypeShared)
	if err != nil {
		t.Fatalf("ExportMailboxes: %v", err)
	}
	if len(rows) != 1 || rows[0].Entry.Mailbox.Address != "team@mail.example" || len(rows[0].Members) != 2 ||
		rows[0].Members[0] != (MailboxExportMember{Email: "u2@people.test", Permission: domain.MailboxPermissionSend}) ||
		rows[0].Members[1] != (MailboxExportMember{Email: "u3@people.test", Permission: domain.MailboxPermissionView}) {
		t.Fatalf("rows = %+v", rows)
	}
	personal, err := f.service.ExportMailboxes(context.Background(), f.admin, domain.MailboxTypePersonal)
	if err != nil || len(personal) != 1 || len(personal[0].Members) != 1 || personal[0].Members[0].Email != "u2@people.test" {
		t.Fatalf("personal = %+v, %v", personal, err)
	}
}
