package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// readableSubjects lists the subjects viewer reads in mailboxID; nil when the mailbox is not readable.
func (f *tenantFixture) readableSubjects(t *testing.T, viewer domain.OrganizationContext, mailboxID uint) []string {
	t.Helper()
	messages, _, err := f.service.ListMessages(context.Background(), viewer, mailboxID, domain.MailMessageFilter{Page: 1, PerPage: 50})
	if err != nil {
		return nil
	}
	subjects := []string{}
	for _, message := range messages {
		subjects = append(subjects, message.Subject)
	}
	return subjects
}

func hasSubject(subjects []string, subject string) bool {
	for _, got := range subjects {
		if got == subject {
			return true
		}
	}
	return false
}

// TestTenantDepartedHolderIsNotReassigned (N1 path a): while a departed member's personal assignment
// awaits handover it cannot be disconnected, purged, or reassigned; after the handover and the
// mailbox's deletion, a new holder reads nothing received before the departure.
func TestTenantDepartedHolderIsNotReassigned(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	token := TenantMailboxToken(tenantAddress("alice"))
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	f.source.Deliver(token, externalMessage("m-1", "Before B left", f.service.now().Add(-time.Hour)))
	f.sync(t)

	left := f.advance(time.Hour)
	f.leave(t, f.b, left)
	f.advance(time.Hour)
	f.source.Deliver(token, externalMessage("m-2", "After B left", f.service.now()))
	f.sync(t)

	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, alice.ID); !errors.Is(err, ErrMailboxAccessConflict) {
		t.Fatalf("disconnecting an assignment awaiting handover = %v, want conflict", err)
	}
	if err := f.service.RevokeExternalMailConnection(ctx, f.admin, alice.ID); !errors.Is(err, ErrMailboxAccessConflict) {
		t.Fatalf("unassigning an assignment awaiting handover = %v, want conflict", err)
	}
	if result := f.assign(t, f.admin, personalRow("alice", accessC))[0]; result.ErrorCode != domain.CodeExternalMailAccountTaken {
		t.Fatalf("reassigning while it awaits handover = %+v, want taken", result)
	}

	if _, err := f.service.HandOverMailbox(ctx, f.admin, alice.MailboxID, accessC); err != nil {
		t.Fatalf("handover: %v", err)
	}
	if subjects := f.readableSubjects(t, f.c, alice.MailboxID); hasSubject(subjects, "Before B left") || !hasSubject(subjects, "After B left") {
		t.Fatalf("successor reads %v", subjects)
	}
	if err := f.service.DeleteMailbox(ctx, f.admin, alice.MailboxID); err != nil {
		t.Fatalf("delete the handed-over mailbox: %v", err)
	}
	result := f.assign(t, f.admin, personalRow("alice", accessD))[0]
	if result.Status != TenantAssignmentCreated {
		t.Fatalf("assigning to D after the handover = %+v", result)
	}
	f.advance(3 * time.Minute)
	f.sync(t)
	if subjects := f.readableSubjects(t, f.d, result.Connection.MailboxID); hasSubject(subjects, "Before B left") {
		t.Fatalf("D reads mail received before B left: %v", subjects)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM mail_messages WHERE mailbox_id = ? AND subject = 'Before B left'`, result.Connection.MailboxID); n != 0 {
		t.Fatalf("%d copies of mail received before the departure were stored for D", n)
	}
}

// TestTenantDisconnectedThenDepartedIsKept (N1 path a, disconnected first): an assignment disconnected
// while its member was there, whose member then left, is neither reassigned nor purged by the worker;
// a returning member finds the mailbox with its mail.
func TestTenantDisconnectedThenDepartedIsKept(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	f.source.Deliver(TenantMailboxToken(tenantAddress("alice")), externalMessage("m-1", "Before B left", f.service.now().Add(-time.Hour)))
	f.sync(t)
	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, alice.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	f.leave(t, f.b, f.advance(time.Hour))
	if result := f.assign(t, f.admin, personalRow("alice", accessC))[0]; result.ErrorCode != domain.CodeExternalMailAccountTaken {
		t.Fatalf("reassigning an assignment awaiting handover = %+v, want taken", result)
	}
	if _, err := f.service.PurgeExternalMailCopies(ctx, f.admin, alice.ID); !errors.Is(err, ErrMailboxAccessConflict) {
		t.Fatalf("purging an assignment awaiting handover = %v, want conflict", err)
	}
	f.advance(f.service.settings.ExternalMail.Sync.Retention() + 24*time.Hour)
	f.purge(t)
	if _, err := f.repo.FindMailbox(ctx, accessOrg, alice.MailboxID); err != nil {
		t.Fatalf("the worker purged a mailbox awaiting handover: %v", err)
	}
	rejoined := f.rejoin(t, f.advance(time.Minute))
	restored := f.assign(t, f.admin, personalRow("alice", accessB))[0]
	if restored.Status != TenantAssignmentRestored || !hasSubject(f.readableSubjects(t, rejoined, alice.MailboxID), "Before B left") {
		t.Fatalf("returning B = %+v, reads %v", restored, f.readableSubjects(t, rejoined, alice.MailboxID))
	}
}

// TestTenantHandoverBoundaryHoldsForLaterAssignees (N1 path b): after a handover while the holder is a
// member, deleting the mailbox and assigning the address to someone else never gives them mail from
// before the handover, and nobody else may self-bind it.
func TestTenantHandoverBoundaryHoldsForLaterAssignees(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	f.source.Deliver(TenantMailboxToken(tenantAddress("alice")), externalMessage("m-1", "B's private mail", f.service.now().Add(-time.Hour)))
	f.sync(t)
	f.advance(time.Hour)
	if _, err := f.service.HandOverMailbox(ctx, f.admin, alice.MailboxID, accessC); err != nil {
		t.Fatalf("handover: %v", err)
	}
	if err := f.service.DeleteMailbox(ctx, f.admin, alice.MailboxID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	result := f.assign(t, f.admin, personalRow("alice", accessD))[0]
	if result.Status != TenantAssignmentCreated {
		t.Fatalf("assign to D = %+v", result)
	}
	f.advance(3 * time.Minute)
	f.sync(t)
	if subjects := f.readableSubjects(t, f.d, result.Connection.MailboxID); hasSubject(subjects, "B's private mail") {
		t.Fatalf("D reads B's mail from before the handover: %v", subjects)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM mail_messages WHERE mailbox_id = ?`, result.Connection.MailboxID); n != 0 {
		t.Fatalf("%d copies from before the handover stored for D", n)
	}
}

