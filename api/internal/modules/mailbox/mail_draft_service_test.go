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
	if err := db.AutoMigrate(&MailDraftPO{}); err != nil {
		t.Fatalf("migrate drafts: %v", err)
	}
	createOwner(t, db, accessOrg)
	transport := &fakeTransport{}
	memberships := newAccessMemberships()
	svc := NewService(repo, memberships, repo, repo, repo, repo,
		nil, nil, nil, transport, &fakeNotifier{}, accessSettings(), nil, nil, nil)
	return seedAccessFixture(t, svc, repo, transport, memberships)
}

func TestMailDraftsBelongToTheirWriterAndNeedSend(t *testing.T) {
	f := newDraftFixture(t)
	ctx := context.Background()

	saved, err := f.service.CreateMailDraft(ctx, f.b, MailDraftInput{
		MailboxID: f.team.ID, To: " ann@example.com,\n bob@example.com ", Subject: "Plan", Body: "Line\r\nTwo",
	})
	if err != nil || saved.ID == 0 || saved.To != "ann@example.com, bob@example.com" || saved.Body != "Line\nTwo" {
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
