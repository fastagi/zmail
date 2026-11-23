package mailbox

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// Tests for the second review of the push subscriptions, which blocks enabling push.

// newAddressPushFixture is the push fixture whose Gmail connector authenticates notifications by a
// "Bearer good" header and names the account by its address, as Gmail does.
func newAddressPushFixture(t *testing.T) *externalFixture {
	t.Helper()
	f := newPushFixture(t)
	registry := fakeExternalMailRegistry(f.source)
	registry.connectors[0].Push = addressPushSource{f.source}
	f.service.externalConnectors = registry
	return f
}

// notifyAddress sends an authenticated notification for the fixture account.
func (f *externalFixture) notifyAddress(t *testing.T) {
	t.Helper()
	if _, err := f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, ExternalMailPushDelivery{
		Header: http.Header{"Authorization": {"Bearer good"}}, Body: []byte(f.connection.AccountAddress), ClientIP: "66.102.0.1",
	}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	f.promote(t)
}

// TestExternalMailPushHandshakesSurviveJunk: a sender that used up its junk budget still completes
// Graph's validation handshake, which has a budget of its own; junk past its budget is answered 429
// without a log line, and the refusals within it log at most one line a minute.
func TestExternalMailPushHandshakesSurviveJunk(t *testing.T) {
	f := newPushFixture(t)
	f.service.pushRefusals = ratelimit.NewMemoryStore(3, time.Minute)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx := context.Background()
	limited := 0
	for range 40 {
		_, err := f.service.ReceiveExternalMailPush(ctx, domain.ExternalMailProviderOutlook, ExternalMailPushDelivery{
			Header: http.Header{}, Query: url.Values{}, Body: fakeNotificationBody("fake-sub-404", "guess", ""), ClientIP: "10.1.2.3",
		})
		if errors.Is(err, ErrExternalMailPushRateLimited) {
			limited++
		}
	}
	if limited != 37 {
		t.Fatalf("junk past its budget answered 429 %d times, want 37", limited)
	}
	if lines := strings.Count(logs.String(), "external_mail.push_refused"); lines != 1 {
		t.Fatalf("40 junk requests logged %d lines, want 1", lines)
	}
	reply, err := f.service.ReceiveExternalMailPush(ctx, domain.ExternalMailProviderOutlook, ExternalMailPushDelivery{
		Header: http.Header{}, Query: url.Values{"validationToken": {"handshake-1"}}, ClientIP: "10.1.2.3",
	})
	if err != nil || reply.Validation != "handshake-1" {
		t.Fatalf("a handshake after the junk = %+v, %v", reply, err)
	}
}

// TestExternalMailPushIgnoresMailItAlreadyHas: a poll that reads again a message already stored, as
// Graph delta does for a message the member read, flagged, or categorized, and as a restart after an
// expired cursor does, never marks a healthy subscription degraded. Only mail the save inserts
// counts.
func TestExternalMailPushIgnoresMailItAlreadyHas(t *testing.T) {
	for name, again := range map[string]func(f *externalFixture, message ExternalMessage){
		"provider reports an updated message": func(f *externalFixture, message ExternalMessage) {
			f.source.Deliver(externalTestToken, message)
		},
		"restart after an expired cursor": func(f *externalFixture, _ ExternalMessage) {
			f.source.ExpireCursor(externalTestToken)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAddressPushFixture(t)
			start := f.service.now().UTC()
			f.sync(t)
			message := externalMessage("g-read-later", "Read ten minutes later", start.Add(8*time.Minute))
			f.source.Deliver(externalTestToken, message)
			f.advance(8 * time.Minute)
			f.notifyAddress(t)
			f.advance(config.DefaultExternalMailPushCoalesceWindow)
			f.sync(t)
			if runs := f.runs(t); runs[0].Trigger != domain.ExternalMailSyncTriggerPush || runs[0].Stored != 1 {
				t.Fatalf("the announced message: %+v", runs[0])
			}
			again(f, message)
			for range 3 {
				f.advance(config.DefaultExternalMailPushPollInterval)
				f.sync(t)
			}
			if subscription := f.subscription(t, f.connection.ID); subscription.DegradedAt != nil {
				t.Fatalf("a message already stored marked the subscription degraded: %+v", subscription)
			}
			if len(f.storedExternal(t)) != 1 {
				t.Fatal("the message was stored twice")
			}
		})
	}
}

