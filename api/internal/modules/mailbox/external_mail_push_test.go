package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
	"github.com/fastagi/zmail/api/pkg/response"
)

// newPushFixture is the external fixture with push on and the fake source serving subscriptions.
func newPushFixture(t *testing.T) *externalFixture {
	t.Helper()
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	if err := f.repo.(*repository).db.AutoMigrate(&ExternalMailSubscriptionPO{}); err != nil {
		t.Fatalf("migrate subscriptions: %v", err)
	}
	f.service.settings.ExternalMail.Push = config.ExternalMailPushConfig{Enabled: true}
	return f
}

// fakeNotificationBody is a notification of the subscription with the given secret.
func fakeNotificationBody(id, secret, lifecycle string) []byte {
	body, _ := json.Marshal(map[string]any{"value": []map[string]string{
		{"subscriptionId": id, "clientState": secret, "lifecycleEvent": lifecycle},
	}})
	return body
}

func (f *externalFixture) subscription(t *testing.T, connectionID uint) *domain.ExternalMailSubscription {
	t.Helper()
	subscription, err := f.service.pushStore.FindExternalMailSubscription(context.Background(), connectionID)
	if err != nil {
		t.Fatalf("FindExternalMailSubscription: %v", err)
	}
	return subscription
}

func (f *externalFixture) notify(t *testing.T, provider domain.ExternalMailProvider, body []byte) {
	t.Helper()
	reply, err := f.service.ReceiveExternalMailPush(context.Background(), provider, ExternalMailPushDelivery{
		Header: http.Header{}, Query: url.Values{}, Body: body,
	})
	if err != nil || reply.Validation != "" {
		t.Fatalf("ReceiveExternalMailPush = %+v, %v", reply, err)
	}
}

// notifyFake notifies the fixture connection's live fake subscription with its secret.
func (f *externalFixture) notifyFake(t *testing.T, token string) {
	t.Helper()
	id, secret, ok := f.source.PushSubscription(token)
	if !ok {
		t.Fatalf("no fake subscription for %s", token)
	}
	f.notify(t, domain.ExternalMailProviderGmail, fakeNotificationBody(id, secret, ""))
}

func (f *externalFixture) nextSyncAt(t *testing.T, connectionID uint) time.Time {
	t.Helper()
	var next time.Time
	if err := f.repo.(*repository).db.Raw(`SELECT next_sync_at FROM external_mail_connections WHERE id = ?`, connectionID).Scan(&next).Error; err != nil {
		t.Fatalf("read next_sync_at: %v", err)
	}
	return next.UTC()
}

// connectionIn binds an account in any organization to its own connected mailbox, due now.
func (f *externalFixture) connectionIn(t *testing.T, organizationID uint, address, token string) *domain.ExternalMailConnection {
	t.Helper()
	ctx := context.Background()
	owner := uint(accessB)
	mailbox := &domain.Mailbox{
		OrganizationID: organizationID, CreatedBy: accessB, Address: address,
		Type: domain.MailboxTypePersonal, Origin: domain.MailboxOriginConnected, OwnerUserID: &owner,
	}
	if err := f.repo.CreateMailbox(ctx, mailbox, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	sealed, err := f.service.sealRefreshToken(token)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	connection := &domain.ExternalMailConnection{
		OrganizationID: organizationID, MailboxID: mailbox.ID, Provider: domain.ExternalMailProviderGmail, AccountAddress: address,
		OwnerUserID: &owner, EncryptedRefreshToken: sealed, NextSyncAt: f.service.now().UTC(),
	}
	if err := f.service.externalMail.CreateExternalMailConnection(ctx, connection); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return connection
}

// TestExternalMailPushSubscribesAfterFirstSync: the first full sync subscribes the connection with
// its own grant, stores only the secret's hash, slows the poll to the push interval, and shows the
// subscription on the connection list; the run is a poll.
func TestExternalMailPushSubscribesAfterFirstSync(t *testing.T) {
	f := newPushFixture(t)
	now := f.service.now().UTC()
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", now.Add(-time.Hour)))
	f.sync(t)

	subscription := f.subscription(t, f.connection.ID)
	id, secret, ok := f.source.PushSubscription(externalTestToken)
	if !ok || subscription == nil || subscription.Status != domain.ExternalMailSubscriptionActive || subscription.LookupKey != id ||
		subscription.ProviderSubscriptionID != id || subscription.SecretHash != pushHash(secret) || subscription.SecretHash == secret {
		t.Fatalf("subscription = %+v (fake id %q)", subscription, id)
	}
	if got := f.source.LastPushToken(externalTestToken); got.RefreshToken != externalTestToken {
		t.Fatalf("subscribed with %+v, want the connection's own grant", got)
	}
	if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now.Add(config.DefaultExternalMailPushPollInterval)) {
		t.Fatalf("next sync at %v, want the push poll interval after %v", next, now)
	}
	if runs := f.runs(t); len(runs) != 1 || runs[0].Trigger != domain.ExternalMailSyncTriggerPoll {
		t.Fatalf("runs = %+v", runs)
	}
	connections, err := f.service.ListExternalMailConnections(context.Background(), f.b)
	if err != nil || len(connections) != 1 || connections[0].Push == nil {
		t.Fatalf("connections = %+v, %v", connections, err)
	}
	state := toExternalMailPushState(connections[0].Push, now)
	if state.Status != "active" || state.ExpiresAt == nil || state.LastNotifiedAt != nil {
		t.Fatalf("push state = %+v", state)
	}
	encoded, _ := json.Marshal(toExternalMailConnectionResponses(connections))
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), id) || strings.Contains(string(encoded), subscription.SecretHash) {
		t.Fatalf("the connection response carries the subscription's id or secret: %s", encoded)
	}
}

