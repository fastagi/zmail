package mailbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// TestTenantSwitchedOff: while EXTERNAL_MAIL_TENANTS_ENABLED is off authorizing, the directory, and
// assignments answer as an unavailable feature and no connector offers tenants, but mailboxes already
// assigned keep syncing (listing and revoking: TestTenantRoutesWhileSwitchedOff).
func TestTenantSwitchedOff(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	f.service.settings.ExternalMail.TenantsEnabled = false

	if _, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, googleTenantInput()); !errors.Is(err, ErrExternalMailProviderUnavailable) {
		t.Fatalf("authorize while off = %v", err)
	}
	if _, err := f.service.ExternalMailTenantDirectory(ctx, f.admin, f.tenant.ID, "", 10); !errors.Is(err, ErrExternalMailTenantNotFound) {
		t.Fatalf("directory while off = %v", err)
	}
	if _, err := f.service.AssignExternalMailTenantMailboxes(ctx, f.admin, f.tenant.ID, []ExternalMailTenantAssignmentInput{personalRow("bob", accessB)}); !errors.Is(err, ErrExternalMailTenantNotFound) {
		t.Fatalf("assign while off = %v", err)
	}
	for _, connector := range f.service.ListExternalMailConnectors(ctx) {
		if connector.Tenant != nil {
			t.Fatalf("connector %s offers tenants while off", connector.ID)
		}
	}
	f.source.Deliver(TenantMailboxToken(tenantAddress("alice")), externalMessage("a-1", "Still synced", f.service.now()))
	f.sync(t)
	messages, _, err := f.service.ListMessages(ctx, f.b, alice.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10})
	if err != nil || len(messages) != 1 {
		t.Fatalf("assigned mailbox while off has %d messages, err %v", len(messages), err)
	}
}

// TestTenantOtherOrganizationIDs: another organization's owner gets not found on every tenant route
// that takes an id.
func TestTenantOtherOrganizationIDs(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	other := viewerOf(accessOrg+1, accessAdmin, domain.OrganizationRoleOwner)
	if _, err := f.service.ExternalMailTenantDirectory(ctx, other, f.tenant.ID, "", 10); !errors.Is(err, ErrExternalMailTenantNotFound) {
		t.Errorf("directory = %v", err)
	}
	if _, err := f.service.AssignExternalMailTenantMailboxes(ctx, other, f.tenant.ID, []ExternalMailTenantAssignmentInput{personalRow("alice", accessB)}); !errors.Is(err, ErrExternalMailTenantNotFound) {
		t.Errorf("assign = %v", err)
	}
	if err := f.service.RevokeExternalMailTenant(ctx, other, f.tenant.ID); !errors.Is(err, ErrExternalMailTenantNotFound) {
		t.Errorf("revoke = %v", err)
	}
	if f.reloadTenant(t).Status != domain.ExternalMailTenantActive {
		t.Fatal("another organization changed the tenant")
	}
}

// TestTenantMalformedCursorKeepsTenant: a cursor the source did not issue is the caller's input; it
// never stops the tenant.
func TestTenantMalformedCursorKeepsTenant(t *testing.T) {
	f := newTenantFixture(t)
	if _, err := f.service.ExternalMailTenantDirectory(context.Background(), f.admin, f.tenant.ID, "https://evil.example/users", 10); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("malformed cursor = %v, want invalid input", err)
	}
	if f.reloadTenant(t).Status != domain.ExternalMailTenantActive {
		t.Fatal("a malformed cursor stopped the tenant")
	}
}

