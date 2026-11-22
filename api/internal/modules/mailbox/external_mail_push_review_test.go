package mailbox

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	infradatabase "github.com/fastagi/zmail/api/internal/infra/database"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// Tests for the findings of the independent review of the push subscriptions (PR #178).

// TestExternalMailPushDuringAPushSyncSyncsAgain: a notification that arrives while a push-triggered
// sync runs, after an earlier one was already pending, is not answered by that sync: it stays
// pending and syncs the connection again, instead of waiting for the slow poll.
func TestExternalMailPushDuringAPushSyncSyncsAgain(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	f.advance(time.Minute)
	f.notifyFake(t) // pending since now; the next sync is a push
	notified := false
	f.service.externalConnectors = fakeExternalMailRegistry(notifyingSource{FakeExternalMailSource: f.source, during: func() {
		if !notified {
			notified = true
			f.advance(time.Second)
			f.notifyFake(t)
		}
	}})
	f.sync(t)
	if runs := f.runs(t); runs[0].Trigger != domain.ExternalMailSyncTriggerPush {
		t.Fatalf("first sync = %q, want push", runs[0].Trigger)
	}
	if subscription := f.subscription(t, f.connection.ID); subscription.PendingAt == nil {
		t.Fatal("the notification received during the push sync was dropped")
	}
	f.advance(config.DefaultExternalMailPushCoalesceWindow)
	f.sync(t)
	if runs := f.runs(t); len(runs) != 3 || runs[0].Trigger != domain.ExternalMailSyncTriggerPush {
		t.Fatalf("runs = %d, newest %q; want another push sync", len(runs), runs[0].Trigger)
	}
}

// TestExternalMailPushJunkCannotStarveTheProvider: requests that authenticate nothing spend only
// their sender's own budget, never the provider's, so a flood of them leaves every authenticated
// notification accepted; past its budget the junk sender gets 429 while other senders do not.
func TestExternalMailPushJunkCannotStarveTheProvider(t *testing.T) {
	f := newPushFixture(t)
	registry := fakeExternalMailRegistry(f.source)
	registry.connectors[0].Push = addressPushSource{f.source}
	f.service.externalConnectors = registry
	f.service.pushLimits = ratelimit.NewMemoryStores().New(ratelimit.Rule{Name: ruleExternalMailPush, Max: 3, Window: time.Minute})
	f.service.pushRefusals = ratelimit.NewMemoryStore(5, time.Minute)
	f.sync(t)
	ctx := context.Background()
	receive := func(provider domain.ExternalMailProvider, ip string, header http.Header, body string) error {
		_, err := f.service.ReceiveExternalMailPush(ctx, provider, ExternalMailPushDelivery{
			Header: header, Query: url.Values{}, Body: []byte(body), ClientIP: ip,
		})
		return err
	}
	junkLimited := 0
	for i := 0; i < 50; i++ {
		gmailErr := receive(domain.ExternalMailProviderGmail, "203.0.113.9", http.Header{}, f.connection.AccountAddress)
		outlookErr := receive(domain.ExternalMailProviderOutlook, "203.0.113.9", http.Header{}, string(fakeNotificationBody("fake-sub-404", "guess", "")))
		for _, err := range []error{gmailErr, outlookErr} {
			if errors.Is(err, ErrExternalMailPushRateLimited) {
				junkLimited++
			} else if err != nil && !errors.Is(err, ErrExternalMailPushUnauthenticated) {
				t.Fatalf("junk answered %v", err)
			}
		}
	}
	if junkLimited < 90 {
		t.Fatalf("the junk sender was limited %d times out of 100, want all but its budget", junkLimited)
	}
	if err := receive(domain.ExternalMailProviderGmail, "203.0.113.200", http.Header{}, f.connection.AccountAddress); !errors.Is(err, ErrExternalMailPushUnauthenticated) {
		t.Fatalf("another sender's first refusal = %v, want 401 rather than the junk sender's 429", err)
	}
	now := f.service.now().UTC()
	if err := receive(domain.ExternalMailProviderGmail, "66.102.0.1", http.Header{"Authorization": {"Bearer good"}}, f.connection.AccountAddress); err != nil {
		t.Fatalf("an authenticated notification after the flood = %v", err)
	}
	f.promote(t)
	if next := f.nextSyncAt(t, f.connection.ID); next.After(now.Add(f.service.settings.ExternalMail.Sync.Spacing())) {
		t.Fatalf("the authenticated notification did not make the connection due: %v", next)
	}
}