// TestExternalMailPushNotificationSyncsAtOnce: a notification makes the connection due now, the
// next pass syncs it as a push, answers the notification, and the poll is slowed again.
func TestExternalMailPushNotificationSyncsAtOnce(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	now := f.advance(time.Minute)
	f.source.Deliver(externalTestToken, externalMessage("g-2", "Fresh", now))
	f.notifyFake(t, externalTestToken)
	if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now) {
		t.Fatalf("after a notification next sync at %v, want now %v", next, now)
	}
	if handled := f.sync(t); handled != 1 {
		t.Fatalf("pass handled %d, want the notified connection", handled)
	}
	if stored := f.storedExternal(t); len(stored) != 1 || stored[0].Subject != "Fresh" {
		t.Fatalf("stored = %d messages", len(stored))
	}
	runs := f.runs(t)
	if runs[0].Trigger != domain.ExternalMailSyncTriggerPush || runs[1].Trigger != domain.ExternalMailSyncTriggerPoll {
		t.Fatalf("triggers = %q, %q", runs[0].Trigger, runs[1].Trigger)
	}
	subscription := f.subscription(t, f.connection.ID)
	if subscription.PendingAt != nil || subscription.LastPushSyncAt == nil || !subscription.LastPushSyncAt.Equal(now) ||
		subscription.LastNotifiedAt == nil {
		t.Fatalf("subscription after the push sync = %+v", subscription)
	}
	if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now.Add(config.DefaultExternalMailPushPollInterval)) {
		t.Fatalf("next sync at %v", next)
	}
	if f.source.Subscribes(externalTestToken) != 1 {
		t.Fatalf("a sync before the renewal subscribed again")
	}
}

// TestExternalMailPushCoalescesABurst: a flood of notifications, replays included, syncs the
// connection at most once per coalescing window.
func TestExternalMailPushCoalescesABurst(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	f.advance(time.Minute)
	id, secret, _ := f.source.PushSubscription(externalTestToken)
	body := fakeNotificationBody(id, secret, "")
	for second := 0; second < 30; second++ {
		for range 20 {
			f.notify(t, domain.ExternalMailProviderGmail, body)
		}
		f.sync(t)
		f.advance(time.Second)
	}
	pushed := 0
	for _, run := range f.runs(t) {
		if run.Trigger == domain.ExternalMailSyncTriggerPush {
			pushed++
		}
	}
	window := int(config.DefaultExternalMailPushCoalesceWindow / time.Second)
	if pushed < 30/window || pushed > 30/window+1 {
		t.Fatalf("600 notifications over 30 seconds caused %d syncs, want one per %d-second window", pushed, window)
	}
}

