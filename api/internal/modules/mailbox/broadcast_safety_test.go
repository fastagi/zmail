package mailbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func (f *broadcastFixture) contacts(t *testing.T, count int, prefix string) {
	t.Helper()
	for i := 0; i < count; i++ {
		f.contact(t, fmt.Sprintf("%s%d@example.com", prefix, i), "", domain.MailContactSubscribed, true)
	}
}

func (f *broadcastFixture) start(t *testing.T, testPercent int) *domain.MailBroadcast {
	t.Helper()
	broadcast, err := f.service.CreateBroadcast(context.Background(), as(1), BroadcastInput{
		Name: "Staged", MailboxID: f.mailbox.ID, TemplateID: f.template.ID, ListID: f.list.ID, TestPercent: testPercent,
	})
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	started, err := f.service.SendBroadcast(context.Background(), as(1), broadcast.ID, nil)
	if err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}
	return started
}

func (f *broadcastFixture) load(t *testing.T, id uint) *domain.MailBroadcast {
	t.Helper()
	broadcast, err := f.service.GetBroadcast(context.Background(), as(1), id)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	return broadcast
}

func TestABroadcastSendsItsTestWaveFirstAndTheRestAfterTheWait(t *testing.T) {
	fixture := newBroadcastFixture(t, 1000)
	ctx := context.Background()
	fixture.contacts(t, 10, "staged")
	broadcast := fixture.start(t, 20)
	if broadcast.TestPercent != 20 || broadcast.TestWaitMinutes != 30 || broadcast.CurrentWave != 1 {
		t.Fatalf("started = %+v", broadcast)
	}

	if processed, err := fixture.service.DispatchBroadcasts(ctx, 50); err != nil || processed != 2 {
		t.Fatalf("test wave = %d, %v; want 2 of 10", processed, err)
	}
	waiting := fixture.load(t, broadcast.ID)
	if waiting.Status != domain.MailBroadcastPaused || waiting.StopReason != stopReasonTestWave || waiting.ResumeAt == nil ||
		waiting.Counts.Sent != 2 || waiting.Counts.Pending != 8 {
		t.Fatalf("after the test wave = %+v", waiting)
	}
	// Before the wait is over nothing more goes out.
	fixture.now = fixture.now.Add(29 * time.Minute)
	if processed, _ := fixture.service.DispatchBroadcasts(ctx, 50); processed != 0 {
		t.Fatalf("sent %d during the wait", processed)
	}
	fixture.now = *waiting.ResumeAt
	if processed, err := fixture.service.DispatchBroadcasts(ctx, 50); err != nil || processed != 8 {
		t.Fatalf("rest = %d, %v; want 8", processed, err)
	}
	if done := fixture.load(t, broadcast.ID); done.Status != domain.MailBroadcastSent || done.Counts.Sent != 10 || done.CurrentWave != 2 {
		t.Fatalf("done = %+v", done)
	}
}

func TestABroadcastPausesItselfWhenTooManyMessagesBounce(t *testing.T) {
	fixture := newBroadcastFixture(t, 1000)
	ctx := context.Background()
	fixture.contacts(t, 25, "health")
	for i := 0; i < 3; i++ {
		fixture.transport.bounces[fmt.Sprintf("health%d@example.com", i)] = true
	}
	broadcast := fixture.start(t, 0)

	if processed, err := fixture.service.DispatchBroadcasts(ctx, 20); err != nil || processed != 20 {
		t.Fatalf("first batch = %d, %v", processed, err)
	}
	// 3 of 20 bounced: 15% is over the 5% limit, so the next round pauses instead of sending.
	if processed, err := fixture.service.DispatchBroadcasts(ctx, 20); err != nil || processed != 0 {
		t.Fatalf("second batch = %d, %v; want a pause", processed, err)
	}
	paused := fixture.load(t, broadcast.ID)
	if paused.Status != domain.MailBroadcastPaused || paused.StopReason != stopReasonBounceRate ||
		paused.Counts.Bounced != 3 || paused.Counts.Pending != 5 {
		t.Fatalf("paused = %+v", paused)
	}

	// An owner decides to continue anyway: the check no longer stops this broadcast.
	resumed, err := fixture.service.ResumeBroadcast(ctx, as(1), broadcast.ID)
	if err != nil || resumed.Status != domain.MailBroadcastSending || !resumed.SafetyOverride {
		t.Fatalf("resumed = %+v, %v", resumed, err)
	}
	if processed, err := fixture.service.DispatchBroadcasts(ctx, 20); err != nil || processed != 5 {
		t.Fatalf("after resume = %d, %v", processed, err)
	}
	if done := fixture.load(t, broadcast.ID); done.Status != domain.MailBroadcastSent {
		t.Fatalf("done = %+v", done)
	}
}

