package mailbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// queueFixture is the PostgreSQL access scene with a clock the test moves and a notifier it
// can read. The send limit is 5 per rolling 24 hours.
type queueFixture struct {
	*accessFixture
	notifier *fakeNotifier
	clock    time.Time
}

func newQueueFixture(t *testing.T) *queueFixture {
	t.Helper()
	repo, db := openRepository(t)
	createOwner(t, db, accessOrg)
	transport := &fakeTransport{}
	notifier := &fakeNotifier{}
	memberships := newAccessMemberships()
	svc := NewService(repo, memberships, repo, repo, repo, nil, nil, transport, notifier, accessSettings())
	f := &queueFixture{notifier: notifier, clock: time.Now().UTC()}
	svc.now = func() time.Time { return f.clock }
	f.accessFixture = seedAccessFixture(t, svc, repo, transport, memberships)
	return f
}

// sendTeam sends one message from team@ as B and returns it with the error.
func (f *queueFixture) sendTeam(t *testing.T, subject string) (*domain.MailMessage, error) {
	t.Helper()
	message, _, err := f.service.SendMessage(context.Background(), f.b, f.team.ID,
		SendInput{To: []string{"bob@example.com"}, Subject: subject, Text: "body"})
	return message, err
}

func (f *queueFixture) mustSend(t *testing.T, subject string, wantQueued bool) *domain.MailMessage {
	t.Helper()
	message, err := f.sendTeam(t, subject)
	if err != nil {
		t.Fatalf("send %q: %v", subject, err)
	}
	if queued := message.QueuedReason == domain.MailQueuedForSendLimit; queued != wantQueued {
		t.Fatalf("send %q queued = %v; want %v", subject, queued, wantQueued)
	}
	return message
}

func (f *queueFixture) dispatch(t *testing.T, limit int) int {
	t.Helper()
	processed, err := f.service.DispatchQueuedSends(context.Background(), limit)
	if err != nil {
		t.Fatalf("DispatchQueuedSends: %v", err)
	}
	return processed
}

func (f *queueFixture) stored(t *testing.T, id uint) MailMessagePO {
	t.Helper()
	var po MailMessagePO
	if err := f.repo.(*repository).db.First(&po, id).Error; err != nil {
		t.Fatalf("load message %d: %v", id, err)
	}
	return po
}

func subjectsSent(f *queueFixture, from int) []string {
	var subjects []string
	for _, mail := range f.transport.sent[from:] {
		subjects = append(subjects, mail.Subject)
	}
	return subjects
}

func TestDispatchQueuedSendsWithinRoom(t *testing.T) {
	f := newQueueFixture(t)
	start := f.clock
	for _, subject := range []string{"a1", "a2", "a3"} {
		f.mustSend(t, subject, false)
	}
	f.clock = start.Add(12 * time.Hour)
	for _, subject := range []string{"b1", "b2"} {
		f.mustSend(t, subject, false)
	}
	var waiting []*domain.MailMessage
	for _, subject := range []string{"w1", "w2", "w3", "w4", "w5"} {
		waiting = append(waiting, f.mustSend(t, subject, true))
	}
	if got := f.dispatch(t, 25); got != 0 {
		t.Fatalf("dispatch with a full window processed %d; want 0", got)
	}

	// The three attempts at the start leave the window: room for three.
	f.clock = start.Add(24*time.Hour + time.Second)
	before := len(f.transport.sent)
	if got := f.dispatch(t, 25); got != 3 {
		t.Fatalf("dispatch processed %d; want the 3 the window has room for", got)
	}
	if got := subjectsSent(f, before); len(got) != 3 || got[0] != "w1" || got[1] != "w2" || got[2] != "w3" {
		t.Fatalf("sent %v; want the oldest waiting messages w1 w2 w3 in order", got)
	}
	first := f.stored(t, waiting[0].ID)
	if first.Status != string(domain.MailStatusSent) || first.QueuedReason != nil || first.SendAttemptedAt == nil ||
		!first.SendAttemptedAt.Equal(f.clock) {
		t.Fatalf("w1 = status %s, reason %v, attempted %v; want sent at %v", first.Status, first.QueuedReason, first.SendAttemptedAt, f.clock)
	}
	if still := f.stored(t, waiting[3].ID); still.QueuedReason == nil || still.Status != string(domain.MailStatusQueued) {
		t.Fatalf("w4 = %+v; want still waiting", still)
	}
}

