package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func newSignatureFixture(t *testing.T) *accessFixture {
	t.Helper()
	repo, db := openRepository(t)
	if err := db.AutoMigrate(&MailSignaturePO{}); err != nil {
		t.Fatalf("migrate signatures: %v", err)
	}
	createOwner(t, db, accessOrg)
	transport := &fakeTransport{}
	memberships := newAccessMemberships()
	svc := NewService(repo, memberships, repo, repo, repo, repo,
		nil, nil, nil, transport, &fakeNotifier{}, accessSettings(), nil, nil, nil, nil)
	return seedAccessFixture(t, svc, repo, transport, memberships)
}

func TestMailSignatureIsPerMemberAndNeedsSend(t *testing.T) {
	f := newSignatureFixture(t)
	ctx := context.Background()

	empty, err := f.service.MailSignature(ctx, f.b, f.team.ID)
	if err != nil || empty.Body != "" || empty.UpdatedAt != nil {
		t.Fatalf("unsaved = %+v, %v", empty, err)
	}
	saved, err := f.service.SaveMailSignature(ctx, f.b, f.team.ID, "Best,\r\nB  \n\n")
	if err != nil || saved.Body != "Best,\nB" || saved.UpdatedAt == nil {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	other, err := f.service.MailSignature(ctx, f.b, f.personal.ID)
	if err != nil || other.Body != "" {
		t.Fatalf("another mailbox's = %+v, %v", other, err)
	}
	if _, viewErr := f.service.MailSignature(ctx, f.c, f.team.ID); !errors.Is(viewErr, domain.ErrPermissionDenied) {
		t.Fatalf("view member err = %v", viewErr)
	}
	if _, outErr := f.service.SaveMailSignature(ctx, f.d, f.team.ID, "x"); !errors.Is(outErr, ErrMailboxNotFound) {
		t.Fatalf("outsider err = %v", outErr)
	}
	var invalid *ValidationError
	if _, longErr := f.service.SaveMailSignature(ctx, f.b, f.team.ID, strings.Repeat("é", 2001)); !errors.As(longErr, &invalid) {
		t.Fatalf("too long err = %v", longErr)
	}
	if _, ctrlErr := f.service.SaveMailSignature(ctx, f.b, f.team.ID, "a\x00b"); !errors.As(ctrlErr, &invalid) {
		t.Fatalf("control err = %v", ctrlErr)
	}
	cleared, err := f.service.SaveMailSignature(ctx, f.b, f.team.ID, "  \n")
	if err != nil || cleared.Body != "" || cleared.UpdatedAt != nil {
		t.Fatalf("clear = %+v, %v", cleared, err)
	}
	again, err := f.service.MailSignature(ctx, f.b, f.team.ID)
	if err != nil || again.Body != "" {
		t.Fatalf("after clear = %+v, %v", again, err)
	}
}

func TestUpdateMailSignatureAnswersInvalidBodiesWith422(t *testing.T) {
	f := newSignatureFixture(t)
	router := accessRouter(f.service, f.b)
	path := fmt.Sprintf("/mailboxes/%d/signature", f.team.ID)
	for name, payload := range map[string]string{
		"control character": `{"body":"a\u0007b"}`,
		"too long":          `{"body":"` + strings.Repeat("é", 2001) + `"}`,
	} {
		recorder, body := serve(router, http.MethodPut, path, payload)
		if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeValidationFailed {
			t.Fatalf("%s = %d %v; want 422 %s", name, recorder.Code, body, domain.CodeValidationFailed)
		}
	}
	recorder, body := serve(router, http.MethodPut, path, `{"body":"Best,\nB"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid = %d %v", recorder.Code, body)
	}
}