// TestExternalMailPushDetectsADeadSubscription: a subscription that claims to be live but delivers
// nothing (its Pub/Sub subscription deleted, or every push refused for a wrong audience) is caught by
// the first poll that stores mail no notification announced: it is degraded, the connection polls
// every two minutes, and owners see it; a notification that gets through restores the slow poll. A
// subscription that stays quiet past the quiet limit falls back to the regular poll too.
func TestExternalMailPushDetectsADeadSubscription(t *testing.T) {
	for name, deliver := range map[string]func(f *externalFixture) error{
		"pubsub subscription deleted": func(*externalFixture) error { return nil },
		"wrong audience": func(f *externalFixture) error {
			_, err := f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, ExternalMailPushDelivery{
				Header: http.Header{"Authorization": {"Bearer token-for-another-audience"}}, Body: []byte(f.connection.AccountAddress),
			})
			if !errors.Is(err, ErrExternalMailPushUnauthenticated) {
				return errors.New("the refused notification was accepted")
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newPushFixture(t)
			registry := fakeExternalMailRegistry(f.source)
			registry.connectors[0].Push = addressPushSource{f.source}
			f.service.externalConnectors = registry
			start := f.service.now().UTC()
			f.sync(t)
			if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(start.Add(config.DefaultExternalMailPushPollInterval)) {
				t.Fatalf("a fresh subscription polls at %v", next)
			}
			f.source.Deliver(externalTestToken, externalMessage("g-missed", "Never announced", start.Add(8*time.Minute)))
			f.advance(8 * time.Minute)
			if err := deliver(f); err != nil {
				t.Fatal(err)
			}
			now := f.advance(7 * time.Minute)
			f.sync(t)
			subscription := f.subscription(t, f.connection.ID)
			if subscription.DegradedAt == nil {
				t.Fatalf("a poll stored unannounced mail and the subscription is %+v", subscription)
			}
			if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now.Add(externalMailSyncInterval)) {
				t.Fatalf("a degraded subscription polls at %v, want two minutes", next)
			}
			connections, err := f.service.ListExternalMailConnections(context.Background(), f.b)
			if err != nil || toExternalMailPushState(connections[0].Push, connections[0].PushStatus).Status != "degraded" {
				t.Fatalf("owners see %+v (%v)", connections[0].PushStatus, err)
			}
			// A notification that gets through shows push works again.
			f.advance(time.Minute)
			if _, err := f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, ExternalMailPushDelivery{
				Header: http.Header{"Authorization": {"Bearer good"}}, Body: []byte(f.connection.AccountAddress),
			}); err != nil {
				t.Fatal(err)
			}
			now = f.advance(f.service.settings.ExternalMail.Push.Coalesce())
			f.sync(t)
			if subscription = f.subscription(t, f.connection.ID); subscription.DegradedAt != nil {
				t.Fatalf("a delivered notification left the subscription degraded: %+v", subscription)
			}
			if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now.Add(config.DefaultExternalMailPushPollInterval)) {
				t.Fatalf("after push works again the poll is at %v", next)
			}
		})
	}
	t.Run("quiet past the limit", func(t *testing.T) {
		f := newPushFixture(t)
		f.service.settings.ExternalMail.Push.QuietLimit = time.Hour
		f.sync(t)
		now := f.advance(time.Hour)
		f.sync(t)
		if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now.Add(externalMailSyncInterval)) {
			t.Fatalf("a subscription quiet past the limit polls at %v, want two minutes", next)
		}
		connections, _ := f.service.ListExternalMailConnections(context.Background(), f.b)
		if got := connections[0].PushStatus; got != pushStatusQuiet {
			t.Fatalf("owners see %q, want quiet", got)
		}
	})
}