// TestTenantHolderMatchesDirectoryID (N3 b): the holder rule follows the directory id when the
// provider reports another address for the same user.
func TestTenantHolderMatchesDirectoryID(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, alice.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	// The user dir-1 is now called alice.renamed@ at the provider.
	f.source.AddTenant(tenantDomain, ExternalMailDirectoryEntry{Address: tenantAddress("alice.renamed"), DisplayName: "Alice", DirectoryID: "dir-1"})
	if result := f.assign(t, f.admin, personalRow("alice.renamed", accessC))[0]; result.ErrorCode != domain.CodeExternalMailAccountTaken {
		t.Fatalf("assigning B's directory user under a new address to C = %+v, want taken", result)
	}
}

// TestTenantRoutesWhileSwitchedOff (N2): with tenant connect off an owner still lists and revokes a
// stored tenant; authorize, directory, and assignments stay unavailable.
func TestTenantRoutesWhileSwitchedOff(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	f.mustAssign(t, personalRow("alice", accessB))
	f.service.settings.ExternalMail.TenantsEnabled = false
	views, err := f.service.ListExternalMailTenants(ctx, f.admin)
	if err != nil || len(views) != 1 || views[0].Tenant.ID != f.tenant.ID {
		t.Fatalf("list while off = %+v, err %v", views, err)
	}
	if err = f.service.RevokeExternalMailTenant(ctx, f.owner, f.tenant.ID); err != nil {
		t.Fatalf("revoke while off: %v", err)
	}
	if f.reloadTenant(t).Status != domain.ExternalMailTenantRevoked || f.reloadTenant(t).EncryptedCredential != "" {
		t.Fatal("the stored credential survived the revocation")
	}
	if _, err = f.service.AuthorizeExternalMailTenant(ctx, f.owner, googleTenantInput()); !errors.Is(err, ErrExternalMailProviderUnavailable) {
		t.Fatalf("authorize while off = %v", err)
	}
	// With external mail switched off entirely, the stored tenant is still listed and revocable.
	f.service.settings.ExternalMail.Providers = nil
	if views, err = f.service.ListExternalMailTenants(ctx, f.admin); err != nil || len(views) != 1 {
		t.Fatalf("list with external mail off = %+v, err %v", views, err)
	}
}