// TestTenantPersonalAssignmentIsNotRedirected: an address personally assigned to B goes to nobody
// else, personal, shared, or self-bound, while B is a member, even after a forced disconnect and a
// purge; it changes hands once B left.
func TestTenantPersonalAssignmentIsNotRedirected(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, alice.ID); err != nil {
		t.Fatalf("force disconnect: %v", err)
	}
	refused := func(stage string) {
		t.Helper()
		for i, row := range []ExternalMailTenantAssignmentInput{personalRow("alice", accessC), sharedRow("alice")} {
			if result := f.assign(t, f.admin, row)[0]; result.ErrorCode != domain.CodeExternalMailAccountTaken {
				t.Fatalf("%s, row %d = %+v, want taken", stage, i, result)
			}
		}
		if _, created, err := f.service.CompleteExternalMailAuthorization(ctx, f.c,
			f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail}), "alice"); !errors.Is(err, ErrExternalMailAccountTaken) || created {
			t.Fatalf("%s, C self-binding alice@ = %v", stage, err)
		}
	}
	refused("after the disconnect")
	if result, err := f.service.PurgeExternalMailCopies(ctx, f.admin, alice.ID); err != nil || !result.Completed {
		t.Fatalf("purge = %+v, err %v", result, err)
	}
	refused("after the purge")

	// B may have it back; once B left, it may go to someone else.
	if result := f.assign(t, f.admin, personalRow("alice", accessB))[0]; result.Status != TenantAssignmentCreated {
		t.Fatalf("back to B = %+v", result)
	}
	again := f.reloadConnection(t, f.assign(t, f.admin, personalRow("alice", accessB))[0].Connection.ID)
	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, again.ID); err != nil {
		t.Fatalf("force disconnect: %v", err)
	}
	// Once B left, the disconnected assignment awaits handover: it is not reassigned either; only the
	// handover moves it on (see TestTenantDepartedHolderIsNotReassigned).
	f.leave(t, f.b, f.advance(time.Minute))
	if result := f.assign(t, f.admin, personalRow("alice", accessC))[0]; result.ErrorCode != domain.CodeExternalMailAccountTaken {
		t.Fatalf("to C after B left = %+v, want taken", result)
	}
}

// TestTenantRevocationWaitsForAssignment: an assignment in progress holds the tenant row shared, so a
// revocation waits for it and then revokes what it created.
func TestTenantRevocationWaitsForAssignment(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	owner, err := f.service.currentMembership(ctx, accessOrg, accessB)
	if err != nil {
		t.Fatalf("membership: %v", err)
	}
	locked, release, assigned := make(chan struct{}), make(chan struct{}), make(chan uint, 1)
	go func() {
		assignErr := f.repo.InTransaction(ctx, func(tx context.Context) error {
			if lockErr := f.service.externalMail.LockActiveExternalMailTenant(tx, f.tenant.ID); lockErr != nil {
				return lockErr
			}
			connection, bindErr := f.service.bindTenantPersonal(tx, f.admin, externalBinding{
				provider: domain.ExternalMailProviderGmail, address: tenantAddress("alice"), backfillDays: 14,
				tenantID: &f.tenant.ID, directoryID: "dir-1",
			}, owner, "Alice")
			if bindErr != nil {
				return bindErr
			}
			assigned <- connection.ID
			close(locked)
			<-release
			return nil
		})
		if assignErr != nil {
			t.Errorf("assignment: %v", assignErr)
			close(locked)
		}
	}()
	<-locked
	revoked := make(chan error, 1)
	go func() { revoked <- f.service.RevokeExternalMailTenant(ctx, f.owner, f.tenant.ID) }()
	select {
	case err = <-revoked:
		t.Fatalf("the revocation did not wait for the assignment: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err = <-revoked; err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := f.reloadConnection(t, <-assigned); got.Status != domain.ExternalMailStatusRevoked {
		t.Fatalf("assignment committed during the revocation = %s, want revoked", got.Status)
	}
	// Once revoked, a new assignment transaction is refused at the lock.
	err = f.repo.InTransaction(ctx, func(tx context.Context) error {
		return f.service.externalMail.LockActiveExternalMailTenant(tx, f.tenant.ID)
	})
	if !errors.Is(err, ErrExternalMailTenantInactive) {
		t.Fatalf("lock after revoke = %v", err)
	}
}

// TestTenantStaleCredentialNeverStopsReauthorizedTenant: a check that used the credential the owner
// has since replaced does not stop the re-authorized tenant.
func TestTenantStaleCredentialNeverStopsReauthorizedTenant(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	stale := f.reloadTenant(t)
	f.advance(time.Minute)
	if _, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, googleTenantInput()); err != nil {
		t.Fatalf("re-authorize: %v", err)
	}
	f.service.tenantLost(ctx, stale)
	if got := f.reloadTenant(t); got.Status != domain.ExternalMailTenantActive {
		t.Fatalf("tenant = %s after a stale check, want active", got.Status)
	}
	f.service.tenantLost(ctx, f.reloadTenant(t))
	if got := f.reloadTenant(t); got.Status != domain.ExternalMailTenantNeedsReauthorization {
		t.Fatalf("tenant = %s after a current check, want needs_reauthorization", got.Status)
	}
}