func TestDispatchQueuedSendsHonorsTheBatchLimit(t *testing.T) {
	f := newQueueFixture(t)
	start := f.clock
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	f.mustSend(t, "w1", true)
	f.mustSend(t, "w2", true)
	f.clock = start.Add(25 * time.Hour)
	if got := f.dispatch(t, 1); got != 1 {
		t.Fatalf("dispatch(1) processed %d; want 1", got)
	}
	if got := f.dispatch(t, 1); got != 1 {
		t.Fatalf("second dispatch(1) processed %d; want 1", got)
	}
	if got := f.dispatch(t, 1); got != 0 {
		t.Fatalf("third dispatch processed %d; want nothing left", got)
	}
}

func TestNewSendQueuesBehindWaitingMessages(t *testing.T) {
	f := newQueueFixture(t)
	start := f.clock
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	f.mustSend(t, "w1", true)
	// The window has room again, but w1 is older: the new send waits behind it.
	f.clock = start.Add(25 * time.Hour)
	f.mustSend(t, "later", true)
	before := len(f.transport.sent)
	f.dispatch(t, 25)
	if got := subjectsSent(f, before); len(got) != 2 || got[0] != "w1" || got[1] != "later" {
		t.Fatalf("sent %v; want w1 then later", got)
	}
}

func TestDispatchLeaseKeepsAMessageToOneWorker(t *testing.T) {
	f := newQueueFixture(t)
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	waiting := f.mustSend(t, "w1", true)
	repo := f.repo.(*repository)
	now := f.clock
	first, err := repo.LeaseWaitingSends(context.Background(), accessOrg, now, now.Add(time.Minute), now.Add(-sendLimitWindow), 100, 10)
	if err != nil || len(first) != 1 || first[0].ID != waiting.ID {
		t.Fatalf("first lease = %v, %v; want w1", first, err)
	}
	second, err := repo.LeaseWaitingSends(context.Background(), accessOrg, now, now.Add(time.Minute), now.Add(-sendLimitWindow), 100, 10)
	if err != nil || len(second) != 0 {
		t.Fatalf("second lease = %v, %v; want nothing while the first lease holds", second, err)
	}
	expired, err := repo.LeaseWaitingSends(context.Background(), accessOrg, now.Add(2*time.Minute), now.Add(3*time.Minute), now.Add(-sendLimitWindow), 100, 10)
	if err != nil || len(expired) != 1 {
		t.Fatalf("lease after expiry = %v, %v; want w1 again", expired, err)
	}
}

