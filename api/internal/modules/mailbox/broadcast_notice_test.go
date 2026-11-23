package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

// recordingNotices keeps every broadcast notice; fail makes the next sends fail.
type recordingNotices struct {
	sent []BroadcastNotice
	fail error
}

func (r *recordingNotices) SendBroadcastNotice(_ context.Context, notice BroadcastNotice) error {
	if r.fail != nil {
		return r.fail
	}
	r.sent = append(r.sent, notice)
	return nil
}

func (r *recordingNotices) only(t *testing.T) BroadcastNotice {
	t.Helper()
	if len(r.sent) != 1 {
		t.Fatalf("notices = %+v; want exactly one", r.sent)
	}
	return r.sent[0]
}

func TestTheStarterIsToldOnceWhenABroadcastFinishes(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	fixture.contacts(t, 3, "done")
	fixture.transport.bounces["done2@example.com"] = true
	broadcast := fixture.start(t, 0)
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	notice := fixture.notices.only(t)
	if notice.Kind != noticeKindSent || notice.UserID != testCreator || notice.Email != fmt.Sprintf("u%d@people.test", testCreator) {
		t.Fatalf("notice = %+v", notice)
	}
	for _, want := range []string{`"Staged"`, "finished"} {
		if !strings.Contains(notice.Title, want) {
			t.Fatalf("title %q lacks %q", notice.Title, want)
		}
	}
	for _, want := range []string{"2 of 3", "1 failed", "see who"} {
		if !strings.Contains(notice.Body, want) {
			t.Fatalf("body %q lacks %q", notice.Body, want)
		}
	}
	wantPath := fmt.Sprintf("/console/mail-broadcasts?org=1&open=%d", broadcast.ID)
	if notice.Path != wantPath || notice.URL != "https://app.example"+wantPath {
		t.Fatalf("links = %q, %q", notice.Path, notice.URL)
	}
	// Later rounds do not repeat it.
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	fixture.notices.only(t)
}

func TestTheStarterIsToldWhenTheSendLimitHoldsABroadcast(t *testing.T) {
	fixture := newBroadcastFixture(t, 3)
	ctx := context.Background()
	fixture.contacts(t, 5, "limit")
	fixture.start(t, 0)
	for range 2 {
		if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
			t.Fatalf("DispatchBroadcasts: %v", err)
		}
	}
	notice := fixture.notices.only(t)
	if notice.Kind != noticeKindPaused || !strings.Contains(notice.Title, "daily send limit") {
		t.Fatalf("notice = %+v", notice)
	}
	for _, want := range []string{"2 recipients are waiting", "UTC", "on its own"} {
		if !strings.Contains(notice.Body, want) {
			t.Fatalf("body %q lacks %q", notice.Body, want)
		}
	}
}

func TestTheStarterIsToldWhenTheHealthCheckPausesABroadcast(t *testing.T) {
	fixture := newBroadcastFixture(t, 1000)
	ctx := context.Background()
	fixture.contacts(t, 25, "health")
	for i := range 3 {
		fixture.transport.bounces[fmt.Sprintf("health%d@example.com", i)] = true
	}
	fixture.start(t, 0)
	for range 2 {
		if _, err := fixture.service.DispatchBroadcasts(ctx, 20); err != nil {
			t.Fatalf("DispatchBroadcasts: %v", err)
		}
	}
	notice := fixture.notices.only(t)
	if notice.Kind != noticeKindPaused || !strings.Contains(notice.Title, "bounce") {
		t.Fatalf("notice = %+v", notice)
	}
	for _, want := range []string{"3 of 20", "5%", "resume or cancel", "5 recipients are waiting"} {
		if !strings.Contains(notice.Body, want) {
			t.Fatalf("body %q lacks %q", notice.Body, want)
		}
	}
}

func TestTheStarterIsToldWhenNoRecipientCouldBeSent(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	fixture.contacts(t, 2, "gone")
	fixture.transport.bounces["gone0@example.com"] = true
	fixture.transport.bounces["gone1@example.com"] = true
	fixture.start(t, 0)
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	notice := fixture.notices.only(t)
	if notice.Kind != noticeKindFailed || !strings.Contains(notice.Body, "None of the 2 recipients") {
		t.Fatalf("notice = %+v", notice)
	}
}

func TestStoppingABroadcastByHandSendsNoNotice(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	fixture.contacts(t, 2, "hand")
	paused := fixture.start(t, 0)
	if _, _, err := fixture.service.PauseBroadcast(ctx, as(1), paused.ID); err != nil {
		t.Fatalf("PauseBroadcast: %v", err)
	}
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	if _, _, err := fixture.service.CancelBroadcast(ctx, as(1), paused.ID); err != nil {
		t.Fatalf("CancelBroadcast: %v", err)
	}
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	if len(fixture.notices.sent) != 0 {
		t.Fatalf("notices = %+v; want none for a person's own action", fixture.notices.sent)
	}
}

func TestANoticeThatCouldNotBeSentIsTriedAgain(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	fixture.contacts(t, 1, "retry")
	fixture.start(t, 0)
	fixture.notices.fail = errors.New("mail provider down")
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("a failed notice must not stop sending: %v", err)
	}
	fixture.notices.fail = nil
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	if notice := fixture.notices.only(t); notice.Key == "" || notice.Kind != noticeKindSent {
		t.Fatalf("notice = %+v", notice)
	}
}

func TestAStarterWhoLeftTheOrganizationIsNotTold(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	fixture.contacts(t, 1, "left")
	broadcast := fixture.start(t, 0)
	fixture.memberships.absent = map[uint]bool{testCreator: true}
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	if len(fixture.notices.sent) != 0 {
		t.Fatalf("notices = %+v; want none", fixture.notices.sent)
	}
	// The state is recorded, so the worker does not look at it again.
	due, err := fixture.repo.ListBroadcastNoticesDue(ctx, fixture.now.Add(-sendLimitWindow), 10)
	if err != nil {
		t.Fatalf("ListBroadcastNoticesDue: %v", err)
	}
	for _, item := range due {
		if item.Broadcast.ID == broadcast.ID {
			t.Fatalf("broadcast still due: %+v", item)
		}
	}
	if done := fixture.load(t, broadcast.ID); done.StartedBy != testCreator || done.Status != domain.MailBroadcastSent {
		t.Fatalf("done = %+v", done)
	}
}