// TestExternalMailPushNeverShortensALease: a notification for a connection a worker holds leaves the
// lease; the notification stays pending and syncs once the lease ends.
func TestExternalMailPushNeverShortensALease(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	now := f.advance(config.DefaultExternalMailPushPollInterval)
	lease := now.Add(externalMailSyncInterval)
	claimed, err := f.service.externalMail.ClaimExternalMailConnections(context.Background(), domain.ExternalMailClaim{
		Now: now, LeaseUntil: lease, Limit: 5, PerProvider: 5,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d, %v", len(claimed), err)
	}
	f.notifyFake(t, externalTestToken)
	if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(lease) {
		t.Fatalf("a notification moved the lease to %v, want %v", next, lease)
	}
	if subscription := f.subscription(t, f.connection.ID); subscription.PendingAt == nil {
		t.Fatal("the notification is not pending")
	}
	if handled := f.sync(t); handled != 0 {
		t.Fatalf("a leased connection was synced")
	}
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if runs := f.runs(t); runs[0].Trigger != domain.ExternalMailSyncTriggerPush {
		t.Fatalf("the sync after the lease = %q, want push", runs[0].Trigger)
	}
}

// notifyingSource notifies the connection while its fetch runs, as a provider does when mail
// arrives during a sync.
type notifyingSource struct {
	*FakeExternalMailSource
	during func()
}

func (s notifyingSource) Fetch(ctx context.Context, request ExternalMailFetchRequest) (ExternalMailFetch, error) {
	if s.during != nil {
		s.during()
	}
	return s.FakeExternalMailSource.Fetch(ctx, request)
}

// TestExternalMailPushDuringAFetchSyncsAgain: a notification that arrives during a fetch is not
// answered by it; the next pass promotes it and syncs again.
func TestExternalMailPushDuringAFetchSyncsAgain(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	f.advance(config.DefaultExternalMailPushPollInterval)
	notified := false
	f.service.externalConnectors = fakeExternalMailRegistry(notifyingSource{FakeExternalMailSource: f.source, during: func() {
		if !notified {
			notified = true
			f.advance(time.Second)
			f.notifyFake(t, externalTestToken)
		}
	}})
	f.sync(t)
	now := f.service.now().UTC()
	if subscription := f.subscription(t, f.connection.ID); subscription.PendingAt == nil {
		t.Fatal("a notification received during the fetch was answered by it")
	}
	f.sync(t)
	runs := f.runs(t)
	if len(runs) != 3 || runs[0].Trigger != domain.ExternalMailSyncTriggerPush || !runs[0].StartedAt.Equal(now) {
		t.Fatalf("runs = %d, newest %+v; want a push sync right after", len(runs), runs[0])
	}
}

// TestExternalMailPushRejectsWrongSecretsAndUnknownSubscriptions: a notification whose clientState
// does not match, or that names no subscription, is answered the same and changes nothing.
func TestExternalMailPushRejectsWrongSecretsAndUnknownSubscriptions(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	before := f.nextSyncAt(t, f.connection.ID)
	id, secret, _ := f.source.PushSubscription(externalTestToken)
	for name, body := range map[string][]byte{
		"wrong secret":         fakeNotificationBody(id, secret+"x", ""),
		"empty secret":         fakeNotificationBody(id, "", ""),
		"the stored hash":      fakeNotificationBody(id, pushHash(secret), ""),
		"unknown subscription": fakeNotificationBody("fake-sub-999", secret, ""),
		"no subscription":      fakeNotificationBody("", secret, ""),
		"lifecycle, wrong key": fakeNotificationBody(id, "guess", domain.ExternalMailPushRemoved),
	} {
		f.notify(t, domain.ExternalMailProviderGmail, body)
		if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(before) {
			t.Fatalf("%s: next sync moved to %v", name, next)
		}
	}
	if subscription := f.subscription(t, f.connection.ID); subscription.LastNotifiedAt != nil || subscription.PendingAt != nil ||
		subscription.Status != domain.ExternalMailSubscriptionActive {
		t.Fatalf("rejected notifications touched the subscription: %+v", subscription)
	}
}

// TestExternalMailPushIsolatesMailboxes: a notification marks only the connection whose subscription
// and secret it names, never another connection of the organization or of another organization.
func TestExternalMailPushIsolatesMailboxes(t *testing.T) {
	f := newPushFixture(t)
	sibling := f.addConnection(t, domain.ExternalMailProviderGmail, "sibling-token")
	foreign := f.connectionIn(t, accessOrg+100, "foreign@gmail.example", "foreign-token")
	f.sync(t)
	f.advance(time.Minute)
	idA, secretA, _ := f.source.PushSubscription(externalTestToken)
	idB, secretB, _ := f.source.PushSubscription("foreign-token")
	if idA == "" || idB == "" || idA == idB {
		t.Fatalf("subscriptions %q %q", idA, idB)
	}
	before := map[uint]time.Time{}
	for _, id := range []uint{f.connection.ID, sibling.ID, foreign.ID} {
		before[id] = f.nextSyncAt(t, id)
	}
	f.notify(t, domain.ExternalMailProviderGmail, fakeNotificationBody(idA, secretB, ""))
	f.notify(t, domain.ExternalMailProviderGmail, fakeNotificationBody(idB, secretA, ""))
	for id, at := range before {
		if next := f.nextSyncAt(t, id); !next.Equal(at) {
			t.Fatalf("a crossed secret moved connection %d", id)
		}
	}
	f.notify(t, domain.ExternalMailProviderGmail, fakeNotificationBody(idA, secretA, ""))
	if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(f.service.now().UTC()) {
		t.Fatalf("the named connection is not due: %v", next)
	}
	for _, id := range []uint{sibling.ID, foreign.ID} {
		if next := f.nextSyncAt(t, id); !next.Equal(before[id]) {
			t.Fatalf("connection %d moved on another's notification", id)
		}
		if subscription := f.subscription(t, id); subscription.LastNotifiedAt != nil {
			t.Fatalf("connection %d recorded another's notification", id)
		}
	}
}

// addressPushSource authenticates every notification itself and names an account by its address,
// as Gmail does; it subscribes through the fake.
type addressPushSource struct{ *FakeExternalMailSource }

func (s addressPushSource) Subscribe(ctx context.Context, request ExternalMailPushRequest) (ExternalMailPushSubscription, error) {
	got, err := s.FakeExternalMailSource.Subscribe(ctx, request)
	return ExternalMailPushSubscription{LookupKey: normalizedPushKey(request.AccountAddress), ExpiresAt: got.ExpiresAt}, err
}

func (addressPushSource) ParseNotification(_ context.Context, delivery ExternalMailPushDelivery) (ExternalMailPushParsed, error) {
	if delivery.Header.Get("Authorization") != "Bearer good" {
		return ExternalMailPushParsed{}, ErrExternalMailPushUnauthenticated
	}
	return ExternalMailPushParsed{Hints: []ExternalMailPushHint{{Key: normalizedPushKey(string(delivery.Body)), Authenticated: true}}}, nil
}

// TestExternalMailPushByAddressSyncsEachGrant: one account bound in two organizations is one
// mailbox at the provider; its notification makes both connections due, and each is fetched with
// its own grant only. Another address moves neither.
func TestExternalMailPushByAddressSyncsEachGrant(t *testing.T) {
	f := newPushFixture(t)
	registry := fakeExternalMailRegistry(f.source)
	registry.connectors[0].Push = addressPushSource{f.source}
	f.service.externalConnectors = registry
	other := f.connectionIn(t, accessOrg+100, f.connection.AccountAddress, "other-org-token")
	unrelated := f.connectionIn(t, accessOrg+100, "unrelated@gmail.example", "unrelated-token")
	f.sync(t)
	now := f.advance(time.Minute)
	header := http.Header{"Authorization": {"Bearer good"}}
	if _, err := f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, ExternalMailPushDelivery{
		Header: header, Body: []byte(strings.ToUpper(f.connection.AccountAddress)),
	}); err != nil {
		t.Fatalf("ReceiveExternalMailPush: %v", err)
	}
	for _, id := range []uint{f.connection.ID, other.ID} {
		if next := f.nextSyncAt(t, id); !next.Equal(now) {
			t.Fatalf("connection %d of the notified account is not due", id)
		}
	}
	if next := f.nextSyncAt(t, unrelated.ID); next.Equal(now) {
		t.Fatal("another account's connection was made due")
	}
	if _, err := f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, ExternalMailPushDelivery{
		Header: http.Header{}, Body: []byte(f.connection.AccountAddress),
	}); !errors.Is(err, ErrExternalMailPushUnauthenticated) {
		t.Fatalf("unauthenticated notification: %v", err)
	}
	f.sync(t)
	if got := f.source.LastRequest(externalTestToken).Token.RefreshToken; got != externalTestToken {
		t.Fatalf("fixture connection fetched with %q", got)
	}
	if got := f.source.LastRequest("other-org-token").Token.RefreshToken; got != "other-org-token" {
		t.Fatalf("other organization's connection fetched with %q", got)
	}
}

