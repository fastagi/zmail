package mailbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// lockedTransport is a provider double that several dispatchers may call at once. It answers
// each call with the next scripted error, then with acceptance.
type lockedTransport struct {
	mu     sync.Mutex
	sent   []mailtransport.Message
	errs   []error
	result mailtransport.Result
}

func (t *lockedTransport) Send(_ context.Context, mail mailtransport.Message) (mailtransport.Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent = append(t.sent, mail)
	if len(t.errs) > 0 {
		err := t.errs[0]
		t.errs = t.errs[1:]
		return mailtransport.Result{}, err
	}
	return t.result, nil
}

func (t *lockedTransport) calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sent)
}

// hold stops the fast dispatch after a send, so the test drives dispatch itself.
func (f *txFixture) hold() {
	f.service.background = func(func()) {}
}

func (f *txFixture) dispatch(t *testing.T) int {
	t.Helper()
	processed, err := f.service.DispatchTransactional(context.Background(), 25)
	if err != nil {
		t.Fatalf("DispatchTransactional: %v", err)
	}
	return processed
}

func (f *txFixture) events(t *testing.T, id uint) []TransactionalMessageEventPO {
	t.Helper()
	var events []TransactionalMessageEventPO
	if err := f.db.Where("message_id = ?", id).Order("id").Find(&events).Error; err != nil {
		t.Fatalf("read events: %v", err)
	}
	return events
}

func (f *txFixture) suppressionRows(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := f.db.Raw(`SELECT count(*) FROM mail_suppressions`).Scan(&count).Error; err != nil {
		t.Fatalf("count suppressions: %v", err)
	}
	return count
}

func (f *txFixture) notificationTypes() []string {
	types := make([]string, 0, len(f.notifier.transactional))
	for _, notification := range f.notifier.transactional {
		types = append(types, notification.Type)
	}
	return types
}

func assertFinal(t *testing.T, row TransactionalMessagePO, status domain.TransactionalStatus, failureCode string) {
	t.Helper()
	if row.Status != string(status) || row.FailureCode != failureCode {
		t.Fatalf("status %s / %q; want %s / %q", row.Status, row.FailureCode, status, failureCode)
	}
	if row.PayloadCiphertext != nil || row.LeaseUntil != nil || row.QueuedReason != nil || row.NextAttemptAt != nil {
		t.Fatalf("a final message keeps queue state: %+v", row)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestDispatchSendsOnceUnderConcurrentDispatchers(t *testing.T) {
	f := newTxFixture(t, nil)
	f.hold()
	transport := &lockedTransport{result: mailtransport.Result{MessageID: "cf-1"}}
	f.service.transport = transport
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))

	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- f.service.DispatchTransactionalMessage(context.Background(), message.ID)
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("DispatchTransactionalMessage: %v", err)
		}
	}
	if transport.calls() != 1 {
		t.Fatalf("provider calls = %d; want 1", transport.calls())
	}
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusSent, "")
}

func TestAcceptedWipesCiphertext(t *testing.T) {
	f := newTxFixture(t, nil)
	f.transport.result = mailtransport.Result{MessageID: "cf-1", Delivered: []string{"LiLei@example.com"}}
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))

	if len(f.transport.sent) != 1 {
		t.Fatalf("provider calls = %d; want the fast dispatch's one", len(f.transport.sent))
	}
	mail := f.transport.sent[0]
	if mail.MessageID != message.RFCMessageID || mail.Subject != "Acme verification code: 482913" ||
		len(mail.To) != 1 || mail.To[0] != "lilei@example.com" || mail.FromAddress != "no-reply@mail.example" ||
		mail.FromName != "Acme" || len(mail.Headers) != 0 || mail.InReplyTo != "" {
		t.Fatalf("provider message = %+v", mail)
	}
	row := f.row(t, message.ID)
	assertFinal(t, row, domain.TransactionalStatusSent, "")
	if row.ProviderMessageID == nil || *row.ProviderMessageID != "cf-1" || row.SentAt == nil || row.Attempts != 1 ||
		row.SendAttemptedAt == nil || row.LastEvent == nil || *row.LastEvent != string(domain.MailEventDelivered) {
		t.Fatalf("sent row = %+v", row)
	}
	events := f.events(t, message.ID)
	if len(events) != 2 || events[0].Type != "accepted" || events[1].Type != "delivered" {
		t.Fatalf("events = %+v", events)
	}
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailSent, WebhookEmailDelivered}) {
		t.Fatalf("notifications = %v", got)
	}
	// A second dispatch finds nothing to do.
	if processed := f.dispatch(t); processed != 0 || len(f.transport.sent) != 1 {
		t.Fatalf("second dispatch processed %d, calls %d", processed, len(f.transport.sent))
	}
}