// TestTenantOrganizationLimit: an organization holds at most MaxExternalMailTenantsPerOrganization live
// tenants; revoked ones do not count.
func TestTenantOrganizationLimit(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	for i := 1; i < domain.MaxExternalMailTenantsPerOrganization; i++ {
		if err := f.service.externalMail.CreateExternalMailTenant(ctx, &domain.ExternalMailTenant{
			OrganizationID: accessOrg, Provider: domain.ExternalMailProviderGmail, TenantIdentifier: fmt.Sprintf("c%02d", i),
			EncryptedCredential: "sealed", AuthorizedAt: f.service.now(),
		}); err != nil {
			t.Fatalf("seed tenant %d: %v", i, err)
		}
	}
	f.source.AddTenant("other.example")
	input := ExternalMailTenantInput{Provider: domain.ExternalMailProviderGmail, Grant: ExternalMailTenantGrant{
		ServiceAccountKey: `{"type":"service_account"}`, AdminSubject: "it@other.example",
	}}
	if _, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, input); !errors.Is(err, ErrExternalMailConnectionLimit) {
		t.Fatalf("51st tenant = %v, want limit", err)
	}
	if err := f.service.RevokeExternalMailTenant(ctx, f.owner, f.tenant.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if result, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, input); err != nil || !result.Created {
		t.Fatalf("after a revocation = %+v, err %v", result, err)
	}
	views, err := f.service.ListExternalMailTenants(ctx, f.admin)
	if err != nil || len(views) != domain.MaxExternalMailTenantsPerOrganization || views[len(views)-1].Tenant.Status == domain.ExternalMailTenantRevoked {
		t.Fatalf("listing holds %d tenants, err %v; want every live one first", len(views), err)
	}
}

// TestTenantSharedRestoreReplacesMembers: restoring a shared assignment makes its members exactly the
// posted set.
func TestTenantSharedRestoreReplacesMembers(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	support := f.mustAssign(t, sharedRow("support"))[0] // C manage, D view
	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, support.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	row := ExternalMailTenantAssignmentInput{Address: tenantAddress("support"), Assign: TenantAssignShared, Sharing: &domain.ExternalMailSharing{
		Members: []domain.ExternalMailSharingMember{
			{UserID: accessC, Permission: domain.MailboxPermissionManage}, {UserID: accessB, Permission: domain.MailboxPermissionView},
		},
	}}
	if result := f.assign(t, f.admin, row)[0]; result.Status != TenantAssignmentRestored {
		t.Fatalf("restore = %+v", result)
	}
	members, err := f.repo.ListMailboxMembers(ctx, support.MailboxID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	got := map[uint]domain.MailboxPermission{}
	for _, member := range members {
		got[member.UserID] = member.Permission
	}
	if len(got) != 2 || got[accessC] != domain.MailboxPermissionManage || got[accessB] != domain.MailboxPermissionView {
		t.Fatalf("members = %v, want exactly C manage and B view", got)
	}
	if removed := eventsOfType(accessEventsOf(t, f.accessFixture, support.MailboxID), domain.MailboxAccessMemberRemoved); len(removed) != 1 {
		t.Fatalf("member_removed events = %d, want D's", len(removed))
	}
}

// TestTenantMailboxNeverSends: a mailbox assigned from a tenant stays read-only even if its record
// claims an SMTP server of its own, as an IMAP binding may have; so does it after a handover.
func TestTenantMailboxNeverSends(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	if err := f.repo.(*repository).db.Exec(`UPDATE mailboxes SET connected_send = true WHERE id = ?`, alice.MailboxID).Error; err != nil {
		t.Fatalf("mark connected send: %v", err)
	}
	mailbox, err := f.service.authorizeMailbox(ctx, f.b, alice.MailboxID, nil)
	if err != nil || mailbox.Access.Send || !mailbox.Access.View {
		t.Fatalf("B on alice@ = %+v, err %v; want read only", mailbox, err)
	}
	send := SendInput{To: []string{"x@example.com"}, Subject: "Hi", Text: "Hi"}
	if _, _, err = f.service.SendMessage(ctx, f.b, alice.MailboxID, send); err == nil {
		t.Fatal("B sent from a tenant-assigned mailbox")
	}
	if _, err = f.service.HandOverMailbox(ctx, f.admin, alice.MailboxID, accessC); err != nil {
		t.Fatalf("handover: %v", err)
	}
	if _, _, err = f.service.SendMessage(ctx, f.c, alice.MailboxID, send); err == nil {
		t.Fatal("the successor sent from a handed-over tenant mailbox")
	}
}