// TestExternalMailPushRenewsBeforeExpiry: the subscription is renewed in the sync after half its
// life, keeping its id and secret, and never before.
func TestExternalMailPushRenewsBeforeExpiry(t *testing.T) {
	f := newPushFixture(t)
	f.source.SetPushLife(2 * time.Hour)
	start := f.service.now().UTC()
	f.sync(t)
	first := f.subscription(t, f.connection.ID)
	if !first.RenewAfter.Equal(start.Add(time.Hour)) {
		t.Fatalf("renew after %v, want half the life", first.RenewAfter)
	}
	for f.service.now().Before(start.Add(time.Hour)) {
		f.advance(config.DefaultExternalMailPushPollInterval)
		f.sync(t)
	}
	renewed := f.subscription(t, f.connection.ID)
	if f.source.Subscribes(externalTestToken) != 2 || renewed.ProviderSubscriptionID != first.ProviderSubscriptionID ||
		renewed.SecretHash != first.SecretHash || !renewed.ExpiresAt.After(*first.ExpiresAt) {
		t.Fatalf("after an hour: %d subscribes, %+v", f.source.Subscribes(externalTestToken), renewed)
	}
	// A subscription the provider removed is created again with a new secret.
	_, secret, _ := f.source.PushSubscription(externalTestToken)
	f.source.DropPushSubscription(renewed.ProviderSubscriptionID)
	f.notify(t, domain.ExternalMailProviderGmail, fakeNotificationBody(renewed.ProviderSubscriptionID, secret, domain.ExternalMailPushRemoved))
	if subscription := f.subscription(t, f.connection.ID); subscription.Live(f.service.now()) {
		t.Fatalf("a removed subscription is still live: %+v", subscription)
	}
	f.sync(t)
	recreated := f.subscription(t, f.connection.ID)
	if recreated.ProviderSubscriptionID == renewed.ProviderSubscriptionID || recreated.SecretHash == renewed.SecretHash ||
		recreated.Status != domain.ExternalMailSubscriptionActive {
		t.Fatalf("after removal: %+v", recreated)
	}
}