func TestRetryOn503ThenFail(t *testing.T) {
	f := newTxFixture(t, nil)
	unavailable := &mailtransport.Error{Code: "provider.http_503", Responded: true}
	transport := &lockedTransport{errs: []error{unavailable, unavailable, unavailable, unavailable}}
	f.service.transport = transport
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))

	for i, delay := range []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute} {
		row := f.row(t, message.ID)
		if row.Status != "queued" || row.QueuedReason == nil || *row.QueuedReason != "retry" || row.Attempts != i+1 ||
			row.NextAttemptAt == nil || !row.NextAttemptAt.Equal(f.clock.Add(delay)) || row.LeaseUntil != nil ||
			row.FailureCode != "provider.http_503" || row.PayloadCiphertext == nil {
			t.Fatalf("after attempt %d: %+v; want retry in %s", i+1, row, delay)
		}
		// Not due yet: nothing happens.
		f.clock = f.clock.Add(delay - time.Second)
		f.dispatch(t)
		if transport.calls() != i+1 {
			t.Fatalf("a retry ran before its time: %d calls", transport.calls())
		}
		f.clock = f.clock.Add(time.Second)
		f.dispatch(t)
	}
	if transport.calls() != 4 {
		t.Fatalf("provider calls = %d; want 4", transport.calls())
	}
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusFailed, "provider.http_503")
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailFailed}) {
		t.Fatalf("notifications = %v; want only the final failure", got)
	}
}

func TestPermanentRefusalFails(t *testing.T) {
	f := newTxFixture(t, nil)
	f.service.transport = &lockedTransport{errs: []error{&mailtransport.Error{Code: "provider.http_400", Permanent: true, Responded: true}}}
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusFailed, "provider.http_400")
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailFailed}) || f.notifier.transactional[0].Reason != "provider.http_400" {
		t.Fatalf("notifications = %+v", f.notifier.transactional)
	}
}

func TestProviderSuppressedRecipientIsNotListed(t *testing.T) {
	f := newTxFixture(t, nil)
	f.transport.result = mailtransport.Result{MessageID: "cf-1", Suppressed: []string{"lilei@example.com"}}
	f.transport.err = &mailtransport.Error{Code: "transport.suppressed", Permanent: true, Responded: true}
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusSuppressed, "")
	if got := f.notificationTypes(); !equalStrings(got, []string{TransactionalSuppressedEvent}) {
		t.Fatalf("notifications = %v", got)
	}
	if f.suppressionRows(t) != 0 {
		t.Fatal("a provider suppression wrote mail_suppressions (D35)")
	}
}

func TestNoRetryWithoutHTTPStatus(t *testing.T) {
	for name, sendErr := range map[string]error{
		"unreachable":        &mailtransport.Error{Code: "transport.unreachable"},
		"invalid_response":   &mailtransport.Error{Code: "transport.invalid_response"},
		"response_too_large": &mailtransport.Error{Code: "transport.response_too_large"},
		"other error":        errors.New("connection reset"),
	} {
		t.Run(name, func(t *testing.T) {
			f := newTxFixture(t, nil)
			transport := &lockedTransport{errs: []error{sendErr, sendErr}}
			f.service.transport = transport
			message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
			f.clock = f.clock.Add(time.Hour)
			f.dispatch(t)
			if transport.calls() != 1 {
				t.Fatalf("provider calls = %d; want 1", transport.calls())
			}
			assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusFailed, domain.TransactionalFailureUnknownOutcome)
			if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailFailed}) {
				t.Fatalf("notifications = %v", got)
			}
		})
	}
}

func TestCrashAfterProviderCallIsNotResent(t *testing.T) {
	f := newTxFixture(t, nil)
	f.hold()
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	// A dispatcher leased the message and started its provider call, then died.
	leaseUntil := f.clock.Add(domain.TransactionalLease)
	leased, err := f.repo.LeaseTransactional(context.Background(), 10, []uint{message.ID}, f.clock, leaseUntil,
		f.clock.Add(-sendLimitWindow), 100, 1)
	if err != nil || len(leased) != 1 {
		t.Fatalf("lease = %v, %v", leased, err)
	}
	if started, err := f.repo.StartTransactionalAttempt(context.Background(), message.ID, leaseUntil, f.clock); err != nil || !started {
		t.Fatalf("start = %v, %v", started, err)
	}

	f.clock = f.clock.Add(time.Minute)
	f.dispatch(t)
	if row := f.row(t, message.ID); row.Status != "queued" {
		t.Fatalf("a held lease was failed early: %+v", row)
	}
	f.clock = f.clock.Add(2 * time.Minute)
	f.dispatch(t)
	if len(f.transport.sent) != 0 {
		t.Fatalf("provider calls = %d; a possibly sent message was resent", len(f.transport.sent))
	}
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusFailed, domain.TransactionalFailureUnknownOutcome)
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailFailed}) {
		t.Fatalf("notifications = %v", got)
	}
}