// TestExternalMailPushRespectsFetchSpacing: a notification never brings a connection's next fetch
// closer than the fetch spacing after its last one, so a throttled backfill keeps its pace.
func TestExternalMailPushRespectsFetchSpacing(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	spacing := f.service.settings.ExternalMail.Sync.Spacing()
	f.service.settings.ExternalMail.Sync.MaxImmediatePages = 1
	f.source.PageSize = 1
	received := f.service.now().UTC()
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", received), externalMessage("g-2", "Two", received),
		externalMessage("g-3", "Three", received))
	// The page is fetched by the poll, so no coalescing window hides the spacing.
	now := f.advance(config.DefaultExternalMailPushPollInterval)
	f.sync(t)
	if runs := f.runs(t); !runs[0].Throttled || runs[0].Trigger != domain.ExternalMailSyncTriggerPoll {
		t.Fatalf("the backfill page was not throttled: %+v", runs[0])
	}
	f.notifyFake(t)
	if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now.Add(spacing)) {
		t.Fatalf("a notification moved a throttled backfill's next page to %v, want %v", next, now.Add(spacing))
	}
}

// txRecordingSource records whether the provider was called inside an open database transaction.
type txRecordingSource struct {
	*FakeExternalMailSource
	mu     sync.Mutex
	inTx   []string
	calls  atomic.Int32
	active atomic.Int32
	peak   atomic.Int32
}

func (s *txRecordingSource) note(call string, ctx context.Context) {
	s.calls.Add(1)
	if _, bound := infradatabase.TransactionFromContext(ctx); bound {
		s.mu.Lock()
		s.inTx = append(s.inTx, call)
		s.mu.Unlock()
	}
}

func (s *txRecordingSource) Unsubscribe(ctx context.Context, request ExternalMailPushRequest) error {
	s.note("unsubscribe", ctx)
	now := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		peak := s.peak.Load()
		if now <= peak || s.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	return s.FakeExternalMailSource.Unsubscribe(ctx, request)
}

func (s *txRecordingSource) Revoke(ctx context.Context, token ExternalMailToken) error {
	s.note("revoke", ctx)
	return s.FakeExternalMailSource.Revoke(ctx, token)
}

// TestExternalMailPushProviderCallsAfterCommit: deleting a connected mailbox and a member's departure
// stop the subscription in the database within the revocation and call the provider (stop, then
// grant hand-back) only after it committed, never inside a transaction; a departure's connections
// are released in parallel.
func TestExternalMailPushProviderCallsAfterCommit(t *testing.T) {
	ctx := context.Background()
	t.Run("mailbox deleted", func(t *testing.T) {
		tf := newTenantFixture(t)
		migratePushTable(t, tf.externalFixture)
		tf.service.settings.ExternalMail.Push = config.ExternalMailPushConfig{Enabled: true}
		recording := &txRecordingSource{FakeExternalMailSource: tf.source}
		tf.service.externalConnectors = fakeExternalMailRegistry(recording)
		alice := tf.mustAssign(t, personalRow("alice", accessB))[0]
		tf.sync(t)
		id, _, _ := tf.source.PushSubscription(TenantMailboxToken(tenantAddress("alice")))
		if err := tf.service.DeleteMailbox(ctx, tf.admin, alice.MailboxID); err != nil {
			t.Fatalf("DeleteMailbox: %v", err)
		}
		if stopped := tf.source.Unsubscribed(); len(stopped) != 1 || stopped[0] != id {
			t.Fatalf("unsubscribed %v, want %q", stopped, id)
		}
		if subscription := tf.subscription(t, alice.ID); subscription.Status != domain.ExternalMailSubscriptionStopped {
			t.Fatalf("subscription after the deletion = %+v", subscription)
		}
		if len(recording.inTx) != 0 || recording.calls.Load() == 0 {
			t.Fatalf("provider calls %d, inside a transaction: %v", recording.calls.Load(), recording.inTx)
		}
	})
	t.Run("member left", func(t *testing.T) {
		f := newPushFixture(t)
		recording := &txRecordingSource{FakeExternalMailSource: f.source}
		f.service.externalConnectors = fakeExternalMailRegistry(recording)
		for i := 0; i < 4; i++ {
			f.addConnection(t, domain.ExternalMailProviderOutlook, "leaver-"+string(rune('a'+i)))
		}
		f.sync(t)
		if err := f.service.MemberRemoved(ctx, domain.NewOrganizationMemberRemovedEvent(accessOrg, accessB, f.b.MembershipID, f.service.now())); err != nil {
			t.Fatalf("MemberRemoved: %v", err)
		}
		if stopped := f.source.Unsubscribed(); len(stopped) != 5 {
			t.Fatalf("unsubscribed %d subscriptions, want 5", len(stopped))
		}
		if open := f.count(t, `SELECT count(*) FROM external_mail_subscriptions WHERE status <> 'stopped'`); open != 0 {
			t.Fatalf("%d subscriptions left open", open)
		}
		if len(recording.inTx) != 0 || recording.peak.Load() < 2 {
			t.Fatalf("provider calls in a transaction %v, at most %d at once", recording.inTx, recording.peak.Load())
		}
	})
}