// TestExternalMailPushFailureFallsBackToPolling: a failed subscribe or renewal never stops the sync:
// mail is stored, the connection stays active, the attempt backs off, and the poll stays at two
// minutes until a subscription is live.
func TestExternalMailPushFailureFallsBackToPolling(t *testing.T) {
	f := newPushFixture(t)
	now := f.service.now().UTC()
	f.source.FailSubscribe(externalTestToken, errors.New("pubsub topic missing"))
	f.source.Deliver(externalTestToken, externalMessage("g-1", "Still synced", now.Add(-time.Minute)))
	f.sync(t)
	connection := f.reload(t)
	subscription := f.subscription(t, f.connection.ID)
	if connection.Status != domain.ExternalMailStatusActive || connection.LastErrorCode != "" || len(f.storedExternal(t)) != 1 {
		t.Fatalf("connection after a failed subscribe = %+v", connection)
	}
	if subscription.Status != domain.ExternalMailSubscriptionPending || subscription.ConsecutiveFailures != 1 ||
		subscription.LastErrorCode != domain.ExternalMailErrorFetchFailed || !subscription.RenewAfter.After(now) {
		t.Fatalf("subscription after a failed subscribe = %+v", subscription)
	}
	if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now.Add(externalMailSyncInterval)) {
		t.Fatalf("poll after a failed subscribe at %v, want two minutes", next)
	}
	f.advance(externalMailMaxBackoff)
	f.sync(t)
	if subscription = f.subscription(t, f.connection.ID); subscription.ConsecutiveFailures != 2 {
		t.Fatalf("second failure = %+v", subscription)
	}
	f.source.FailSubscribe(externalTestToken, nil)
	f.advance(externalMailMaxBackoff)
	f.sync(t)
	if subscription = f.subscription(t, f.connection.ID); subscription.Status != domain.ExternalMailSubscriptionActive || subscription.ConsecutiveFailures != 0 {
		t.Fatalf("recovered subscription = %+v", subscription)
	}

	// A failed renewal keeps the live subscription until it expires; then the poll is two minutes.
	f.source.SetPushLife(time.Hour)
	f.advance(externalMailMaxBackoff * 100)
	f.sync(t)
	f.source.FailSubscribe(externalTestToken, ErrExternalMailAuth)
	for range 8 {
		f.advance(15 * time.Minute)
		f.sync(t)
	}
	subscription = f.subscription(t, f.connection.ID)
	if subscription.LastErrorCode != domain.ExternalMailErrorAuthFailed || subscription.Live(f.service.now()) {
		t.Fatalf("after failed renewals = %+v", subscription)
	}
	if connection = f.reload(t); connection.Status != domain.ExternalMailStatusActive {
		t.Fatalf("a refused renewal stopped the connection: %+v", connection)
	}
	if next := f.nextSyncAt(t, f.connection.ID); next.After(f.service.now().Add(externalMailSyncInterval)) {
		t.Fatalf("poll with an expired subscription at %v, want at most two minutes", next)
	}
}

