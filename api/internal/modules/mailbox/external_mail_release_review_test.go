package mailbox

import (
	"context"
	"errors"
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

// TestExternalMailPushNotificationAndSaveShareALockOrder: a sync's save locks the connection and then
// its subscription; a notification for that subscription arriving meanwhile waits for the connection
// instead of taking the subscription first, so neither is aborted as a deadlock and the notification
// stays pending. Two real transactions on PostgreSQL.
func TestExternalMailPushNotificationAndSaveShareALockOrder(t *testing.T) {
	f := newPushFixture(t)
	f.sync(t)
	subscription := f.subscription(t, f.connection.ID)
	db := f.repo.(*repository).db
	save := db.Begin()
	t.Cleanup(func() { save.Rollback() })
	// The save holds the connection, as SaveExternalMailSync does.
	if err := save.Exec(`UPDATE external_mail_connections SET updated_at = now() WHERE id = ?`, f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	notified := make(chan error, 1)
	go func() {
		_, err := f.service.pushStore.MarkExternalMailPush(context.Background(), domain.ExternalMailPushMark{
			SubscriptionIDs: []uint{subscription.ID}, Now: f.service.now().UTC(), Window: time.Second, Spacing: time.Second,
		})
		notified <- err
	}()
	// The test pool has two connections, both busy now (the save and the notification), so the
	// notification's lock wait cannot be watched from here; it starts at once and is given time to
	// reach its first lock.
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-notified:
		t.Fatalf("the notification did not wait for the save's connection lock: %v", err)
	default:
	}
	// The save then takes the subscription, as savePushUpkeep does.
	if err := save.Exec(`UPDATE external_mail_subscriptions SET updated_at = now() WHERE id = ?`, subscription.ID).Error; err != nil {
		t.Fatalf("the save lost its transaction: %v", err)
	}
	if err := save.Commit().Error; err != nil {
		t.Fatalf("the save did not commit: %v", err)
	}
	select {
	case err := <-notified:
		if err != nil {
			t.Fatalf("the notification failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the notification never finished")
	}
	if after := f.subscription(t, f.connection.ID); after.PendingAt == nil {
		t.Fatal("the notification was lost")
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