func TestQueuedOverOneHourExpires(t *testing.T) {
	f := newTxFixture(t, nil)
	f.hold()
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	f.clock = f.clock.Add(time.Hour + time.Second)
	if processed := f.dispatch(t); processed != 1 {
		t.Fatalf("processed = %d", processed)
	}
	if len(f.transport.sent) != 0 {
		t.Fatalf("provider calls = %d", len(f.transport.sent))
	}
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusFailed, domain.TransactionalFailureExpired)
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailFailed}) || f.notifier.transactional[0].Reason != "send.expired" {
		t.Fatalf("notifications = %+v", f.notifier.transactional)
	}
}

// Review Focus 5: the sender domain lost its verification while the message waited.
func TestSenderUnverifiedBeforeDispatch(t *testing.T) {
	f := newTxFixture(t, func(settings *Settings) { settings.Provider = "cloudflare" })
	f.hold()
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	if err := f.db.Exec(`UPDATE mail_domains SET status = 'pending' WHERE name = 'mail.example'`).Error; err != nil {
		t.Fatalf("unverify: %v", err)
	}
	f.dispatch(t)
	if len(f.transport.sent) != 0 {
		t.Fatalf("provider calls = %d; want 0", len(f.transport.sent))
	}
	row := f.row(t, message.ID)
	assertFinal(t, row, domain.TransactionalStatusFailed, "send.sender_unverified")
	if row.PayloadCiphertext != nil {
		t.Fatal("ciphertext kept")
	}
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailFailed}) {
		t.Fatalf("notifications = %v", got)
	}
}

func TestUndecryptablePayloadFails(t *testing.T) {
	f := newTxFixture(t, nil)
	f.hold()
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	if err := f.db.Exec(`UPDATE transactional_messages SET payload_ciphertext = '\x0102' WHERE id = ?`, message.ID).Error; err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	f.dispatch(t)
	if len(f.transport.sent) != 0 {
		t.Fatalf("provider calls = %d", len(f.transport.sent))
	}
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusFailed, "send.payload_unavailable")
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailFailed}) {
		t.Fatalf("notifications = %v", got)
	}
}

func TestRecipientSuppressedAfterEnqueue(t *testing.T) {
	f := newTxFixture(t, nil)
	f.hold()
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	if err := f.repo.AddSuppression(context.Background(), domain.MailSuppression{
		OrganizationID: 10, Email: "lilei@example.com", Reason: "bounced", Source: "broadcast", CreatedAt: f.clock,
	}); err != nil {
		t.Fatalf("suppress: %v", err)
	}
	f.dispatch(t)
	if len(f.transport.sent) != 0 {
		t.Fatalf("provider calls = %d", len(f.transport.sent))
	}
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusSuppressed, "")
	if got := f.notificationTypes(); !equalStrings(got, []string{TransactionalSuppressedEvent}) {
		t.Fatalf("notifications = %v", got)
	}
}

func TestSimulatorOutcomes(t *testing.T) {
	for _, test := range []struct {
		to            string
		actor         TransactionalActor
		status        domain.TransactionalStatus
		events        []string
		notifications []string
		bounceType    string
	}{
		{"delivered@test.zmail.im", liveActor(), "sent", []string{"accepted", "delivered"},
			[]string{WebhookEmailSent, WebhookEmailDelivered}, ""},
		{"Bounced+u1@TEST.zmail.im", liveActor(), "sent", []string{"accepted", "bounced"},
			[]string{WebhookEmailSent, WebhookEmailBounced}, "permanent"},
		{"soft-bounced@test.zmail.im", testActor(), "sent", []string{"accepted", "deferred"},
			[]string{WebhookEmailSent, WebhookEmailBounced}, "temporary"},
		{"complained+x@test.zmail.im", liveActor(), "sent", []string{"accepted", "delivered", "complained"},
			[]string{WebhookEmailSent, WebhookEmailDelivered, WebhookEmailComplained}, ""},
		{"SUPPRESSED@test.zmail.im", liveActor(), "suppressed", []string{"rejected"},
			[]string{TransactionalSuppressedEvent}, ""},
		// A test key sending to a normal address simulates delivered.
		{"lilei@example.com", testActor(), "sent", []string{"accepted", "delivered"},
			[]string{WebhookEmailSent, WebhookEmailDelivered}, ""},
	} {
		t.Run(test.to, func(t *testing.T) {
			f := newTxFixture(t, nil)
			message, _ := f.send(t, test.actor, codeInput("k1", test.to, "482913"))
			if len(f.transport.sent) != 0 {
				t.Fatalf("provider calls = %d", len(f.transport.sent))
			}
			row := f.row(t, message.ID)
			assertFinal(t, row, test.status, "")
			if row.SendAttemptedAt != nil || row.Attempts != 0 {
				t.Fatalf("a simulation counted as a provider attempt: %+v", row)
			}
			events := f.events(t, message.ID)
			types := make([]string, 0, len(events))
			for _, event := range events {
				types = append(types, event.Type)
				if event.Detail != "simulated" {
					t.Fatalf("event detail = %q", event.Detail)
				}
			}
			if !equalStrings(types, test.events) {
				t.Fatalf("events = %v; want %v", types, test.events)
			}
			if got := f.notificationTypes(); !equalStrings(got, test.notifications) {
				t.Fatalf("notifications = %v; want %v", got, test.notifications)
			}
			keys := map[string]bool{}
			for _, notification := range f.notifier.transactional {
				if keys[notification.Key] || len(notification.Key) < 5 || notification.Key[:4] != "sim:" {
					t.Fatalf("notification key %q", notification.Key)
				}
				keys[notification.Key] = true
				if notification.Type == WebhookEmailBounced && notification.BounceType != test.bounceType {
					t.Fatalf("bounce type = %q; want %q", notification.BounceType, test.bounceType)
				}
			}
			if f.suppressionRows(t) != 0 {
				t.Fatal("a simulation wrote mail_suppressions")
			}
		})
	}
}

