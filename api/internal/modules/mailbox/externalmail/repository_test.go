package externalmail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestPureHelpers(t *testing.T) {
	t.Parallel()

	if optionalText("") != nil {
		t.Fatalf("optionalText(\"\") should return nil")
	}
	if ptr := optionalText("hello"); ptr == nil || *ptr != "hello" {
		t.Fatalf("optionalText(\"hello\") = %v", ptr)
	}

	if optionalUserID(0) != nil {
		t.Fatalf("optionalUserID(0) should return nil")
	}
	if ptr := optionalUserID(42); ptr == nil || *ptr != 42 {
		t.Fatalf("optionalUserID(42) = %v", ptr)
	}

	t1 := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if laterTime(nil, nil) != nil {
		t.Fatalf("laterTime(nil, nil) should return nil")
	}
	if got := laterTime(nil, &t1); got != &t1 {
		t.Fatalf("laterTime(nil, &t1) = %v", got)
	}
	if got := laterTime(&t1, nil); got != &t1 {
		t.Fatalf("laterTime(&t1, nil) = %v", got)
	}
	if got := laterTime(&t1, &t2); got != &t2 {
		t.Fatalf("laterTime(&t1, &t2) = %v, want %v", got, &t2)
	}
	if got := laterTime(&t2, &t1); got != &t2 {
		t.Fatalf("laterTime(&t2, &t1) = %v, want %v", got, &t2)
	}

	if (mailboxRow{}).TableName() != "mailboxes" {
		t.Fatalf("unexpected mailboxRow TableName")
	}
	if got := (&ExternalMailSyncRunPO{TriggeredBy: ""}).triggerOrPoll(); got != "poll" {
		t.Fatalf("empty trigger should default to poll, got %q", got)
	}
	if got := (&ExternalMailSyncRunPO{TriggeredBy: "push"}).triggerOrPoll(); got != "push" {
		t.Fatalf("explicit trigger should be preserved, got %q", got)
	}
	if got := releaseOwnerScope(""); got != domain.ExternalMailReleaseOwnerUnknown {
		t.Fatalf("empty releaseOwnerScope should default to unknown, got %q", got)
	}
	if got := releaseOwnerScope(domain.ExternalMailReleaseOwnerShared); got != domain.ExternalMailReleaseOwnerShared {
		t.Fatalf("explicit releaseOwnerScope should be preserved, got %q", got)
	}
}