func TestAHealthyTestWaveContinuesButASickOneStops(t *testing.T) {
	fixture := newBroadcastFixture(t, 1000)
	ctx := context.Background()
	fixture.contacts(t, 100, "wave")
	for i := 0; i < 100; i++ {
		// Every recipient bounces, so whichever 20 form the test wave all bounce.
		fixture.transport.bounces[fmt.Sprintf("wave%d@example.com", i)] = true
	}
	broadcast := fixture.start(t, 20)
	if processed, _ := fixture.service.DispatchBroadcasts(ctx, 100); processed != 20 {
		t.Fatalf("test wave = %d", processed)
	}
	fixture.now = fixture.now.Add(31 * time.Minute)
	if processed, _ := fixture.service.DispatchBroadcasts(ctx, 100); processed != 0 {
		t.Fatalf("a sick test wave let %d more out", processed)
	}
	if stopped := fixture.load(t, broadcast.ID); stopped.Status != domain.MailBroadcastPaused ||
		stopped.StopReason != stopReasonBounceRate || stopped.Counts.Pending != 80 {
		t.Fatalf("stopped = %+v", stopped)
	}
}

func TestOwnersPauseResumeAndCancelABroadcast(t *testing.T) {
	fixture := newBroadcastFixture(t, 1000)
	ctx := context.Background()
	fixture.contacts(t, 6, "manual")
	broadcast := fixture.start(t, 0)

	paused, _, err := fixture.service.PauseBroadcast(ctx, as(1), broadcast.ID)
	if err != nil || paused.Status != domain.MailBroadcastPaused || paused.StopReason != stopReasonOwner {
		t.Fatalf("paused = %+v, %v", paused, err)
	}
	if processed, _ := fixture.service.DispatchBroadcasts(ctx, 50); processed != 0 {
		t.Fatalf("a paused broadcast sent %d", processed)
	}
	if _, _, crossErr := fixture.service.PauseBroadcast(ctx, as(2), broadcast.ID); !errors.Is(crossErr, ErrMailBroadcastNotFound) {
		t.Fatalf("pause from another organization = %v", crossErr)
	}
	resumed, err := fixture.service.ResumeBroadcast(ctx, as(1), broadcast.ID)
	if err != nil || resumed.Status != domain.MailBroadcastSending || resumed.SafetyOverride || resumed.StopReason != "" {
		t.Fatalf("resumed = %+v, %v", resumed, err)
	}
	if _, resumeErr := fixture.service.ResumeBroadcast(ctx, as(1), broadcast.ID); !errors.Is(resumeErr, ErrMailBroadcastNotEditable) {
		t.Fatalf("resume a sending broadcast = %v", resumeErr)
	}
	if _, _, pauseErr := fixture.service.PauseBroadcast(ctx, as(1), broadcast.ID); pauseErr != nil {
		t.Fatalf("pause again: %v", pauseErr)
	}
	canceled, _, err := fixture.service.CancelBroadcast(ctx, as(1), broadcast.ID)
	if err != nil || canceled.Status != domain.MailBroadcastCanceled || canceled.Counts.Canceled != 6 {
		t.Fatalf("canceled = %+v, %v", canceled, err)
	}
}

