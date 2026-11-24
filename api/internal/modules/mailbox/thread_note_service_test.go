package mailbox

import (
	"context"
	"errors"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestSharedThreadNotesNeedSendAndStayWithTheirAuthor(t *testing.T) {
	f := newAssignmentFixture(t)
	ctx := context.Background()
	thread := f.teamMessage.ThreadID

	empty, err := f.service.MailThreadNotes(ctx, f.c, f.team.ID, thread)
	if err != nil || len(empty) != 0 {
		t.Fatalf("no notes = %v, %v", empty, err)
	}
	if _, err = f.service.AddMailThreadNote(ctx, f.c, f.team.ID, thread, "mine"); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("viewer adds = %v; want permission denied", err)
	}
	var invalid *ValidationError
	for _, body := range []string{"   ", "bell\a", string(make([]rune, domain.MailThreadNoteMaxLength+1))} {
		if _, err = f.service.AddMailThreadNote(ctx, f.b, f.team.ID, thread, body); !errors.As(err, &invalid) {
			t.Fatalf("body %q = %v; want validation", body[:min(len(body), 8)], err)
		}
	}
	note, err := f.service.AddMailThreadNote(ctx, f.b, f.team.ID, thread, "  Call them back\r\ntomorrow  ")
	if err != nil || note.Body != "Call them back\ntomorrow" || note.AuthorUserID == nil || *note.AuthorUserID != f.b.UserID {
		t.Fatalf("add = %+v, %v", note, err)
	}
	read, err := f.service.MailThreadNotes(ctx, f.c, f.team.ID, thread)
	if err != nil || len(read) != 1 || read[0].ID != note.ID {
		t.Fatalf("read = %+v, %v", read, err)
	}
	if err = f.service.DeleteMailThreadNote(ctx, f.c, f.team.ID, thread, note.ID); !errors.Is(err, ErrMailThreadNoteNotFound) {
		t.Fatalf("another member deletes = %v; want not found", err)
	}
	if err = f.service.DeleteMailThreadNote(ctx, f.b, f.team.ID, thread, note.ID); err != nil {
		t.Fatalf("author deletes: %v", err)
	}
	if _, err = f.service.MailThreadNotes(ctx, f.b, f.personal.ID, f.personalMessage.ThreadID); !errors.Is(err, ErrMailThreadAssignmentUnavailable) {
		t.Fatalf("personal mailbox = %v; want unavailable", err)
	}
	if _, err = f.service.MailThreadNotes(ctx, f.d, f.team.ID, thread); err == nil {
		t.Fatal("outsider read notes")
	}
	if _, err = f.service.MailThreadNotes(ctx, f.b, f.team.ID, thread+1000); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("missing thread = %v; want not found", err)
	}

	for i := 0; i < domain.MailThreadNoteLimit; i++ {
		if _, err = f.service.notes.CreateMailThreadNote(ctx, domain.MailThreadNote{
			MailboxID: f.team.ID, ThreadID: thread, Body: "n", CreatedAt: f.service.now(),
		}, domain.MailThreadNoteLimit); err != nil {
			t.Fatalf("fill note %d: %v", i, err)
		}
	}
	if _, err = f.service.AddMailThreadNote(ctx, f.b, f.team.ID, thread, "one more"); !errors.Is(err, ErrMailThreadNoteLimitReached) {
		t.Fatalf("over the limit = %v; want limit reached", err)
	}
}