func TestRepositoryNilDBServiceUnavailable(t *testing.T) {
	t.Parallel()

	repo := NewRepository(nil, Errors{}, Hooks{})
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	// Connection repository methods
	if err := repo.CreateExternalMailConnection(ctx, &domain.ExternalMailConnection{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CreateExternalMailConnection err = %v", err)
	}
	if _, err := repo.ClaimExternalMailConnections(ctx, domain.ExternalMailClaim{Now: now, Limit: 5}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ClaimExternalMailConnections err = %v", err)
	}
	if _, _, err := repo.SaveExternalMailSync(ctx, domain.ExternalMailSyncResult{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("SaveExternalMailSync err = %v", err)
	}
	if _, err := repo.ListExternalMailConnections(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListExternalMailConnections err = %v", err)
	}
	if _, err := repo.ListExternalMailConnectionDirectory(ctx, 1, 0, 10); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListExternalMailConnectionDirectory err = %v", err)
	}
	if _, err := repo.FindExternalMailConnectionByMailbox(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindExternalMailConnectionByMailbox err = %v", err)
	}
	if _, err := repo.FindExternalMailConnection(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindExternalMailConnection err = %v", err)
	}
	if err := repo.RevokeExternalMailConnection(ctx, 1, 2, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("RevokeExternalMailConnection err = %v", err)
	}
	if _, err := repo.FindExternalMailConnectionByAccount(ctx, 1, domain.ExternalMailProviderGmail, "a@example.com"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindExternalMailConnectionByAccount err = %v", err)
	}
	if _, err := repo.RebindExternalMailConnection(ctx, domain.ExternalMailRebind{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("RebindExternalMailConnection err = %v", err)
	}
	if err := repo.SetConnectedMailboxSend(ctx, 1, 2, true); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("SetConnectedMailboxSend err = %v", err)
	}
	if err := repo.CreateExternalMailAuthorization(ctx, &domain.ExternalMailAuthorization{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CreateExternalMailAuthorization err = %v", err)
	}
	if _, err := repo.TakeExternalMailAuthorization(ctx, "hash", now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("TakeExternalMailAuthorization err = %v", err)
	}
	if err := repo.LockExternalMailBindings(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("LockExternalMailBindings err = %v", err)
	}
	if _, err := repo.CountLiveExternalMailConnections(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CountLiveExternalMailConnections err = %v", err)
	}
	if err := repo.LockSharedExternalMailBindings(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("LockSharedExternalMailBindings err = %v", err)
	}
	if _, err := repo.CountLiveSharedExternalMailConnections(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CountLiveSharedExternalMailConnections err = %v", err)
	}
	if _, err := repo.ListSharedExternalMailConnections(ctx, 1, 2, true, 10); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListSharedExternalMailConnections err = %v", err)
	}

	// Push repository methods
	if _, err := repo.FindExternalMailSubscription(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindExternalMailSubscription err = %v", err)
	}
	if _, err := repo.ListExternalMailSubscriptions(ctx, []uint{1}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListExternalMailSubscriptions err = %v", err)
	}
	if _, err := repo.MatchExternalMailSubscriptions(ctx, domain.ExternalMailProviderGmail, []string{"k"}, 5); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("MatchExternalMailSubscriptions err = %v", err)
	}
	if _, err := repo.MarkExternalMailPushDegraded(ctx, 1, now, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("MarkExternalMailPushDegraded err = %v", err)
	}
	if _, err := repo.MarkExternalMailPush(ctx, domain.ExternalMailPushMark{SubscriptionIDs: []uint{1}}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("MarkExternalMailPush err = %v", err)
	}
	if err := repo.KeepExternalMailRotatedToken(ctx, 1, "prev", "sealed"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("KeepExternalMailRotatedToken err = %v", err)
	}
	if _, err := repo.PromotePendingExternalMailPush(ctx, now, time.Minute, time.Second, 10); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("PromotePendingExternalMailPush err = %v", err)
	}
	if err := repo.SaveExternalMailSubscription(ctx, domain.ExternalMailSubscriptionSave{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("SaveExternalMailSubscription err = %v", err)
	}
	if err := repo.RecordExternalMailPushSync(ctx, domain.ExternalMailSubscriptionSynced{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("RecordExternalMailPushSync err = %v", err)
	}
	if _, err := repo.StopExternalMailSubscription(ctx, 1, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("StopExternalMailSubscription err = %v", err)
	}
	if _, err := repo.StopOrphanedExternalMailSubscriptions(ctx, now, 10); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("StopOrphanedExternalMailSubscriptions err = %v", err)
	}
	if _, err := repo.CountLiveExternalMailWatchers(ctx, domain.ExternalMailProviderGmail, "k", 0); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CountLiveExternalMailWatchers err = %v", err)
	}

	// Release repository methods
	if err := repo.CreateExternalMailProviderRelease(ctx, &domain.ExternalMailProviderRelease{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CreateExternalMailProviderRelease err = %v", err)
	}
	if _, err := repo.ClaimExternalMailProviderReleases(ctx, now, now, 5); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ClaimExternalMailProviderReleases err = %v", err)
	}
	if _, err := repo.ClaimExternalMailProviderRelease(ctx, 1, now, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ClaimExternalMailProviderRelease err = %v", err)
	}
	if err := repo.DeleteExternalMailProviderRelease(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("DeleteExternalMailProviderRelease err = %v", err)
	}
	if err := repo.DeferExternalMailProviderRelease(ctx, 1, 1, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("DeferExternalMailProviderRelease err = %v", err)
	}
	if _, err := repo.DeleteExpiredExternalMailProviderReleases(ctx, now, 10); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("DeleteExpiredExternalMailProviderReleases err = %v", err)
	}
	if err := repo.DeleteExternalMailProviderReleasesForAccount(ctx, 1, domain.ExternalMailProviderGmail, "a@example.com", false); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("DeleteExternalMailProviderReleasesForAccount err = %v", err)
	}
	if _, err := repo.CountLiveExternalMailAccountGrants(ctx, domain.ExternalMailProviderGmail, "a@example.com", 0); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CountLiveExternalMailAccountGrants err = %v", err)
	}
	if _, err := repo.CountPendingExternalMailAuthorizations(ctx, 1, domain.ExternalMailProviderGmail, 2, 0, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CountPendingExternalMailAuthorizations err = %v", err)
	}

	// Tenant repository methods
	if err := repo.CreateExternalMailTenant(ctx, &domain.ExternalMailTenant{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CreateExternalMailTenant err = %v", err)
	}
	if _, err := repo.FindExternalMailTenant(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindExternalMailTenant err = %v", err)
	}
	if _, err := repo.FindLiveExternalMailTenant(ctx, domain.ExternalMailProviderGmail, "example.com"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindLiveExternalMailTenant err = %v", err)
	}
	if _, err := repo.ListExternalMailTenants(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListExternalMailTenants err = %v", err)
	}
	if _, err := repo.CountLiveExternalMailTenants(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CountLiveExternalMailTenants err = %v", err)
	}
	if err := repo.LockActiveExternalMailTenant(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("LockActiveExternalMailTenant err = %v", err)
	}
	if err := repo.RecordAddressHolder(ctx, 1, domain.ExternalMailProviderGmail, "a@example.com", "dir", 2, 3, nil, 0, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("RecordAddressHolder err = %v", err)
	}
	if _, err := repo.FindPersonalAssignment(ctx, 1, domain.ExternalMailProviderGmail, "a@example.com", "dir"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindPersonalAssignment err = %v", err)
	}
	if err := repo.MarkPersonalBoundary(ctx, 1, 2, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("MarkPersonalBoundary err = %v", err)
	}
}