// TestExternalMailPushStopsWithItsConnection: revoking by the owner, a forced disconnect, and a
// member's departure stop the subscription at the provider and in the database; a connection that
// needs reconnecting, a revoked tenant, and a purge stop it in the database, and its notifications
// are ignored from the moment the connection stops being active.
func TestExternalMailPushStopsWithItsConnection(t *testing.T) {
	ctx := context.Background()
	stoppedAndInert := func(t *testing.T, f *externalFixture, connectionID uint, id, secret string) {
		t.Helper()
		before := f.nextSyncAt(t, connectionID)
		f.notify(t, domain.ExternalMailProviderGmail, fakeNotificationBody(id, secret, ""))
		if next := f.nextSyncAt(t, connectionID); !next.Equal(before) {
			t.Fatalf("a stopped connection's notification moved it")
		}
		f.sync(t)
		subscription := f.subscription(t, connectionID)
		if subscription.Status != domain.ExternalMailSubscriptionStopped || subscription.LookupKey != "" || subscription.SecretHash != "" {
			t.Fatalf("subscription = %+v, want stopped", subscription)
		}
	}
	for name, revoke := range map[string]func(f *externalFixture) error{
		"owner revoke": func(f *externalFixture) error {
			return f.service.RevokeExternalMailConnection(ctx, f.b, f.connection.ID)
		},
		"forced": func(f *externalFixture) error {
			return f.service.RevokeExternalMailConnection(ctx, f.admin, f.connection.ID)
		},
		"member left": func(f *externalFixture) error {
			return f.service.MemberRemoved(ctx, domain.NewOrganizationMemberRemovedEvent(accessOrg, accessB, f.b.MembershipID, f.service.now()))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newPushFixture(t)
			f.sync(t)
			id, secret, _ := f.source.PushSubscription(externalTestToken)
			if err := revoke(f); err != nil {
				t.Fatalf("revoke: %v", err)
			}
			if stopped := f.source.Unsubscribed(); len(stopped) != 1 || stopped[0] != id {
				t.Fatalf("unsubscribed at the provider = %v, want %q", stopped, id)
			}
			if subscription := f.subscription(t, f.connection.ID); subscription.Status != domain.ExternalMailSubscriptionStopped {
				t.Fatalf("not stopped in the revoke: %+v", subscription)
			}
			stoppedAndInert(t, f, f.connection.ID, id, secret)
		})
	}
	t.Run("needs reconnect", func(t *testing.T) {
		f := newPushFixture(t)
		f.sync(t)
		id, secret, _ := f.source.PushSubscription(externalTestToken)
		f.source.FailAuth(externalTestToken)
		f.advance(config.DefaultExternalMailPushPollInterval)
		f.sync(t)
		if connection := f.reload(t); connection.Status != domain.ExternalMailStatusNeedsReconnect {
			t.Fatalf("status %q", connection.Status)
		}
		stoppedAndInert(t, f, f.connection.ID, id, secret)
	})
	t.Run("purge", func(t *testing.T) {
		f := newPushFixture(t)
		f.sync(t)
		if err := f.service.RevokeExternalMailConnection(ctx, f.b, f.connection.ID); err != nil {
			t.Fatal(err)
		}
		f.advance(config.DefaultExternalMailRevokedRetention + time.Hour)
		f.purge(t)
		if open := f.count(t, `SELECT count(*) FROM external_mail_subscriptions WHERE status <> 'stopped'`); open != 0 {
			t.Fatalf("%d subscriptions still open after the purge", open)
		}
	})
}

