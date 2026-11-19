package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
	"github.com/fastagi/zmail/api/internal/modules/webhook"
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
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailSuppressed}) {
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

// A missing transport never reached the provider: the message fails with the code the personal
// send path records for it, not as an unknown outcome.
func TestNotConfiguredTransportFailsDeterministically(t *testing.T) {
	f := newTxFixture(t, nil)
	transport := &lockedTransport{errs: []error{mailtransport.ErrNotConfigured}}
	f.service.transport = transport
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	f.clock = f.clock.Add(time.Hour)
	f.dispatch(t)
	if transport.calls() != 1 {
		t.Fatalf("transport calls = %d; want 1", transport.calls())
	}
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusFailed, domain.TransactionalFailureNotConfigured)
	if domain.TransactionalFailureNotConfigured != failureNotConfigured {
		t.Fatalf("transactional code %q differs from the personal path's %q", domain.TransactionalFailureNotConfigured, failureNotConfigured)
	}
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailFailed}) {
		t.Fatalf("notifications = %v", got)
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

// afterFailUnknown runs a hook right after the worker's unknown-outcome check.
type afterFailUnknown struct {
	domain.TransactionalMailRepository
	hook func()
}

func (r afterFailUnknown) FailUnknownTransactional(ctx context.Context, now time.Time, limit int) ([]*domain.TransactionalMessage, error) {
	failed, err := r.TransactionalMailRepository.FailUnknownTransactional(ctx, now, limit)
	r.hook()
	return failed, err
}

// A lease that runs out between the worker's unknown-outcome check and its leasing must not be
// leased again: the provider may have accepted the message (D33).
func TestLeaseLostAfterUnknownCheckIsNotResent(t *testing.T) {
	f := newTxFixture(t, nil)
	f.hold()
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	leaseUntil := f.clock.Add(domain.TransactionalLease)
	if leased, err := f.repo.LeaseTransactional(context.Background(), 10, []uint{message.ID}, f.clock, leaseUntil,
		f.clock.Add(-sendLimitWindow), 100, 1); err != nil || len(leased) != 1 {
		t.Fatalf("lease = %v, %v", leased, err)
	}
	if started, err := f.repo.StartTransactionalAttempt(context.Background(), message.ID, leaseUntil, f.clock); err != nil || !started {
		t.Fatalf("start = %v, %v", started, err)
	}
	// The worker checks while the lease still holds; the lease runs out right after.
	f.clock = leaseUntil.Add(-time.Second)
	f.service.transactional = afterFailUnknown{TransactionalMailRepository: f.repo, hook: func() {
		f.clock = leaseUntil.Add(time.Second)
	}}
	f.dispatch(t)
	if len(f.transport.sent) != 0 || f.row(t, message.ID).Status != "queued" {
		t.Fatalf("calls %d, row %+v; want nothing sent and the message left for the next check", len(f.transport.sent), f.row(t, message.ID))
	}
	// Another dispatcher leasing after the lease ran out does not take it either.
	f.service.transactional = f.repo
	if processed, err := f.service.dispatchTransactionalOrganization(context.Background(), 10, nil, f.clock, 25); err != nil || processed != 0 {
		t.Fatalf("dispatchTransactionalOrganization = %d, %v; want nothing leased", processed, err)
	}
	f.dispatch(t)
	if len(f.transport.sent) != 0 {
		t.Fatalf("provider calls = %d; a possibly sent message was resent", len(f.transport.sent))
	}
	assertFinal(t, f.row(t, message.ID), domain.TransactionalStatusFailed, domain.TransactionalFailureUnknownOutcome)
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
	if got := f.notificationTypes(); !equalStrings(got, []string{WebhookEmailSuppressed}) {
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
			[]string{WebhookEmailSuppressed}, ""},
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

// webhookPublisherOn is the real webhook starter over the fixture's database, with the product
// catalog, so publication runs the production validation and deduplication.
func webhookPublisherOn(t *testing.T, db *gorm.DB) (domain.WebhookPublisher, *config.Config) {
	t.Helper()
	cfg := &config.Config{
		Starters: config.StarterConfig{Optional: []string{config.StarterWebhook}},
		Webhook: config.WebhookConfig{
			EncryptionKey: "webhook-test-encryption-key-at-least-32-bytes", RequestTimeout: time.Second,
			MaxResponseBytes: 1024, SecretOverlap: 24 * time.Hour, EventRetention: 30 * 24 * time.Hour,
		},
	}
	catalog, err := NewWebhookCatalog()
	if err != nil {
		t.Fatalf("NewWebhookCatalog: %v", err)
	}
	secrets, err := webhook.NewSecretProtector(cfg)
	if err != nil {
		t.Fatalf("NewSecretProtector: %v", err)
	}
	targets := webhook.NewTargetPolicy(cfg)
	return webhook.NewService(catalog, webhook.NewRepository(db), secrets, targets, webhook.NewSender(cfg, targets), cfg), cfg
}

func TestDuplicatePublishDeduplicated(t *testing.T) {
	f := newTxFixture(t, nil)
	publisher, cfg := webhookPublisherOn(t, f.db)
	notifier := NewWebhookNotifier(cfg, publisher)
	message := secretMessage()
	message.ID = 501
	notification := TransactionalNotification{
		Type: WebhookEmailDelivered, Message: message, Reason: "delivered", Key: "event:prov-dup",
		OccurredAt: f.clock.Add(-time.Minute),
	}
	for i := 0; i < 2; i++ {
		if err := notifier.NotifyTransactional(context.Background(), notification); err != nil {
			t.Fatalf("NotifyTransactional #%d: %v", i+1, err)
		}
	}
	var rows []struct {
		EventID   string
		EventType string
	}
	if err := f.db.Raw(`SELECT event_id, event_type FROM webhook_events WHERE organization_id = 10 AND source = 'mailbox'`).
		Scan(&rows).Error; err != nil {
		t.Fatalf("read webhook_events: %v", err)
	}
	if len(rows) != 1 || rows[0].EventType != WebhookEmailDelivered ||
		rows[0].EventID != transactionalWebhookEventID(501, WebhookEmailDelivered, "event:prov-dup") {
		t.Fatalf("webhook_events = %+v; want one email.delivered row", rows)
	}
}

// A simulated bounce goes through the real notifier and webhook starter: the receiver sees a
// permanent bounce of a simulated live message, with the full address and none of its content.
func TestSimulatedBouncePublishesPermanentBounce(t *testing.T) {
	f := newTxFixture(t, nil)
	publisher, cfg := webhookPublisherOn(t, f.db)
	f.service.notifier = NewWebhookNotifier(cfg, publisher)
	message, _ := f.send(t, liveActor(), codeInput("k1", " Bounced+u1@TEST.zmail.im ", "482913"))
	if len(f.transport.sent) != 0 {
		t.Fatalf("provider calls = %d", len(f.transport.sent))
	}
	var rows []struct {
		EventID     string
		EventType   string
		PayloadJSON string `gorm:"column:payload_json"`
	}
	if err := f.db.Raw(`SELECT event_id, event_type, payload_json FROM webhook_events WHERE organization_id = 10 ORDER BY id`).
		Scan(&rows).Error; err != nil {
		t.Fatalf("read webhook_events: %v", err)
	}
	if len(rows) != 2 || rows[0].EventType != WebhookEmailSent || rows[1].EventType != WebhookEmailBounced {
		t.Fatalf("webhook_events = %+v; want email.sent then email.bounced", rows)
	}
	for _, row := range rows {
		if strings.Contains(row.PayloadJSON, "482913") || strings.Contains(row.PayloadJSON, "verification") {
			t.Fatalf("payload leaks content: %s", row.PayloadJSON)
		}
	}
	var bounce transactionalWebhookPayload
	if err := json.Unmarshal([]byte(rows[1].PayloadJSON), &bounce); err != nil {
		t.Fatalf("payload: %v", err)
	}
	want := transactionalWebhookPayload{
		MessageID: message.ID, Template: "signup-code", To: "bounced+u1@test.zmail.im", Mode: "live",
		Simulated: true, BounceType: bounceTypePermanent, Reason: "simulated",
	}
	if bounce != want {
		t.Fatalf("bounce payload = %+v; want %+v", bounce, want)
	}
	if !strings.HasPrefix(rows[1].EventID, "t"+itoa(message.ID)+":bounced:") {
		t.Fatalf("bounce event id = %q", rows[1].EventID)
	}
}
