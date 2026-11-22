package mailbox

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// Tests for the review of the durable provider releases (PR #194).

// flakyRevokeSource refuses to hand grants back while failing is set, and counts the calls.
type flakyRevokeSource struct {
	*FakeExternalMailSource
	failing *atomic.Bool
	revokes *atomic.Int32
}

func (s flakyRevokeSource) Revoke(ctx context.Context, token ExternalMailToken) error {
	s.revokes.Add(1)
	if s.failing.Load() {
		return errors.New("provider unavailable")
	}
	return s.FakeExternalMailSource.Revoke(ctx, token)
}

// newFlakyReleaseFixture is the push fixture whose hand-back fails until the test lets it succeed;
// the fixture connection has synced and is subscribed.
func newFlakyReleaseFixture(t *testing.T) (*externalFixture, flakyRevokeSource) {
	t.Helper()
	f := newPushFixture(t)
	source := flakyRevokeSource{FakeExternalMailSource: f.source, failing: &atomic.Bool{}, revokes: &atomic.Int32{}}
	source.failing.Store(true)
	f.service.externalConnectors = fakeExternalMailRegistry(source)
	f.sync(t)
	return f, source
}

// revokeWithAFailedHandBack revokes the fixture connection as its owner; the call right after the
// commit fails, so the release stays for the worker.
func (f *externalFixture) revokeWithAFailedHandBack(t *testing.T) {
	t.Helper()
	if err := f.service.RevokeExternalMailConnection(context.Background(), f.b, f.connection.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if rows := f.releaseRows(t, f.connection.ID); len(rows) != 1 {
		t.Fatalf("after the failed hand-back the releases are %+v", rows)
	}
}

// runReleaseWorker lets the worker pick up every due release.
func (f *externalFixture) runReleaseWorker(t *testing.T) {
	t.Helper()
	f.advance(externalMailMaxBackoff)
	f.sync(t)
}

// TestExternalMailProviderReleaseSparesARebind: a hand-back that failed once never revokes the grant
// of the member who bound the same account again within the day: rebinding the connection deletes
// its pending releases, and a release the worker finds for a connection active again is dropped
// without a provider call.
func TestExternalMailProviderReleaseSparesARebind(t *testing.T) {
	ctx := context.Background()
	t.Run("rebound connection", func(t *testing.T) {
		f, source := newFlakyReleaseFixture(t)
		f.revokeWithAFailedHandBack(t)
		sealed, err := f.service.sealRefreshToken("rebound-grant")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.service.externalMail.RebindExternalMailConnection(ctx, domain.ExternalMailRebind{
			ConnectionID: f.connection.ID, EncryptedRefreshToken: sealed, ResetCursor: true, Now: f.service.now().UTC(),
		}); err != nil {
			t.Fatalf("rebind: %v", err)
		}
		if rows := f.releaseRows(t, f.connection.ID); len(rows) != 0 {
			t.Fatalf("the rebind left releases: %+v", rows)
		}
		source.failing.Store(false)
		calls := source.revokes.Load()
		f.runReleaseWorker(t)
		if source.revokes.Load() != calls || f.source.Revoked(externalTestToken) {
			t.Fatal("the old grant of a rebound account was handed back")
		}
		if connection := f.reload(t); connection.Status != domain.ExternalMailStatusActive {
			t.Fatalf("the rebound connection is %q", connection.Status)
		}
	})
	t.Run("connection active again without the rebind path", func(t *testing.T) {
		f, source := newFlakyReleaseFixture(t)
		f.revokeWithAFailedHandBack(t)
		if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_connections SET status = 'active', revoked_at = NULL WHERE id = ?`,
			f.connection.ID).Error; err != nil {
			t.Fatal(err)
		}
		source.failing.Store(false)
		calls := source.revokes.Load()
		f.runReleaseWorker(t)
		if source.revokes.Load() != calls || f.source.Revoked(externalTestToken) {
			t.Fatal("a release was performed for a connection that is active again")
		}
		if rows := f.releaseRows(t, f.connection.ID); len(rows) != 0 {
			t.Fatalf("the stale release was kept: %+v", rows)
		}
	})
}

// TestExternalMailProviderReleaseSparesTheAccountBoundElsewhere: while another live connection, a
// new row in another organization, reads the same account with a grant of its own, the old grant is
// not handed back: at Google that would revoke the user's whole grant.
func TestExternalMailProviderReleaseSparesTheAccountBoundElsewhere(t *testing.T) {
	f, source := newFlakyReleaseFixture(t)
	f.revokeWithAFailedHandBack(t)
	f.connectionIn(t, accessOrg+100, f.connection.AccountAddress, "other-org-grant")
	source.failing.Store(false)
	calls := source.revokes.Load()
	f.runReleaseWorker(t)
	if source.revokes.Load() != calls || f.source.Revoked(externalTestToken) {
		t.Fatal("the grant was handed back while the account is bound elsewhere")
	}
	if rows := f.releaseRows(t, f.connection.ID); len(rows) != 0 {
		t.Fatalf("the release was kept: %+v", rows)
	}
}

// TestExternalMailProviderReleaseSurvivesThePurge: purging the copies right after revoking deletes
// the connection, but not the hand-back that has not succeeded yet.
func TestExternalMailProviderReleaseSurvivesThePurge(t *testing.T) {
	f, source := newFlakyReleaseFixture(t)
	f.revokeWithAFailedHandBack(t)
	result, err := f.service.PurgeExternalMailCopies(context.Background(), f.b, f.connection.ID)
	if err != nil || !result.Completed {
		t.Fatalf("purge = %+v, %v", result, err)
	}
	if f.count(t, `SELECT count(*) FROM external_mail_connections WHERE id = ?`, f.connection.ID) != 0 {
		t.Fatal("the purge kept the connection")
	}
	if rows := f.releaseRows(t, f.connection.ID); len(rows) != 1 {
		t.Fatalf("the purge dropped the pending hand-back: %+v", rows)
	}
	source.failing.Store(false)
	f.runReleaseWorker(t)
	if !f.source.Revoked(externalTestToken) {
		t.Fatal("the grant of a purged connection was never handed back")
	}
	if rows := f.releaseRows(t, f.connection.ID); len(rows) != 0 {
		t.Fatalf("a done release was kept: %+v", rows)
	}
}

// TestExternalMailProviderReleaseKeepsASharedWatch: a stop the worker performs later counts the live
// watchers of the account's primary address again, so a watch another connection started meanwhile
// keeps running.
func TestExternalMailProviderReleaseKeepsASharedWatch(t *testing.T) {
	f := newAddressPushFixture(t)
	f.sync(t)
	crashed, _ := withDeferredReleases(context.Background())
	if err := f.service.revokeConnection(crashed, f.reload(t), accessB, domain.MailboxAccessConnectionRevoked); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if rows := f.releaseRows(t, f.connection.ID); len(rows) != 1 || !rows[0].StopPush || rows[0].StopWatchKey == "" {
		t.Fatalf("release = %+v", rows)
	}
	f.connectionIn(t, accessOrg+100, f.connection.AccountAddress, "watching-token")
	f.sync(t) // the new connection watches the same account
	f.advance(externalMailReleaseGrace + time.Second)
	f.sync(t)
	if stopped := f.source.Unsubscribed(); len(stopped) != 0 {
		t.Fatalf("the worker stopped the watch a live connection shares: %v", stopped)
	}
}

// TestExternalMailProviderReleaseHoldsNoUnrevocableGrant: a release keeps a sealed grant only for a
// connector whose revocation really revokes a provider grant; for any other it only stops the push
// subscription and holds no secret.
func TestExternalMailProviderReleaseHoldsNoUnrevocableGrant(t *testing.T) {
	f := newPushFixture(t)
	registry := fakeExternalMailRegistry(f.source)
	registry.connectors[1].RevokesGrant = false
	f.service.externalConnectors = registry
	outlook := f.addConnection(t, domain.ExternalMailProviderOutlook, "outlook-grant")
	f.sync(t)
	reloaded, err := f.service.externalMail.FindExternalMailConnection(context.Background(), accessOrg, outlook.ID)
	if err != nil {
		t.Fatal(err)
	}
	crashed, _ := withDeferredReleases(context.Background())
	if err = f.service.revokeConnection(crashed, reloaded, accessB, domain.MailboxAccessConnectionRevoked); err != nil {
		t.Fatal(err)
	}
	rows := f.releaseRows(t, outlook.ID)
	if len(rows) != 1 || !rows[0].StopPush || rows[0].EncryptedGrant != "" {
		t.Fatalf("release of a connector that cannot revoke grants = %+v", rows)
	}
}

// TestExternalMailProviderReleaseRunsOnce: a call right after the commit that comes after the worker
// already took the release does nothing, so the provider is never called twice.
func TestExternalMailProviderReleaseRunsOnce(t *testing.T) {
	f := newPushFixture(t)
	recording := &txRecordingSource{FakeExternalMailSource: f.source}
	f.service.externalConnectors = fakeExternalMailRegistry(recording)
	f.sync(t)
	crashed, late := withDeferredReleases(context.Background())
	if err := f.service.revokeConnection(crashed, f.reload(t), accessB, domain.MailboxAccessConnectionRevoked); err != nil {
		t.Fatal(err)
	}
	f.advance(externalMailReleaseGrace + time.Second)
	f.sync(t)
	before := recording.calls.Load()
	if before == 0 {
		t.Fatal("the worker did not perform the release")
	}
	late.run(context.Background())
	if after := recording.calls.Load(); after != before {
		t.Fatalf("the late call right after the commit called the provider again (%d calls, then %d)", before, after)
	}
}

// TestExternalMailProviderReleaseExpiresWithConnectorsOff: releases past their deadline are deleted,
// with their sealed grants, by the mail worker's purge pass even when no connector is enabled.
func TestExternalMailProviderReleaseExpiresWithConnectorsOff(t *testing.T) {
	f := newExternalFixture(t)
	if f.service.externalMailAvailable() {
		t.Fatal("external mail is on")
	}
	now := f.service.now().UTC()
	release := &domain.ExternalMailProviderRelease{
		OrganizationID: accessOrg, ConnectionID: f.connection.ID, Provider: domain.ExternalMailProviderGmail,
		AccountAddress: f.connection.AccountAddress, EncryptedGrant: f.connection.EncryptedRefreshToken,
		CreatedAt: now.Add(-25 * time.Hour), NextAttemptAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	if err := f.service.releases.CreateExternalMailProviderRelease(context.Background(), release); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.PurgeRevokedExternalMail(context.Background(), now, 20); err != nil {
		t.Fatalf("purge pass: %v", err)
	}
	if rows := f.releaseRows(t, f.connection.ID); len(rows) != 0 {
		t.Fatalf("an expired release kept its sealed grant with connectors off: %+v", rows)
	}
}

// TestExternalMailPushEndpointNeverWaitsForAConnection: a sync's save that holds the connection row
// (and is about to take the subscription) does not delay a notification: the endpoint records it on
// the subscription at once, without touching the connection. Once the save commits, the worker's
// promotion makes the connection due, and the save, taking the subscription afterwards, is never
// aborted as a deadlock. Two real transactions on PostgreSQL.
func TestExternalMailPushEndpointNeverWaitsForAConnection(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	f.advance(time.Minute)
	subscription := f.subscription(t, f.connection.ID)
	id, secret, _ := f.source.PushSubscription(externalTestToken)
	db := f.repo.(*repository).db
	save := db.Begin()
	t.Cleanup(func() { save.Rollback() })
	// The save holds the connection, as SaveExternalMailSync does.
	if err := save.Exec(`UPDATE external_mail_connections SET updated_at = now() WHERE id = ?`, f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, ExternalMailPushDelivery{
		Header: http.Header{}, Query: url.Values{}, Body: fakeNotificationBody(id, secret, ""), ClientIP: "40.0.0.1",
	})
	if took := time.Since(started); err != nil || took > time.Second {
		t.Fatalf("the endpoint answered %v after %v while a connection row was held", err, took)
	}
	if pending := f.subscription(t, f.connection.ID); pending.PendingAt == nil {
		t.Fatal("the notification was not recorded")
	}
	// The save then takes the subscription, as savePushUpkeep does, and commits.
	if err = save.Exec(`UPDATE external_mail_subscriptions SET updated_at = now() WHERE id = ?`, subscription.ID).Error; err != nil {
		t.Fatalf("the save lost its transaction: %v", err)
	}
	if err = save.Commit().Error; err != nil {
		t.Fatalf("the save did not commit: %v", err)
	}
	now := f.service.now().UTC()
	f.promote(t)
	if next := f.nextSyncAt(t, f.connection.ID); !next.Equal(now) {
		t.Fatalf("after the save the worker did not make the connection due: %v", next)
	}
}

// TestExternalMailPushEndpointAsksAgainWhileASaveHoldsTheSubscription: when a sync's save holds the
// subscription row itself, the endpoint does not wait for it either: it answers 503 with Retry-After
// at once, and the provider's redelivery is recorded.
func TestExternalMailPushEndpointAsksAgainWhileASaveHoldsTheSubscription(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	subscription := f.subscription(t, f.connection.ID)
	id, secret, _ := f.source.PushSubscription(externalTestToken)
	db := f.repo.(*repository).db
	save := db.Begin()
	t.Cleanup(func() { save.Rollback() })
	if err := save.Exec(`UPDATE external_mail_subscriptions SET updated_at = now() WHERE id = ?`, subscription.ID).Error; err != nil {
		t.Fatal(err)
	}
	delivery := ExternalMailPushDelivery{Header: http.Header{}, Query: url.Values{}, Body: fakeNotificationBody(id, secret, ""), ClientIP: "40.0.0.1"}
	started := time.Now()
	_, err := f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, delivery)
	var retry *RetryAfterError
	if took := time.Since(started); !errors.As(err, &retry) || !errors.Is(err, domain.ErrExternalMailPushBusy) || took > time.Second {
		t.Fatalf("while the subscription was held the endpoint answered %v after %v", err, took)
	}
	if err = save.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, delivery); err != nil {
		t.Fatalf("the redelivery = %v", err)
	}
	if pending := f.subscription(t, f.connection.ID); pending.PendingAt == nil {
		t.Fatal("the redelivered notification was not recorded")
	}
}

// failingSaveRepository fails the next sync save, as a lost transaction does.
type failingSaveRepository struct {
	domain.ExternalMailRepository
	fail *atomic.Bool
}

func (r failingSaveRepository) SaveExternalMailSync(ctx context.Context, result domain.ExternalMailSyncResult) ([]*domain.MailMessage, bool, error) {
	if r.fail.CompareAndSwap(true, false) {
		return nil, false, errors.New("could not serialize access due to concurrent update")
	}
	return r.ExternalMailRepository.SaveExternalMailSync(ctx, result)
}

// TestExternalMailSyncKeepsARotatedGrantWhenTheSaveFails: when a sync's save fails after the provider
// rotated the grant (Outlook rotates on every use), the new grant is still stored.
func TestExternalMailSyncKeepsARotatedGrantWhenTheSaveFails(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	fail := &atomic.Bool{}
	fail.Store(true)
	f.service.externalMail = failingSaveRepository{ExternalMailRepository: f.service.externalMail, fail: fail}
	f.source.RotateOnNextFetch(externalTestToken, "rotated-grant")
	if _, err := f.service.SyncExternalMail(context.Background(), 20); err == nil {
		t.Fatal("the failed save went unreported")
	}
	grant, err := f.service.encryptor.DecryptString(f.reload(t).EncryptedRefreshToken)
	if err != nil || grant != "rotated-grant" {
		t.Fatalf("after a failed save the stored grant is %q (%v), want the rotated one", grant, err)
	}
}

// TestExternalMailSyncKeepsAReconnectOverAFailedSave: a member who reconnects while a sync runs keeps
// the new grant even when that sync's save fails after the provider rotated the old one: the rotated
// grant is stored only over the grant the sync started with.
func TestExternalMailSyncKeepsAReconnectOverAFailedSave(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	fail := &atomic.Bool{}
	fail.Store(true)
	f.service.externalMail = failingSaveRepository{ExternalMailRepository: f.service.externalMail, fail: fail}
	reconnected, err := f.service.sealRefreshToken("reconnected-grant")
	if err != nil {
		t.Fatal(err)
	}
	f.service.externalConnectors = fakeExternalMailRegistry(notifyingSource{FakeExternalMailSource: f.source, during: func() {
		if _, rebindErr := f.service.externalMail.RebindExternalMailConnection(context.Background(), domain.ExternalMailRebind{
			ConnectionID: f.connection.ID, EncryptedRefreshToken: reconnected, Now: f.service.now().UTC(),
		}); rebindErr != nil {
			t.Errorf("reconnect: %v", rebindErr)
		}
	}})
	f.source.RotateOnNextFetch(externalTestToken, "rotated-grant")
	if _, err = f.service.SyncExternalMail(context.Background(), 20); err == nil {
		t.Fatal("the failed save went unreported")
	}
	grant, err := f.service.encryptor.DecryptString(f.reload(t).EncryptedRefreshToken)
	if err != nil || grant != "reconnected-grant" {
		t.Fatalf("after the reconnect and a failed save the stored grant is %q (%v), want the reconnected one", grant, err)
	}
}

// rebindingStopSource rebinds the connection while the worker's stop call is at the provider, the
// slow part of a release.
type rebindingStopSource struct {
	*FakeExternalMailSource
	during func()
}

func (s rebindingStopSource) Unsubscribe(ctx context.Context, request ExternalMailPushRequest) error {
	if s.during != nil {
		s.during()
	}
	return s.FakeExternalMailSource.Unsubscribe(ctx, request)
}

// TestExternalMailProviderReleaseRechecksBeforeTheHandBack: a member who binds the account again
// while the worker is stopping the old subscription keeps the new grant: the conditions are checked
// again right before the hand-back.
func TestExternalMailProviderReleaseRechecksBeforeTheHandBack(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	crashed, _ := withDeferredReleases(context.Background())
	if err := f.service.revokeConnection(crashed, f.reload(t), accessB, domain.MailboxAccessConnectionRevoked); err != nil {
		t.Fatal(err)
	}
	rebound, err := f.service.sealRefreshToken("rebound-grant")
	if err != nil {
		t.Fatal(err)
	}
	once := false
	f.service.externalConnectors = fakeExternalMailRegistry(rebindingStopSource{FakeExternalMailSource: f.source, during: func() {
		if once {
			return
		}
		once = true
		// The rebind commits on its own; the release row it deletes is already in the worker's hands.
		if _, rebindErr := f.service.externalMail.RebindExternalMailConnection(context.Background(), domain.ExternalMailRebind{
			ConnectionID: f.connection.ID, EncryptedRefreshToken: rebound, ResetCursor: true, Now: f.service.now().UTC(),
		}); rebindErr != nil {
			t.Errorf("rebind: %v", rebindErr)
		}
	}})
	f.advance(externalMailReleaseGrace + time.Second)
	f.sync(t)
	if !once {
		t.Fatal("the worker never stopped the subscription")
	}
	if f.source.Revoked(externalTestToken) {
		t.Fatal("the grant was handed back although the account was bound again during the stop")
	}
}

// TestExternalMailProviderReleaseWaitsForAPendingSignIn: while the member has a sign-in pending for
// the provider, the release is postponed, not dropped; once the sign-in is gone it runs.
func TestExternalMailProviderReleaseWaitsForAPendingSignIn(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	crashed, _ := withDeferredReleases(context.Background())
	if err := f.service.revokeConnection(crashed, f.reload(t), accessB, domain.MailboxAccessConnectionRevoked); err != nil {
		t.Fatal(err)
	}
	now := f.service.now().UTC()
	if err := f.service.externalMail.CreateExternalMailAuthorization(context.Background(), &domain.ExternalMailAuthorization{
		OrganizationID: accessOrg, UserID: accessB, Provider: domain.ExternalMailProviderGmail,
		StateHash: pushHash("pending-state"), SealedVerifier: "sealed", BackfillDays: 14,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	f.advance(externalMailReleaseGrace + time.Second)
	f.sync(t)
	if f.source.Revoked(externalTestToken) || len(f.source.Unsubscribed()) != 0 {
		t.Fatal("a release ran while the member was signing in again")
	}
	if rows := f.releaseRows(t, f.connection.ID); len(rows) != 1 || rows[0].Attempts != 1 {
		t.Fatalf("the release was not postponed: %+v", rows)
	}
	f.advance(10 * time.Minute)
	f.runReleaseWorker(t)
	if !f.source.Revoked(externalTestToken) {
		t.Fatal("the release never ran once the sign-in expired")
	}
}

// TestExternalMailPushRecordedDuringTheSavesMessageWork: a notification that arrives while a sync's
// save is still doing its per-message work is recorded and answered normally, not asked to come again,
// and it still causes one more sync after the save.
func TestExternalMailPushRecordedDuringTheSavesMessageWork(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	received := f.service.now().UTC()
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", received), externalMessage("g-2", "Two", received))
	f.advance(config.DefaultExternalMailPushPollInterval)
	id, secret, _ := f.source.PushSubscription(externalTestToken)
	var during error
	notified := false
	f.service.afterStoredHook = func() {
		if notified {
			return
		}
		notified = true
		f.advance(time.Second)
		_, during = f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, ExternalMailPushDelivery{
			Header: http.Header{}, Query: url.Values{}, Body: fakeNotificationBody(id, secret, ""), ClientIP: "40.0.0.1",
		})
	}
	f.sync(t)
	f.service.afterStoredHook = nil
	if !notified || during != nil {
		t.Fatalf("a notification during the save's message work was answered %v", during)
	}
	if subscription := f.subscription(t, f.connection.ID); subscription.PendingAt == nil {
		t.Fatal("the notification during the save was answered by it")
	}
	f.advance(f.service.settings.ExternalMail.Sync.Spacing())
	f.sync(t)
	if runs := f.runs(t); runs[0].Trigger != domain.ExternalMailSyncTriggerPush {
		t.Fatalf("no sync followed the notification: newest run %+v", runs[0])
	}
}

// TestExternalMailPushReconcilesAtMostOnceAMinute: the stop of subscriptions of ended connections runs
// at most once a minute per process, whatever the worker's idle wait, and still runs.
func TestExternalMailPushReconcilesAtMostOnceAMinute(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_connections SET status = 'revoked', revoked_at = ? WHERE id = ?`,
		f.service.now().UTC(), f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	for range 10 {
		f.advance(2 * time.Second)
		f.sync(t)
	}
	if subscription := f.subscription(t, f.connection.ID); subscription.Status == domain.ExternalMailSubscriptionStopped {
		t.Fatal("the stop ran on every pass")
	}
	f.advance(externalMailPushReconcileEvery)
	f.sync(t)
	if subscription := f.subscription(t, f.connection.ID); subscription.Status != domain.ExternalMailSubscriptionStopped {
		t.Fatalf("the stop never ran: %+v", subscription)
	}
}

// TestExternalMailProviderReleaseOutlivesATenantAssignment: assigning the address from a tenant does
// not cancel the hand-back a member's own revoked grant is still owed: the tenant reads with its own
// credential, so the member's grant is still handed back.
func TestExternalMailProviderReleaseOutlivesATenantAssignment(t *testing.T) {
	tf := newTenantFixture(t)
	source := flakyRevokeSource{FakeExternalMailSource: tf.source, failing: &atomic.Bool{}, revokes: &atomic.Int32{}}
	source.failing.Store(true)
	registry := fakeExternalMailRegistry(source)
	registry.connectors[0].Tenant = tf.source
	tf.service.externalConnectors = registry
	own := tf.connectionIn(t, accessOrg, tenantAddress("alice"), "alice-own-grant")
	if err := tf.service.RevokeExternalMailConnection(context.Background(), tf.b, own.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if rows := tf.releaseRows(t, own.ID); len(rows) != 1 || rows[0].EncryptedGrant == "" {
		t.Fatalf("release of the member's grant = %+v", rows)
	}
	tf.mustAssign(t, personalRow("alice", accessB))
	if rows := tf.releaseRows(t, own.ID); len(rows) != 1 {
		t.Fatalf("the tenant assignment canceled the member's hand-back: %+v", rows)
	}
	source.failing.Store(false)
	tf.runReleaseWorker(t)
	if !tf.source.Revoked("alice-own-grant") {
		t.Fatal("the member's grant was never handed back")
	}
}