func TestLaneFullMessageWaitsThenSends(t *testing.T) {
	f := newTxFixture(t, func(settings *Settings) { settings.DailyTransactionalLimit = 1 })
	first, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	second, _ := f.send(t, liveActor(), codeInput("k2", "hanmeimei@example.com", "482914"))
	if len(f.transport.sent) != 1 || f.row(t, first.ID).Status != "sent" {
		t.Fatalf("first message not sent: %d calls", len(f.transport.sent))
	}
	row := f.row(t, second.ID)
	if row.Status != "queued" || row.QueuedReason == nil || *row.QueuedReason != "send_limit" {
		t.Fatalf("second = %+v; want waiting for the lane", row)
	}
	f.dispatch(t)
	if len(f.transport.sent) != 1 {
		t.Fatal("the worker sent beyond the lane")
	}
	// The first attempt leaves the rolling window.
	if err := f.db.Exec(`UPDATE transactional_messages SET send_attempted_at = ? WHERE id = ?`,
		f.clock.Add(-sendLimitWindow-time.Minute), first.ID).Error; err != nil {
		t.Fatalf("age attempt: %v", err)
	}
	f.clock = f.clock.Add(time.Minute)
	f.dispatch(t)
	if len(f.transport.sent) != 2 {
		t.Fatalf("provider calls = %d; want the waiting message sent", len(f.transport.sent))
	}
	assertFinal(t, f.row(t, second.ID), domain.TransactionalStatusSent, "")
}

func TestPurgeExpiredTransactional(t *testing.T) {
	f := newTxFixture(t, nil)
	old, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	recent, _ := f.send(t, liveActor(), codeInput("k2", "lilei@example.com", "482914"))
	if err := f.db.Exec(`UPDATE transactional_messages SET created_at = ? WHERE id = ?`,
		f.clock.Add(-domain.TransactionalRetention-time.Minute), old.ID).Error; err != nil {
		t.Fatalf("age: %v", err)
	}
	purged, err := f.service.PurgeExpiredTransactional(context.Background(), f.clock, 10)
	if err != nil || purged != 1 {
		t.Fatalf("purged = %d, %v; want 1", purged, err)
	}
	if len(f.events(t, old.ID)) != 0 {
		t.Fatal("events of a purged message remain")
	}
	if f.row(t, recent.ID).ID != recent.ID {
		t.Fatal("recent message purged")
	}
	if purged, err := f.service.PurgeExpiredTransactional(context.Background(), f.clock, 0); err != nil || purged != 0 {
		t.Fatalf("purge with no batch = %d, %v", purged, err)
	}
}

func TestDisabledFeatureStillDrainsQueue(t *testing.T) {
	f := newTxFixture(t, nil)
	f.hold()
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	f.service.settings.TransactionalEnabled = false
	f.dispatch(t)
	if len(f.transport.sent) != 1 {
		t.Fatalf("provider calls = %d; want the queued message sent", len(f.transport.sent))
	}
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusSent, "")
}

func TestTransactionalRetryDelay(t *testing.T) {
	for attempts, want := range map[int]time.Duration{1: 30 * time.Second, 2: 2 * time.Minute, 3: 10 * time.Minute} {
		if got, ok := transactionalRetryDelay(attempts); !ok || got != want {
			t.Fatalf("transactionalRetryDelay(%d) = %s, %v; want %s", attempts, got, ok, want)
		}
	}
	for _, attempts := range []int{0, 4, 5} {
		if _, ok := transactionalRetryDelay(attempts); ok {
			t.Fatalf("transactionalRetryDelay(%d) allows a retry", attempts)
		}
	}
}
