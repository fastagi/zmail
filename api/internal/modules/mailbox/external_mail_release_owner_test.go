package mailbox

import (
	"context"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// Tests for acceptance round 3, defect D2: the release of a purged connection is held back only by
// its own member's pending sign-in (anyone's only for a shared connection).

// revokedAndPurged revokes the fixture connection with the call right after the commit lost (the
// release stays for the worker), then purges its copies, which deletes the connection row.
func (f *externalFixture) revokedAndPurged(t *testing.T) {
	t.Helper()
	f.sync(t)
	crashed, _ := withDeferredReleases(context.Background())
	if err := f.service.revokeConnection(crashed, f.reload(t), accessB, domain.MailboxAccessConnectionRevoked); err != nil {
		t.Fatal(err)
	}
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
}

// pendingSignIn starts a Gmail sign-in for the user in the fixture organization and leaves it pending.
func (f *externalFixture) pendingSignIn(t *testing.T, userID uint, state string) {
	t.Helper()
	now := f.service.now().UTC()
	if err := f.service.externalMail.CreateExternalMailAuthorization(context.Background(), &domain.ExternalMailAuthorization{
		OrganizationID: accessOrg, UserID: userID, Provider: domain.ExternalMailProviderGmail,
		StateHash: pushHash(state), SealedVerifier: "sealed", BackfillDays: 14,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

// runDueReleases lets the worker pick up the releases due after the grace period.
func (f *externalFixture) runDueReleases(t *testing.T) {
	t.Helper()
	f.advance(externalMailReleaseGrace + time.Second)
	f.sync(t)
}

// TestExternalMailPurgedReleaseIgnoresOtherMembersSignIns: once a member's own connection is purged,
// another member's pending sign-in for the provider does not hold its hand-back back.
func TestExternalMailPurgedReleaseIgnoresOtherMembersSignIns(t *testing.T) {
	f := newPushFixture(t)
	f.revokedAndPurged(t)
	f.pendingSignIn(t, accessC, "other-member")
	f.runDueReleases(t)
	if !f.source.Revoked(externalTestToken) {
		t.Fatalf("another member's sign-in postponed the hand-back of a purged personal connection: %+v",
			f.releaseRows(t, f.connection.ID))
	}
	if rows := f.releaseRows(t, f.connection.ID); len(rows) != 0 {
		t.Fatalf("a done release was kept: %+v", rows)
	}
}

// TestExternalMailPurgedReleaseWaitsForItsMembersSignIn: the member's own pending sign-in still holds
// the hand-back of their purged connection back, and it runs once the sign-in is gone.
func TestExternalMailPurgedReleaseWaitsForItsMembersSignIn(t *testing.T) {
	f := newPushFixture(t)
	f.revokedAndPurged(t)
	f.pendingSignIn(t, accessB, "own-member")
	f.runDueReleases(t)
	if f.source.Revoked(externalTestToken) {
		t.Fatal("the hand-back ran while its member was signing in again")
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

// TestExternalMailReleaseRecordsItsOwner: the release carries its owning member for a personal
// connection and the shared marker for a shared one, set when it is recorded.
func TestExternalMailReleaseRecordsItsOwner(t *testing.T) {
	f := newPushFixture(t)
	f.revokedAndPurged(t)
	rows := f.releaseRows(t, f.connection.ID)
	if rows[0].OwnerScope != string(domain.ExternalMailReleaseOwnerMember) || rows[0].OwnerUserID == nil || *rows[0].OwnerUserID != accessB {
		t.Fatalf("personal release owner = %q %v", rows[0].OwnerScope, rows[0].OwnerUserID)
	}

	shared := newPushFixture(t)
	shared.makeShared(t)
	shared.sync(t)
	crashed, _ := withDeferredReleases(context.Background())
	if err := shared.service.revokeConnection(crashed, shared.reload(t), accessAdmin, domain.MailboxAccessConnectionRevoked); err != nil {
		t.Fatal(err)
	}
	rows = shared.releaseRows(t, shared.connection.ID)
	if len(rows) != 1 || rows[0].OwnerScope != string(domain.ExternalMailReleaseOwnerShared) || rows[0].OwnerUserID != nil {
		t.Fatalf("shared release = %+v", rows)
	}
}

// makeShared turns the fixture connection into the organization's shared connection.
func (f *externalFixture) makeShared(t *testing.T) {
	t.Helper()
	if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_connections SET shared = true, owner_user_id = NULL WHERE id = ?`,
		f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
}

// TestExternalMailSharedReleaseWaitsForAnyonesSignIn: a shared connection's release is held back by
// any member's pending sign-in, also once the connection row is gone.
func TestExternalMailSharedReleaseWaitsForAnyonesSignIn(t *testing.T) {
	for _, purged := range []bool{false, true} {
		f := newPushFixture(t)
		f.makeShared(t)
		f.sync(t)
		crashed, _ := withDeferredReleases(context.Background())
		if err := f.service.revokeConnection(crashed, f.reload(t), accessAdmin, domain.MailboxAccessConnectionRevoked); err != nil {
			t.Fatal(err)
		}
		if purged {
			if err := f.repo.(*repository).db.Exec(`DELETE FROM external_mail_connections WHERE id = ?`, f.connection.ID).Error; err != nil {
				t.Fatal(err)
			}
		}
		f.pendingSignIn(t, accessC, "anyone")
		f.runDueReleases(t)
		if f.source.Revoked(externalTestToken) {
			t.Fatalf("purged %v: a shared connection's hand-back ran while a member was signing in", purged)
		}
		if rows := f.releaseRows(t, f.connection.ID); len(rows) != 1 || rows[0].Attempts != 1 {
			t.Fatalf("purged %v: the release was not postponed: %+v", purged, rows)
		}
	}
}

// TestExternalMailReleaseOfADeletedMember: once the member's user account is deleted (the owner is
// gone), no pending sign-in holds the hand-back of their purged connection back.
func TestExternalMailReleaseOfADeletedMember(t *testing.T) {
	f := newPushFixture(t)
	f.revokedAndPurged(t)
	if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_provider_releases SET owner_user_id = NULL WHERE connection_id = ?`,
		f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	f.pendingSignIn(t, accessB, "deleted-owner")
	f.pendingSignIn(t, accessC, "other-member")
	f.runDueReleases(t)
	if !f.source.Revoked(externalTestToken) {
		t.Fatalf("a deleted member's release was postponed: %+v", f.releaseRows(t, f.connection.ID))
	}
}

// TestExternalMailReleaseOfUnknownOwnerKeepsTheConservativeRule: a release recorded before its owner
// was kept (backfilled as unknown) is still held back by anyone's sign-in once its connection is gone.
func TestExternalMailReleaseOfUnknownOwnerKeepsTheConservativeRule(t *testing.T) {
	f := newPushFixture(t)
	f.revokedAndPurged(t)
	if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_provider_releases SET owner_scope = 'unknown', owner_user_id = NULL
		WHERE connection_id = ?`, f.connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	f.pendingSignIn(t, accessC, "other-member")
	f.runDueReleases(t)
	if f.source.Revoked(externalTestToken) {
		t.Fatal("a release of unknown owner ran while a member was signing in")
	}
	if rows := f.releaseRows(t, f.connection.ID); len(rows) != 1 || rows[0].Attempts != 1 {
		t.Fatalf("the release was not postponed: %+v", rows)
	}
}