// TestTenantManagementNeverShowsMail (N3 a): what owners and admins get from a personal assignment's
// connection (sync runs, purge, dev delivery) carries no message content or headers.
func TestTenantManagementNeverShowsMail(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	secret := "Quarterly salary review for Alice"
	f.source.Deliver(TenantMailboxToken(tenantAddress("alice")), ExternalMessage{
		ExternalID: "s-1", FromAddress: "hr@sender.test", FromName: "HR", To: []string{tenantAddress("alice")},
		Subject: secret, TextBody: "body " + secret, RFCMessageID: "s-1@sender.test", ReceivedAt: f.service.now(),
	})
	f.sync(t)
	runs, err := f.service.ListExternalMailSyncRuns(ctx, f.admin, alice.ID)
	if err != nil || len(runs) == 0 {
		t.Fatalf("admin sync runs = %d, err %v", len(runs), err)
	}
	status, body := f.opsCall(t, f.admin, http.MethodGet, "/external-mail-connections/"+itoa(alice.ID)+"/sync-runs")
	encoded, _ := json.Marshal(body)
	if status != http.StatusOK || strings.Contains(string(encoded), "salary") || strings.Contains(string(encoded), "hr@sender.test") {
		t.Fatalf("sync runs over HTTP = %d %s", status, encoded)
	}
	delivered, err := f.service.DeliverFakeExternalMail(ctx, f.admin, alice.ID, FakeExternalMailDeliveryInput{From: "x@sender.test", Subject: "dev"})
	if err != nil || strings.Contains(delivered, "salary") {
		t.Fatalf("dev delivery = %q, err %v", delivered, err)
	}
	if err = f.service.RevokeExternalMailConnection(ctx, f.admin, alice.ID); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	result, err := f.service.PurgeExternalMailCopies(ctx, f.admin, alice.ID)
	encoded, _ = json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), "salary") {
		t.Fatalf("purge = %s, err %v", encoded, err)
	}
	if subjects := f.readableSubjects(t, f.admin, alice.MailboxID); subjects != nil {
		t.Fatalf("admin reads %v", subjects)
	}
}

// TestTenantHandedOverAddressIsNotSelfBound (N1 rule 4): once an address was personally assigned,
// nobody but its last personal holder binds it with an own grant, whatever the case the provider
// reports, even after a handover and the mailbox's deletion.
func TestTenantHandedOverAddressIsNotSelfBound(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	f.source.Deliver(TenantMailboxToken(tenantAddress("alice")), externalMessage("m-1", "B's private mail", f.service.now().Add(-time.Hour)))
	f.sync(t)
	if _, err := f.service.HandOverMailbox(ctx, f.admin, alice.MailboxID, accessC); err != nil {
		t.Fatalf("handover: %v", err)
	}
	if err := f.service.DeleteMailbox(ctx, f.admin, alice.MailboxID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, created, err := f.service.CompleteExternalMailAuthorization(ctx, f.c,
		f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail}), "ALICE"); !errors.Is(err, ErrExternalMailAccountTaken) || created {
		t.Fatalf("C self-binding the handed-over address = %v", err)
	}
}