// pushRevokingSource lets the connection be revoked while its subscribe call is at the provider.
type pushRevokingSource struct {
	*FakeExternalMailSource
	during func()
}

func (s pushRevokingSource) Subscribe(ctx context.Context, request ExternalMailPushRequest) (ExternalMailPushSubscription, error) {
	got, err := s.FakeExternalMailSource.Subscribe(ctx, request)
	if s.during != nil {
		s.during()
	}
	return got, err
}

// TestExternalMailPushCleansUpAfterAConcurrentRevoke: when the connection is revoked while its sync
// subscribes, the revocation finds no live subscription to stop and the save finds the connection
// revoked; the subscription the sync created is stopped again at the provider.
func TestExternalMailPushCleansUpAfterAConcurrentRevoke(t *testing.T) {
	f := newPushFixture(t)
	revoked := false
	registry := fakeExternalMailRegistry(f.source)
	registry.connectors[0].Push = pushRevokingSource{FakeExternalMailSource: f.source, during: func() {
		if !revoked {
			revoked = true
			if err := f.service.RevokeExternalMailConnection(context.Background(), f.b, f.connection.ID); err != nil {
				t.Errorf("revoke: %v", err)
			}
		}
	}}
	f.service.externalConnectors = registry
	f.sync(t)
	if !revoked || f.source.Subscribes(externalTestToken) != 1 {
		t.Fatalf("revoked %v, subscribes %d", revoked, f.source.Subscribes(externalTestToken))
	}
	if _, _, live := f.source.PushSubscription(externalTestToken); live {
		t.Fatalf("a subscription created for a revoked connection is still live at the provider (unsubscribed %v)", f.source.Unsubscribed())
	}
}

// unsupportedTenantSource cannot push for tenant mailboxes, as Gmail cannot.
type unsupportedTenantSource struct{ *FakeExternalMailSource }

func (s unsupportedTenantSource) Subscribe(ctx context.Context, request ExternalMailPushRequest) (ExternalMailPushSubscription, error) {
	if request.TenantMailbox {
		return ExternalMailPushSubscription{}, ErrExternalMailPushUnsupported
	}
	return s.FakeExternalMailSource.Subscribe(ctx, request)
}

// TestExternalMailPushUnsupportedForTenantMailboxes: a provider that cannot push for a mailbox
// assigned from a tenant is not retried each sync: the subscription reads unsupported, the mailbox is
// polled every two minutes, and the provider is asked again only a day later.
func TestExternalMailPushUnsupportedForTenantMailboxes(t *testing.T) {
	tf := newTenantFixture(t)
	migratePushTable(t, tf.externalFixture)
	tf.service.settings.ExternalMail.Push = config.ExternalMailPushConfig{Enabled: true}
	registry := fakeExternalMailRegistry(tf.source)
	registry.connectors[0].Push = unsupportedTenantSource{tf.source}
	tf.service.externalConnectors = registry
	assigned := tf.mustAssign(t, personalRow("alice", accessB))[0]
	token := TenantMailboxToken(tenantAddress("alice"))
	now := tf.service.now().UTC()
	tf.sync(t)
	for range 5 {
		tf.advance(externalMailSyncInterval)
		tf.sync(t)
	}
	subscription := tf.subscription(t, assigned.ID)
	if subscription.Status != domain.ExternalMailSubscriptionUnsupported || subscription.LastErrorCode != "" ||
		subscription.ConsecutiveFailures != 0 || !subscription.RenewAfter.Equal(now.Add(externalMailPushUnsupportedRecheck)) {
		t.Fatalf("subscription = %+v", subscription)
	}
	if got := tf.source.Subscribes(token); got != 0 {
		t.Fatalf("the provider was asked %d times", got)
	}
	if next := tf.nextSyncAt(t, assigned.ID); !next.Equal(tf.service.now().UTC().Add(externalMailSyncInterval)) {
		t.Fatalf("an unsupported mailbox polls at %v", next)
	}
	connections, _ := tf.service.ListExternalMailConnections(context.Background(), tf.b)
	for _, connection := range connections {
		if connection.ID == assigned.ID && connection.PushStatus != domain.ExternalMailSubscriptionUnsupported {
			t.Fatalf("owners see %q", connection.PushStatus)
		}
	}
}