// Issue #143: pausing during a test wave's wait and resuming goes back to waiting for the same
// window, not to sending.
func TestResumingAWaitingTestWavePausedByHandKeepsItsWindow(t *testing.T) {
	fixture := newBroadcastFixture(t, 1000)
	ctx := context.Background()
	fixture.contacts(t, 2, "window")
	broadcast := fixture.start(t, 50)
	if processed, _ := fixture.service.DispatchBroadcasts(ctx, 50); processed != 1 {
		t.Fatalf("test wave = %d", processed)
	}
	waiting := fixture.load(t, broadcast.ID)
	if waiting.StopReason != stopReasonTestWave || waiting.ResumeAt == nil {
		t.Fatalf("waiting = %+v", waiting)
	}
	window := *waiting.ResumeAt

	fixture.now = fixture.now.Add(10 * time.Minute)
	paused, _, err := fixture.service.PauseBroadcast(ctx, as(1), broadcast.ID)
	if err != nil || paused.Status != domain.MailBroadcastPaused || paused.StopReason != stopReasonOwner {
		t.Fatalf("paused = %+v, %v", paused, err)
	}
	fixture.now = fixture.now.Add(5 * time.Minute)
	resumed, err := fixture.service.ResumeBroadcast(ctx, as(1), broadcast.ID)
	if err != nil || resumed.Status != domain.MailBroadcastPaused || resumed.StopReason != stopReasonTestWave ||
		resumed.CurrentWave != 1 || resumed.ResumeAt == nil || !resumed.ResumeAt.Equal(window) {
		t.Fatalf("resumed = %+v, %v; want waiting until %s", resumed, err, window)
	}
	if processed, _ := fixture.service.DispatchBroadcasts(ctx, 50); processed != 0 {
		t.Fatalf("sent %d before the window ended", processed)
	}
	if still := fixture.load(t, broadcast.ID); still.ResumeAt == nil || !still.ResumeAt.Equal(window) {
		t.Fatalf("the worker moved the window: %+v", still)
	}
	fixture.now = window
	if processed, err := fixture.service.DispatchBroadcasts(ctx, 50); err != nil || processed != 1 {
		t.Fatalf("rest = %d, %v; want 1", processed, err)
	}
	if done := fixture.load(t, broadcast.ID); done.Status != domain.MailBroadcastSent || done.Counts.Sent != 2 {
		t.Fatalf("done = %+v", done)
	}
}

// A test wave paused by hand before it finished, whose last messages finish meanwhile, waits a
// full window after the resume.
func TestResumingAFinishedTestWaveWithoutAWindowStartsOne(t *testing.T) {
	fixture := newBroadcastFixture(t, 1000)
	ctx := context.Background()
	fixture.contacts(t, 2, "late")
	broadcast := fixture.start(t, 50)
	if _, _, err := fixture.service.PauseBroadcast(ctx, as(1), broadcast.ID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// The test recipient finishes while the broadcast is paused, as an in-flight lease would.
	if err := fixture.repo.db.Exec(`UPDATE mail_broadcast_recipients SET status = 'sent'
		WHERE broadcast_id = ? AND wave = 1`, broadcast.ID).Error; err != nil {
		t.Fatalf("finish the test wave: %v", err)
	}
	resumed, err := fixture.service.ResumeBroadcast(ctx, as(1), broadcast.ID)
	want := fixture.now.Add(30 * time.Minute)
	if err != nil || resumed.Status != domain.MailBroadcastPaused || resumed.StopReason != stopReasonTestWave ||
		resumed.ResumeAt == nil || !resumed.ResumeAt.Equal(want) {
		t.Fatalf("resumed = %+v, %v; want waiting until %s", resumed, err, want)
	}
}

func TestBroadcastTestSettingsAreValidated(t *testing.T) {
	fixture := newBroadcastFixture(t, 1000)
	for _, input := range []BroadcastInput{{TestPercent: 51}, {TestPercent: -1}, {TestPercent: 10, TestWaitMinutes: 4}, {TestWaitMinutes: 1441}} {
		input.Name, input.MailboxID, input.TemplateID, input.ListID = "x", fixture.mailbox.ID, fixture.template.ID, fixture.list.ID
		if _, err := fixture.service.CreateBroadcast(context.Background(), as(1), input); !errors.Is(err, ErrMailBroadcastInvalid) {
			t.Fatalf("CreateBroadcast(%+v) = %v", input, err)
		}
	}
}

func TestHealthStopNeedsASampleBeforeJudging(t *testing.T) {
	cases := []struct {
		counts domain.MailBroadcastCounts
		want   string
	}{
		{domain.MailBroadcastCounts{Sent: 10, Bounced: 10}, ""},
		{domain.MailBroadcastCounts{Sent: 100, Bounced: 4}, ""},
		{domain.MailBroadcastCounts{Sent: 95, Failed: 5, Bounced: 5}, stopReasonBounceRate},
		{domain.MailBroadcastCounts{Sent: 1000, Complained: 2}, ""},
		{domain.MailBroadcastCounts{Sent: 1000, Complained: 3}, stopReasonComplaintRate},
	}
	for _, c := range cases {
		if got := healthStop(c.counts); got != c.want {
			t.Fatalf("healthStop(%+v) = %q, want %q", c.counts, got, c.want)
		}
	}
}