// TestExternalMailPushTenantMailbox: a mailbox assigned from a tenant subscribes with the token the
// tenant minted for that mailbox only, and stops when the tenant is revoked.
func TestExternalMailPushTenantMailbox(t *testing.T) {
	tf := newTenantFixture(t)
	if err := tf.repo.(*repository).db.AutoMigrate(&ExternalMailSubscriptionPO{}); err != nil {
		t.Fatal(err)
	}
	tf.service.settings.ExternalMail.Push = config.ExternalMailPushConfig{Enabled: true}
	assigned := tf.mustAssign(t, personalRow("alice", accessB))[0]
	tf.sync(t)
	token := TenantMailboxToken(tenantAddress("alice"))
	if got := tf.source.LastPushToken(token); got.AccessToken != token || got.RefreshToken != "" || got.DirectoryID == "" {
		t.Fatalf("tenant mailbox subscribed with %+v", got)
	}
	id, secret, ok := tf.source.PushSubscription(token)
	if !ok {
		t.Fatal("tenant mailbox not subscribed")
	}
	if err := tf.service.RevokeExternalMailTenant(context.Background(), tf.owner, tf.tenant.ID); err != nil {
		t.Fatalf("revoke tenant: %v", err)
	}
	if subscription := tf.subscription(t, assigned.ID); subscription.Status != domain.ExternalMailSubscriptionStopped {
		t.Fatalf("tenant revocation left %+v", subscription)
	}
	before := tf.nextSyncAt(t, assigned.ID)
	tf.notify(t, domain.ExternalMailProviderGmail, fakeNotificationBody(id, secret, ""))
	if next := tf.nextSyncAt(t, assigned.ID); !next.Equal(before) {
		t.Fatal("a revoked tenant's notification moved its connection")
	}
}