// TestExternalMailPushBatchMatchesEveryNotification: an Outlook batch naming several subscriptions
// marks each, read with one lookup.
func TestExternalMailPushBatchMatchesEveryNotification(t *testing.T) {
	f := newPushFixture(t)
	other := f.addConnection(t, domain.ExternalMailProviderGmail, "batch-token")
	f.sync(t)
	now := f.advance(time.Minute)
	idA, secretA, _ := f.source.PushSubscription(externalTestToken)
	idB, secretB, _ := f.source.PushSubscription("batch-token")
	body := `{"value":[{"subscriptionId":"` + idA + `","clientState":"` + secretA + `"},{"subscriptionId":"` + idB + `","clientState":"` + secretB + `"}]}`
	counting := &countingPushStore{ExternalMailPushRepository: f.service.pushStore}
	f.service.pushStore = counting
	f.notify(t, []byte(body))
	if counting.matches != 1 {
		t.Fatalf("a batch of two notifications looked up %d times, want once", counting.matches)
	}
	for _, id := range []uint{f.connection.ID, other.ID} {
		if next := f.nextSyncAt(t, id); !next.Equal(now) {
			t.Fatalf("connection %d of the batch is not due", id)
		}
	}
}

// countingPushStore counts the subscription lookups.
type countingPushStore struct {
	domain.ExternalMailPushRepository
	matches int
}

func (c *countingPushStore) MatchExternalMailSubscriptions(ctx context.Context, provider domain.ExternalMailProvider, keys []string, limit int) ([]domain.ExternalMailPushHit, error) {
	c.matches++
	return c.ExternalMailPushRepository.MatchExternalMailSubscriptions(ctx, provider, keys, limit)
}

// TestGoogleOIDCVerifierFetchesWithoutBlocking: a slow key set fetch for an unknown key never holds
// up a token signed with a known key, and concurrent callers share one fetch.
func TestGoogleOIDCVerifierFetchesWithoutBlocking(t *testing.T) {
	keyA, keyB := pushTestRSAKeys(t)
	release := make(chan struct{})
	var fetches atomic.Int32
	slow := false
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		mu.Lock()
		wait := slow
		mu.Unlock()
		if wait {
			<-release
		}
		_, _ = w.Write(jwksOf(map[string]*rsa.PrivateKey{"key-a": keyA}))
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	verifier := newGoogleOIDCVerifier(server.URL, pushTestAudience, pushTestServiceAccount)
	ctx := context.Background()
	known := signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "exp", pushTestNow.Add(48*time.Hour).Unix()))
	if err := verifier.Verify(ctx, known, pushTestNow); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	slow = true
	mu.Unlock()
	unknown := signPushToken(t, keyB, "key-b", "RS256", goodPushClaims())
	started := make(chan struct{})
	go func() {
		close(started)
		_ = verifier.Verify(ctx, unknown, pushTestNow.Add(2*time.Minute))
	}()
	<-started
	for fetches.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	done := make(chan error, 1)
	go func() { done <- verifier.Verify(ctx, known, pushTestNow.Add(2*time.Minute)) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("known key during a fetch: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("a known key waited for the key set fetch of an unknown one")
	}
}
