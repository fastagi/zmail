package mailbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// deliverAlice delivers one message to alice@ received now and advances the clock past it.
func (f *tenantFixture) deliverAlice(subject string) {
	message := externalMessage("id-"+strings.ReplaceAll(subject, " ", "-"), subject, f.service.now())
	// The account behind the tenant token and behind a member's own sign-in ("fake-alice") is one.
	f.source.Deliver(TenantMailboxToken(tenantAddress("alice")), message)
	f.source.Deliver("fake-alice", message)
	f.advance(time.Minute)
}

// TestTenantFloorSurvivesReassignment (N4, probes P3/P4): once B's address went to C through a handover
// (after B left, or while B was a member) and the mailbox was deleted, C never reads B's mail, not
// after a first assignment and not after C is unassigned (or C's mailbox deleted) and assigned again.
func TestTenantFloorSurvivesReassignment(t *testing.T) {
	for _, start := range []string{"leave then handover", "handover while a member"} {
		for _, reset := range []string{"unassign", "delete"} {
			t.Run(start+", "+reset, func(t *testing.T) {
				f := newTenantFixture(t)
				ctx := context.Background()
				alice := f.mustAssign(t, personalRow("alice", accessB))[0]
				f.deliverAlice("B private")
				f.sync(t)
				if start == "leave then handover" {
					f.leave(t, f.b, f.advance(time.Minute))
				}
				f.advance(time.Minute)
				if _, err := f.service.HandOverMailbox(ctx, f.admin, alice.MailboxID, accessC); err != nil {
					t.Fatalf("handover: %v", err)
				}
				if err := f.service.DeleteMailbox(ctx, f.admin, alice.MailboxID); err != nil {
					t.Fatalf("delete: %v", err)
				}
				first := f.assign(t, f.admin, personalRow("alice", accessC))[0]
				if first.Status != TenantAssignmentCreated {
					t.Fatalf("first assignment to C = %+v", first)
				}
				f.advance(3 * time.Minute)
				f.sync(t)
				if hasSubject(f.readableSubjects(t, f.c, first.Connection.MailboxID), "B private") {
					t.Fatal("C reads B's mail after the first assignment")
				}
				if reset == "unassign" {
					if err := f.service.RevokeExternalMailConnection(ctx, f.admin, first.Connection.ID); err != nil {
						t.Fatalf("unassign: %v", err)
					}
				} else if err := f.service.DeleteMailbox(ctx, f.admin, first.Connection.MailboxID); err != nil {
					t.Fatalf("delete C's mailbox: %v", err)
				}
				again := f.assign(t, f.admin, personalRow("alice", accessC))[0]
				if again.Connection == nil {
					t.Fatalf("second assignment to C = %+v", again)
				}
				f.advance(3 * time.Minute)
				f.sync(t)
				if subjects := f.readableSubjects(t, f.c, again.Connection.MailboxID); hasSubject(subjects, "B private") {
					t.Fatalf("C reads B's mail after being assigned again: %v", subjects)
				}
				if n := f.count(t, `SELECT COUNT(*) FROM mail_messages WHERE mailbox_id = ? AND subject = 'B private'`,
					again.Connection.MailboxID); n != 0 {
					t.Fatalf("%d copies of B's mail stored for C", n)
				}
			})
		}
	}
}

// TestTenantReauthorizedTenantRestoresSameHolder (N5): after the tenant is revoked and authorized again
// (a new tenant id), assigning the address back to its holder restores the same mailbox with its mail.
func TestTenantReauthorizedTenantRestoresSameHolder(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	f.deliverAlice("Before the revocation")
	f.sync(t)
	if err := f.service.RevokeExternalMailTenant(ctx, f.owner, f.tenant.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	result, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, googleTenantInput())
	if err != nil || !result.Created {
		t.Fatalf("re-authorize = %+v, err %v", result, err)
	}
	f.tenant = result.Tenant.Tenant
	restored := f.assign(t, f.admin, personalRow("alice", accessB))[0]
	if restored.Status != TenantAssignmentRestored || restored.Connection.ID != alice.ID ||
		restored.Connection.TenantID == nil || *restored.Connection.TenantID != f.tenant.ID {
		t.Fatalf("assigning back to B = %+v", restored)
	}
	mailbox, err := f.repo.FindMailbox(ctx, accessOrg, alice.MailboxID)
	if err != nil || mailbox.ExternalTenantID == nil || *mailbox.ExternalTenantID != f.tenant.ID {
		t.Fatalf("mailbox = %+v, err %v", mailbox, err)
	}
	if !hasSubject(f.readableSubjects(t, f.b, alice.MailboxID), "Before the revocation") {
		t.Fatal("B lost the mail of the restored mailbox")
	}
}

// TestTenantFloorDropsUndatedMail (N6 a): with a floor set, a message without a provider receive time
// is dropped rather than stored as received now.
func TestTenantFloorDropsUndatedMail(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	f.advance(time.Minute)
	if _, err := f.service.HandOverMailbox(ctx, f.admin, alice.MailboxID, accessC); err != nil {
		t.Fatalf("handover: %v", err)
	}
	if err := f.service.DeleteMailbox(ctx, f.admin, alice.MailboxID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	f.source.Deliver(TenantMailboxToken(tenantAddress("alice")), ExternalMessage{
		ExternalID: "undated", FromAddress: "x@sender.test", Subject: "Undated", RFCMessageID: "undated@sender.test",
	})
	result := f.assign(t, f.admin, personalRow("alice", accessD))[0]
	f.advance(3 * time.Minute)
	f.sync(t)
	if hasSubject(f.readableSubjects(t, f.d, result.Connection.MailboxID), "Undated") {
		t.Fatal("an undated message bypassed the floor")
	}
}

// TestTenantSelfBindMatchesDirectoryID (N6 b): a self-binding whose provider reports the directory id of
// an earlier personal holder is refused even under another address.
func TestTenantSelfBindMatchesDirectoryID(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	f.mustAssign(t, personalRow("alice", accessB))
	// C signs in to the same directory user (dir-1), now reported as renamed@.
	if _, created, err := f.service.CompleteExternalMailAuthorization(ctx, f.c,
		f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail}), "renamed#dir-1"); !errors.Is(err, ErrExternalMailAccountTaken) || created {
		t.Fatalf("self-binding B's directory user = %v", err)
	}
}

// TestTenantWaitingMailboxIsNotDeleted (N6 c): a departed member's mailbox awaiting handover cannot be
// deleted, so a returning member keeps their copies.
func TestTenantWaitingMailboxIsNotDeleted(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	f.deliverAlice("Kept")
	f.sync(t)
	f.leave(t, f.b, f.advance(time.Minute))
	if err := f.service.DeleteMailbox(ctx, f.admin, alice.MailboxID); !errors.Is(err, ErrMailboxAccessConflict) {
		t.Fatalf("deleting a mailbox awaiting handover = %v, want conflict", err)
	}
	rejoined := f.rejoin(t, f.advance(time.Minute))
	if !hasSubject(f.readableSubjects(t, rejoined, alice.MailboxID), "Kept") {
		t.Fatal("the returning member lost their mail")
	}
}