func TestDeletedWaitingMessageIsNeverSent(t *testing.T) {
	f := newQueueFixture(t)
	start := f.clock
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	waiting := f.mustSend(t, "w1", true)
	if err := f.service.DeleteMessage(context.Background(), f.b, f.team.ID, waiting.ID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	f.clock = start.Add(25 * time.Hour)
	before := len(f.transport.sent)
	if got := f.dispatch(t, 25); got != 0 || len(f.transport.sent) != before {
		t.Fatalf("dispatch processed %d and sent %d; want nothing", got, len(f.transport.sent)-before)
	}
}

func TestWaitingMessagesOfADeletedMailboxAreNeverSent(t *testing.T) {
	f := newQueueFixture(t)
	start := f.clock
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	waiting := f.mustSend(t, "w1", true)
	if err := f.service.DeleteMailbox(context.Background(), f.admin, f.team.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	f.clock = start.Add(25 * time.Hour)
	before := len(f.transport.sent)
	f.dispatch(t, 25)
	if len(f.transport.sent) != before {
		t.Fatalf("a deleted mailbox's waiting message reached the provider")
	}
	// Deleting a mailbox deletes its messages, the waiting one included.
	var remaining int64
	f.repo.(*repository).db.Model(&MailMessagePO{}).Where("id = ?", waiting.ID).Count(&remaining)
	if remaining != 0 {
		t.Fatalf("the deleted mailbox's waiting message is still stored")
	}
}

func TestHandoverCancelsWaitingSends(t *testing.T) {
	f := newQueueFixture(t)
	f.service.now = time.Now
	ctx := context.Background()
	input := SendInput{To: []string{"bob@example.com"}, Subject: "s", Text: "body"}
	for i := 0; i < 5; i++ {
		if _, _, err := f.service.SendMessage(ctx, f.b, f.personal.ID, input); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	input.Subject = "waiting"
	waiting, _, err := f.service.SendMessage(ctx, f.b, f.personal.ID, input)
	if err != nil || waiting.QueuedReason != domain.MailQueuedForSendLimit {
		t.Fatalf("waiting = %+v, %v", waiting, err)
	}
	f.leave(t, f.b, tick())
	f.handOver(t, f.personal.ID)

	if stored := f.stored(t, waiting.ID); stored.Status != string(domain.MailStatusFailed) ||
		stored.FailureCode != failureSendCanceled || stored.QueuedReason != nil {
		t.Fatalf("stored = status %s, code %s, reason %v; want canceled by the handover", stored.Status, stored.FailureCode, stored.QueuedReason)
	}
}

func TestDispatchPublishesSentWebhook(t *testing.T) {
	f := newQueueFixture(t)
	if _, err := f.service.SetMailboxWebhooks(context.Background(), f.admin, f.team.ID, true); err != nil {
		t.Fatalf("SetMailboxWebhooks: %v", err)
	}
	start := f.clock
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	waiting := f.mustSend(t, "w1", true)
	for _, notification := range f.notifier.sent {
		if notification.Message != nil && notification.Message.ID == waiting.ID {
			t.Fatalf("queuing published %s; want nothing until the message is sent", notification.Type)
		}
	}
	f.clock = start.Add(25 * time.Hour)
	f.dispatch(t, 25)
	for _, notification := range f.notifier.sent {
		if notification.Type == WebhookMailSent && notification.Message != nil && notification.Message.ID == waiting.ID {
			return
		}
	}
	t.Fatalf("no %s notification for the queued message after dispatch", WebhookMailSent)
}

// fillWindowWithRoomFor sends five messages so that, at the returned time, the window has room
// for exactly room of them (room <= 3), then queues waiting messages w1..w5.
func (f *queueFixture) fillWindowWithRoomFor(t *testing.T) time.Time {
	t.Helper()
	start := f.clock
	for i := 0; i < 3; i++ {
		f.mustSend(t, "early", false)
	}
	f.clock = start.Add(12 * time.Hour)
	for i := 0; i < 2; i++ {
		f.mustSend(t, "late", false)
	}
	for _, subject := range []string{"w1", "w2", "w3", "w4", "w5"} {
		f.mustSend(t, subject, true)
	}
	// The three early attempts leave the window: room for three.
	return start.Add(24*time.Hour + time.Second)
}

// Review Focus 3: a worker that leased messages but has not started them yet already holds that
// part of the room, so a second worker must not lease past the limit.
func TestAnotherWorkersLeaseHoldsTheRoom(t *testing.T) {
	f := newQueueFixture(t)
	f.clock = f.fillWindowWithRoomFor(t)
	repo := f.repo.(*repository)
	held, err := repo.LeaseWaitingSends(context.Background(), accessOrg, f.clock, f.clock.Add(time.Minute),
		f.clock.Add(-sendLimitWindow), 5, 25)
	if err != nil || len(held) != 3 {
		t.Fatalf("first worker leased %d, %v; want the 3 the window has room for", len(held), err)
	}
	before := len(f.transport.sent)
	if got := f.dispatch(t, 25); got != 0 || len(f.transport.sent) != before {
		t.Fatalf("second worker processed %d and sent %d; want nothing while the first worker holds the room",
			got, len(f.transport.sent)-before)
	}
}

// failingAttachments is the PostgreSQL repository with attachment content that cannot be loaded.
type failingAttachments struct {
	domain.MailboxRepository
}

func (failingAttachments) FindMessageAttachment(context.Context, uint, uint) (*domain.MailAttachment, error) {
	return nil, errors.New("storage unavailable")
}

// A waiting message whose attachments cannot be loaded stays waiting for the next tick instead
// of being left started but never sent, and does not stop the rest of the batch.
func TestUnloadableAttachmentsLeaveTheMessageWaiting(t *testing.T) {
	f := newQueueFixture(t)
	start := f.clock
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	withFile, _, err := f.service.SendMessage(context.Background(), f.b, f.team.ID, SendInput{
		To: []string{"bob@example.com"}, Subject: "with-file", Text: "body",
		Attachments: []AttachmentInput{{Filename: "a.txt", ContentType: "text/plain", Content: "aGVsbG8="}},
	})
	if err != nil || withFile.QueuedReason != domain.MailQueuedForSendLimit {
		t.Fatalf("with-file = %+v, %v; want queued", withFile, err)
	}
	plain := f.mustSend(t, "plain", true)
	f.service.repo = failingAttachments{f.service.repo}
	f.clock = start.Add(25 * time.Hour)
	if _, err := f.service.DispatchQueuedSends(context.Background(), 25); err != nil {
		t.Fatalf("DispatchQueuedSends: %v; want the unloadable message skipped", err)
	}
	if stored := f.stored(t, withFile.ID); stored.QueuedReason == nil || stored.SendAttemptedAt != nil {
		t.Fatalf("with-file = reason %v, attempted %v; want still waiting", stored.QueuedReason, stored.SendAttemptedAt)
	}
	if stored := f.stored(t, plain.ID); stored.Status != string(domain.MailStatusSent) {
		t.Fatalf("plain = %s; want sent despite the unloadable message before it", stored.Status)
	}
}

// The organization's email log shows why a message waits, so the web can label it.
func TestMailLogShowsWhyAMessageWaits(t *testing.T) {
	f := newQueueFixture(t)
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	waiting := f.mustSend(t, "w1", true)
	entries, _, err := f.service.ListLog(context.Background(), f.b, domain.MailLogFilter{})
	if err != nil {
		t.Fatalf("ListLog: %v", err)
	}
	for _, entry := range entries {
		if entry.ID == waiting.ID {
			if entry.QueuedReason != domain.MailQueuedForSendLimit {
				t.Fatalf("log entry queued_reason = %q; want %q", entry.QueuedReason, domain.MailQueuedForSendLimit)
			}
			return
		}
	}
	t.Fatalf("the waiting message is not in the log")
}

func TestQueuedSendUsesStoredRFCMessageID(t *testing.T) {
	f := newQueueFixture(t)
	start := f.clock
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	parent := f.receiveReply(t, f.team, "parent@sender.test", "References: <root@sender.test>")
	// A queued reply keeps the References chain built when it was stored.
	waiting, _, err := f.service.SendMessage(context.Background(), f.b, f.team.ID, SendInput{
		To: []string{"alice@sender.test"}, Subject: "Re: Hello", Text: "body", InReplyTo: "parent@sender.test",
	})
	if err != nil || waiting.QueuedReason != domain.MailQueuedForSendLimit {
		t.Fatalf("queued reply = %+v, %v; want a waiting message", waiting, err)
	}
	stored := f.stored(t, waiting.ID)
	if stored.RFCMessageID == "" || stored.RFCMessageID != waiting.RFCMessageID {
		t.Fatalf("waiting rfc_message_id = stored %q, returned %q; want one id stored at queue time", stored.RFCMessageID, waiting.RFCMessageID)
	}
	if stored.ThreadID == nil || *stored.ThreadID != parent.ThreadID {
		t.Fatalf("waiting thread_id = %v; want the parent's thread %d", stored.ThreadID, parent.ThreadID)
	}

	f.clock = start.Add(25 * time.Hour)
	before := len(f.transport.sent)
	if got := f.dispatch(t, 25); got != 1 {
		t.Fatalf("dispatch processed %d; want 1", got)
	}
	sent := f.transport.sent[before:]
	if len(sent) != 1 || sent[0].MessageID != stored.RFCMessageID {
		t.Fatalf("worker sent %+v; want Message-ID %q", sent, stored.RFCMessageID)
	}
	if got := strings.Join(sent[0].References, " "); got != "root@sender.test parent@sender.test" || sent[0].InReplyTo != "parent@sender.test" {
		t.Fatalf("worker sent References %q, In-Reply-To %q; want the stored chain", got, sent[0].InReplyTo)
	}
	if after := f.stored(t, waiting.ID); after.RFCMessageID != stored.RFCMessageID {
		t.Fatalf("rfc_message_id after send = %q; want %q", after.RFCMessageID, stored.RFCMessageID)
	}
}