// releaseRows reads the pending provider releases of a connection.
func (f *externalFixture) releaseRows(t *testing.T, connectionID uint) []ExternalMailProviderReleasePO {
	t.Helper()
	var rows []ExternalMailProviderReleasePO
	if err := f.repo.(*repository).db.Where("connection_id = ?", connectionID).Find(&rows).Error; err != nil {
		t.Fatalf("read releases: %v", err)
	}
	return rows
}

// TestExternalMailProviderReleaseSurvivesACrash: a process that stops right after a revocation
// committed, before it called the provider, leaves the release in the database; the worker then
// stops the subscription and hands the grant back, and deletes the release with its sealed grant.
func TestExternalMailProviderReleaseSurvivesACrash(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	id, _, _ := f.source.PushSubscription(externalTestToken)
	// The provider calls are collected for after the commit and never made: the process stopped.
	crashed, _ := withDeferredReleases(context.Background())
	if err := f.service.revokeConnection(crashed, f.reload(t), accessB, domain.MailboxAccessConnectionRevoked); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	rows := f.releaseRows(t, f.connection.ID)
	if len(rows) != 1 || !rows[0].StopPush || rows[0].StopSubscriptionID != id || rows[0].EncryptedGrant == "" ||
		strings.Contains(rows[0].EncryptedGrant, externalTestToken) {
		t.Fatalf("release rows = %+v", rows)
	}
	if grant, err := f.service.encryptor.DecryptString(rows[0].EncryptedGrant); err != nil || grant != externalTestToken {
		t.Fatalf("the release holds no sealed copy of the grant (%v)", err)
	}
	if f.source.Revoked(externalTestToken) || len(f.source.Unsubscribed()) != 0 {
		t.Fatal("the provider was called although the process stopped")
	}
	f.advance(externalMailReleaseGrace + time.Second)
	f.sync(t)
	if !f.source.Revoked(externalTestToken) {
		t.Fatal("the worker did not hand the grant back")
	}
	if stopped := f.source.Unsubscribed(); len(stopped) != 1 || stopped[0] != id {
		t.Fatalf("the worker stopped %v, want %q", stopped, id)
	}
	if rows = f.releaseRows(t, f.connection.ID); len(rows) != 0 {
		t.Fatalf("a done release was kept: %+v", rows)
	}
}

// refusingRevokeSource cannot hand grants back.
type refusingRevokeSource struct{ *FakeExternalMailSource }

func (refusingRevokeSource) Revoke(context.Context, ExternalMailToken) error {
	return errors.New("provider unavailable")
}

// TestExternalMailProviderReleaseRetriesThenExpires: a release the provider keeps refusing is retried
// with backoff, and deleted with its sealed grant at its deadline.
func TestExternalMailProviderReleaseRetriesThenExpires(t *testing.T) {
	f := newPushFixture(t)
	f.service.externalConnectors = fakeExternalMailRegistry(refusingRevokeSource{f.source})
	f.sync(t)
	if err := f.service.RevokeExternalMailConnection(context.Background(), f.b, f.connection.ID); err != nil {
		t.Fatal(err)
	}
	rows := f.releaseRows(t, f.connection.ID)
	if len(rows) != 1 || rows[0].Attempts != 1 {
		t.Fatalf("after the failed call right after the commit: %+v", rows)
	}
	for range 6 {
		f.advance(externalMailMaxBackoff)
		f.sync(t)
	}
	if rows = f.releaseRows(t, f.connection.ID); len(rows) != 1 || rows[0].Attempts < 3 {
		t.Fatalf("the release was not retried: %+v", rows)
	}
	f.advance(externalMailReleaseDeadline)
	f.sync(t)
	f.purge(t)
	if rows = f.releaseRows(t, f.connection.ID); len(rows) != 0 {
		t.Fatalf("a release past its deadline kept its sealed grant: %+v", rows)
	}
}