// TestExternalMailPushKeepsTheReceiveFloor: a push-triggered sync still drops mail received before
// the connection's received_not_before.
func TestExternalMailPushKeepsTheReceiveFloor(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	now := f.advance(time.Minute)
	if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_connections SET received_not_before = ? WHERE id = ?`, now, f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	f.source.Deliver(externalTestToken, externalMessage("g-old", "Before the floor", now.Add(-time.Hour)),
		externalMessage("g-new", "After the floor", now.Add(time.Second)))
	f.notifyFake(t, externalTestToken)
	f.sync(t)
	stored := f.storedExternal(t)
	if len(stored) != 1 || stored[0].Subject != "After the floor" || f.runs(t)[0].Trigger != domain.ExternalMailSyncTriggerPush {
		t.Fatalf("stored %d messages after a push sync", len(stored))
	}
}

// TestExternalMailPushSwitchedOff: with push off nothing subscribes, the routes are unavailable,
// the poll stays two minutes, and the connection list shows no subscription.
func TestExternalMailPushSwitchedOff(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	if err := f.repo.(*repository).db.AutoMigrate(&ExternalMailSubscriptionPO{}); err != nil {
		t.Fatal(err)
	}
	now := f.service.now().UTC()
	f.sync(t)
	if f.source.Subscribes(externalTestToken) != 0 || f.count(t, `SELECT count(*) FROM external_mail_subscriptions`) != 0 {
		t.Fatal("a subscription was created with push off")
	}
	if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now.Add(externalMailSyncInterval)) {
		t.Fatalf("poll at %v", next)
	}
	_, err := f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, ExternalMailPushDelivery{
		Header: http.Header{}, Query: url.Values{"validationToken": {"abc"}}, Body: []byte(`{}`),
	})
	if !errors.Is(err, ErrExternalMailProviderUnavailable) {
		t.Fatalf("notification with push off: %v", err)
	}
	connections, err := f.service.ListExternalMailConnections(context.Background(), f.b)
	if err != nil || connections[0].Push != nil {
		t.Fatalf("connections = %+v, %v", connections, err)
	}
}

// TestExternalMailPushRoutes: the routes need no session or key; the handshake echoes plain text,
// a notification is acknowledged without a body, an unauthenticated Gmail request is 401, an
// oversized body is refused, and the per-provider budget answers 429 with Retry-After. Nothing from
// the body reaches the logs.
func TestExternalMailPushRoutes(t *testing.T) {
	f := newPushFixture(t)
	registry := fakeExternalMailRegistry(f.source)
	registry.connectors[0].Push = addressPushSource{f.source}
	f.service.externalConnectors = registry
	f.sync(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	gin.SetMode(gin.TestMode)
	handler := NewHandler(f.service)
	response.DefaultErrorMapper.Register(domain.ErrInvalidInput, http.StatusBadRequest, domain.CodeInvalidInput)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	engine := gin.New()
	routes := router.New(engine).Prefix("/v1")
	apikey.NewHandler(agentKeys{}).RegisterMiddleware(routes)
	routes.MiddlewareGroup("auth", func(c *gin.Context) { response.AbortUnauthorized(c, "Authentication required") })
	routes.MiddlewareGroup("organization_context", func(c *gin.Context) { c.Next() })
	handler.RegisterRoutes(routes)
	call := func(path, body string, header http.Header) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		for key, values := range header {
			request.Header[key] = values
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		return recorder
	}

	handshake := call("/v1/external-mail-notifications/outlook?validationToken=Validation%3A+Token+%3Cb%3E", "", nil)
	if handshake.Code != http.StatusOK || handshake.Body.String() != "Validation: Token <b>" ||
		!strings.HasPrefix(handshake.Header().Get("Content-Type"), "text/plain") || handshake.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("handshake = %d %q %v", handshake.Code, handshake.Body.String(), handshake.Header())
	}
	id, secret, _ := f.source.PushSubscription(externalTestToken)
	for name, body := range map[string][]byte{
		"known":   fakeNotificationBody(id, secret, ""),
		"unknown": fakeNotificationBody("fake-sub-404", "nope", ""),
	} {
		if got := call("/v1/external-mail-notifications/outlook", string(body), nil); got.Code != http.StatusAccepted || got.Body.Len() != 0 {
			t.Fatalf("%s notification = %d %q", name, got.Code, got.Body.String())
		}
	}
	if got := call("/v1/external-mail-notifications/outlook", "{not json", nil); got.Code != http.StatusBadRequest {
		t.Fatalf("malformed = %d", got.Code)
	}
	if got := call("/v1/external-mail-notifications/gmail", "dana@gmail.example", nil); got.Code != http.StatusUnauthorized ||
		!strings.Contains(got.Body.String(), response.ErrorCodeUnauthorized) {
		t.Fatalf("unauthenticated gmail = %d %s", got.Code, got.Body.String())
	}
	if got := call("/v1/external-mail-notifications/gmail", "dana@gmail.example", http.Header{"Authorization": {"Bearer good"}}); got.Code != http.StatusNoContent {
		t.Fatalf("authenticated gmail = %d %s", got.Code, got.Body.String())
	}
	if got := call("/v1/external-mail-notifications/outlook", strings.Repeat("x", maxExternalMailPushBodyBytes+1), nil); got.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized = %d", got.Code)
	}
	f.service.pushLimits = ratelimit.NewMemoryStores().New(ratelimit.Rule{Name: ruleExternalMailPush, Max: 1, Window: time.Minute})
	call("/v1/external-mail-notifications/outlook", string(fakeNotificationBody(id, secret, "")), nil)
	limited := call("/v1/external-mail-notifications/outlook", string(fakeNotificationBody(id, secret, "")), nil)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("over budget = %d %v", limited.Code, limited.Header())
	}
	for _, leaked := range []string{secret, id, "dana@gmail.example", "Bearer good", "Validation: Token"} {
		if strings.Contains(logs.String(), leaked) {
			t.Fatalf("the logs carry %q", leaked)
		}
	}
	f.service.settings.ExternalMail.Push.Enabled = false
	if got := call("/v1/external-mail-notifications/outlook", string(fakeNotificationBody(id, secret, "")), nil); got.Code != http.StatusNotFound ||
		!strings.Contains(got.Body.String(), domain.CodeExternalMailProviderUnavailable) {
		t.Fatalf("switched off = %d %s", got.Code, got.Body.String())
	}
}
