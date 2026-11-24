package mailbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func newDraftFixture(t *testing.T) *accessFixture {
	t.Helper()
	repo, db := openRepository(t)
	createOwner(t, db, accessOrg)
	transport := &fakeTransport{}
	memberships := newAccessMemberships()
	svc := NewService(repo, memberships, repo, repo, repo, repo,
		nil, nil, nil, transport, &fakeNotifier{}, accessSettings(), nil, nil, nil, nil)
	return seedAccessFixture(t, svc, repo, transport, memberships)
}

func TestMailDraftsBelongToTheirWriterAndNeedSend(t *testing.T) {
	f := newDraftFixture(t)
	ctx := context.Background()

	saved, err := f.service.CreateMailDraft(ctx, f.b, MailDraftInput{
		MailboxID: f.team.ID, To: " ann@example.com,\n bob@example.com ", Subject: "Plan", Body: "Line\r\nTwo",
	})
	if err != nil || saved.ID == 0 || saved.To != "ann@example.com,bob@example.com" || saved.Body != "Line\nTwo" {
		t.Fatalf("create = %+v, %v", saved, err)
	}
	if _, err = f.service.CreateMailDraft(ctx, f.c, MailDraftInput{MailboxID: f.team.ID}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("viewer without send = %v; want permission denied", err)
	}
	others, err := f.service.MailDrafts(ctx, f.c)
	if err != nil || len(others) != 0 {
		t.Fatalf("another member's drafts = %+v, %v", others, err)
	}
	if _, err = f.service.UpdateMailDraft(ctx, f.c, saved.ID, MailDraftInput{MailboxID: f.team.ID}); !errors.Is(err, ErrMailDraftNotFound) {
		t.Fatalf("update another's = %v; want not found", err)
	}
	if err = f.service.DeleteMailDraft(ctx, f.c, saved.ID); !errors.Is(err, ErrMailDraftNotFound) {
		t.Fatalf("delete another's = %v; want not found", err)
	}

	updated, err := f.service.UpdateMailDraft(ctx, f.b, saved.ID, MailDraftInput{MailboxID: f.personal.ID, Subject: "Plan B"})
	if err != nil || updated.MailboxID != f.personal.ID || updated.Subject != "Plan B" || updated.To != "" {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	mine, err := f.service.MailDrafts(ctx, f.b)
	if err != nil || len(mine) != 1 || mine[0].ID != saved.ID {
		t.Fatalf("list = %+v, %v", mine, err)
	}
	if err = f.service.DeleteMailDraft(ctx, f.b, saved.ID); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if mine, _ = f.service.MailDrafts(ctx, f.b); len(mine) != 0 {
		t.Fatalf("after delete = %+v", mine)
	}
}

func TestMailDraftRejectsOversizedAndControlCharacters(t *testing.T) {
	f := newDraftFixture(t)
	for name, input := range map[string]MailDraftInput{
		"subject too long":  {MailboxID: f.team.ID, Subject: strings.Repeat("é", 999)},
		"control character": {MailboxID: f.team.ID, Body: "a\u0007b"},
	} {
		_, err := f.service.CreateMailDraft(context.Background(), f.b, input)
		if _, ok := asValidationError(err); !ok {
			t.Fatalf("%s = %v; want validation error", name, err)
		}
	}
}

func TestMailDraftRecipientLinesStaySeparateAddresses(t *testing.T) {
	cases := map[string]string{
		"a@example.com\nb@example.com":      "a@example.com,b@example.com",
		"a@example.com,\r\n  b@example.com": "a@example.com,b@example.com",
		"  Ann  <a@example.com>  ":          "Ann <a@example.com>",
		"\n\n":                              "",
	}
	for in, want := range cases {
		if got := normalizeDraftLine(in); got != want {
			t.Errorf("normalizeDraftLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMailDraftCapHoldsUnderConcurrentSaves(t *testing.T) {
	f := newDraftFixture(t)
	ctx := context.Background()
	for i := 0; i < domain.MailDraftsPerMember-1; i++ {
		if _, err := f.service.CreateMailDraft(ctx, f.b, MailDraftInput{MailboxID: f.team.ID}); err != nil {
			t.Fatalf("seed draft %d: %v", i, err)
		}
	}
	errs := make(chan error, 4)
	for i := 0; i < cap(errs); i++ {
		go func() {
			_, err := f.service.CreateMailDraft(ctx, f.b, MailDraftInput{MailboxID: f.team.ID})
			errs <- err
		}()
	}
	saved := 0
	for i := 0; i < cap(errs); i++ {
		err := <-errs
		switch {
		case err == nil:
			saved++
		case !errors.Is(err, ErrMailDraftLimitReached):
			t.Fatalf("concurrent save: %v", err)
		}
	}
	if saved != 1 {
		t.Fatalf("concurrent saves at 199 drafts stored %d, want 1", saved)
	}
}

func TestMailDraftListPreviewsAndShowReadsInFull(t *testing.T) {
	f := newDraftFixture(t)
	ctx := context.Background()
	body := strings.Repeat("é", domain.MailDraftListBodyLength+50)
	saved, err := f.service.CreateMailDraft(ctx, f.b, MailDraftInput{MailboxID: f.team.ID, Body: body})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	list, err := f.service.MailDrafts(ctx, f.b)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if got := []rune(toMailDraftSummaryResponse(list[0]).Preview); len(got) != mailDraftPreviewLength {
		t.Fatalf("preview has %d characters, want %d", len(got), mailDraftPreviewLength)
	}
	full, err := f.service.MailDraft(ctx, f.b, saved.ID)
	if err != nil || full.Body != body {
		t.Fatalf("show = %d characters, %v", len([]rune(full.Body)), err)
	}
	if _, err = f.service.MailDraft(ctx, f.c, saved.ID); !errors.Is(err, ErrMailDraftNotFound) {
		t.Fatalf("another member read the draft: %v", err)
	}
}