// primaryPushSource keys every account by one primary address, as Gmail does for aliases of one
// account, and lets the test revoke a connection while its subscribe call is at the provider.
type primaryPushSource struct {
	*FakeExternalMailSource
	during func(request ExternalMailPushRequest)
}

const pushTestPrimary = "primary@gmail.example"

func (s primaryPushSource) Subscribe(ctx context.Context, request ExternalMailPushRequest) (ExternalMailPushSubscription, error) {
	got, err := s.FakeExternalMailSource.Subscribe(ctx, request)
	if s.during != nil {
		s.during(request)
	}
	return ExternalMailPushSubscription{LookupKey: pushTestPrimary, ExpiresAt: got.ExpiresAt}, err
}

// TestExternalMailPushOrphanCleanupKeepsASharedWatch: when a connection is revoked while its sync
// subscribes, the watch it renewed is left running if another live connection of the same account,
// bound under another of its addresses, shares it.
func TestExternalMailPushOrphanCleanupKeepsASharedWatch(t *testing.T) {
	f := newPushFixture(t)
	db := f.repo.(*repository).db
	revoked := false
	registry := fakeExternalMailRegistry(f.source)
	registry.connectors[0].Push = primaryPushSource{FakeExternalMailSource: f.source, during: func(request ExternalMailPushRequest) {
		if request.Token.RefreshToken == externalTestToken && !revoked {
			revoked = true
			if err := f.service.RevokeExternalMailConnection(context.Background(), f.b, f.connection.ID); err != nil {
				t.Errorf("revoke: %v", err)
			}
		}
	}}
	f.service.externalConnectors = registry
	alias := f.connectionIn(t, accessOrg+100, "alias@gmail.example", "alias-token")
	if err := db.Exec(`UPDATE external_mail_connections SET next_sync_at = ? WHERE id = ?`, f.service.now().Add(time.Hour), f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	f.sync(t) // the alias connection watches the account
	if err := db.Exec(`UPDATE external_mail_connections SET next_sync_at = ? WHERE id = ?`, f.service.now(), f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	if !revoked {
		t.Fatal("the fixture connection never subscribed")
	}
	if stopped := f.source.Unsubscribed(); len(stopped) != 0 {
		t.Fatalf("the watch %v shares with a live connection was stopped", stopped)
	}
	if subscription := f.subscription(t, alias.ID); subscription.Status != domain.ExternalMailSubscriptionActive {
		t.Fatalf("the sharing connection's subscription = %+v", subscription)
	}
}

// TestExternalMailPushWakesEveryConnectionOfAnAccount: one Gmail notification makes every live
// connection of the account due, more than ten included.
func TestExternalMailPushWakesEveryConnectionOfAnAccount(t *testing.T) {
	f := newAddressPushFixture(t)
	ids := []uint{f.connection.ID}
	for i := 0; i < 14; i++ {
		ids = append(ids, f.connectionIn(t, accessOrg+100+uint(i), f.connection.AccountAddress, "shared-token-"+string(rune('a'+i))).ID)
	}
	f.sync(t)
	now := f.advance(time.Minute)
	f.notifyAddress(t)
	for _, id := range ids {
		if next := f.nextSyncAt(t, id); !next.Equal(now) {
			t.Fatalf("connection %d of the notified account is not due (%v)", id, next)
		}
	}
}

// TestExternalMailPushMissedEventKeepsDegraded: a Graph missed lifecycle event marks the connection
// due but does not show that delivery works, so a degraded subscription stays degraded; a change
// notification clears it.
func TestExternalMailPushMissedEventKeepsDegraded(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_subscriptions SET degraded_at = ? WHERE connection_id = ?`,
		f.service.now(), f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	id, secret, _ := f.source.PushSubscription(externalTestToken)
	f.advance(time.Minute)
	f.notify(t, fakeNotificationBody(id, secret, domain.ExternalMailPushMissed))
	if subscription := f.subscription(t, f.connection.ID); subscription.DegradedAt == nil {
		t.Fatal("a missed lifecycle event cleared the degraded mark")
	}
	f.notify(t, fakeNotificationBody(id, secret, ""))
	if subscription := f.subscription(t, f.connection.ID); subscription.DegradedAt != nil {
		t.Fatal("a change notification left the subscription degraded")
	}
}
