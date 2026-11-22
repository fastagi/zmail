package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
	"github.com/fastagi/zmail/api/pkg/response"
)

// tenantDomain is the fake Workspace tenant: its addresses are the fake source's sign-in addresses,
// so a self-binding and a tenant assignment can collide.
const tenantDomain = "external.invalid"

// tenantFixture is an external fixture with an authorized tenant of five directory mailboxes.
type tenantFixture struct {
	*externalFixture
	owner  domain.OrganizationContext
	tenant *domain.ExternalMailTenant
	audits []auditstarter.Change
}

func tenantAddress(local string) string { return local + "@" + tenantDomain }

func newTenantFixture(t *testing.T) *tenantFixture {
	t.Helper()
	f := &tenantFixture{externalFixture: newExternalFixture(t, config.ExternalMailSourceFake)}
	f.service.settings.ExternalMail.TenantsEnabled = true
	f.service.recordAudit = func(_ context.Context, change auditstarter.Change) { f.audits = append(f.audits, change) }
	f.owner = viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleOwner)
	var directory []ExternalMailDirectoryEntry
	for i, local := range []string{"alice", "bob", "support", "sales", "dave"} {
		directory = append(directory, ExternalMailDirectoryEntry{
			Address: tenantAddress(local), DisplayName: strings.ToUpper(local[:1]) + local[1:], DirectoryID: fmt.Sprintf("dir-%d", i+1),
		})
	}
	f.source.AddTenant(tenantDomain, directory...)
	result, err := f.service.AuthorizeExternalMailTenant(context.Background(), f.owner, googleTenantInput())
	if err != nil || !result.Created || result.Tenant == nil {
		t.Fatalf("authorize tenant = %+v, err %v", result, err)
	}
	f.tenant = result.Tenant.Tenant
	return f
}

func googleTenantInput() ExternalMailTenantInput {
	return ExternalMailTenantInput{Provider: domain.ExternalMailProviderGmail, Grant: ExternalMailTenantGrant{
		ServiceAccountKey: `{"type":"service_account"}`, AdminSubject: "it@" + tenantDomain,
	}}
}

func personalRow(local string, userID uint) ExternalMailTenantAssignmentInput {
	return ExternalMailTenantAssignmentInput{Address: tenantAddress(local), Assign: TenantAssignPersonal, UserID: userID}
}

func sharedRow(local string) ExternalMailTenantAssignmentInput {
	return ExternalMailTenantAssignmentInput{Address: tenantAddress(local), Assign: TenantAssignShared, Sharing: teamMembers()}
}

func (f *tenantFixture) assign(t *testing.T, viewer domain.OrganizationContext, rows ...ExternalMailTenantAssignmentInput) []ExternalMailTenantAssignmentResult {
	t.Helper()
	results, err := f.service.AssignExternalMailTenantMailboxes(context.Background(), viewer, f.tenant.ID, rows)
	if err != nil {
		t.Fatalf("AssignExternalMailTenantMailboxes: %v", err)
	}
	return results
}

// mustAssign assigns rows that must all be created and returns their connections in order.
func (f *tenantFixture) mustAssign(t *testing.T, rows ...ExternalMailTenantAssignmentInput) []*domain.ExternalMailConnection {
	t.Helper()
	var connections []*domain.ExternalMailConnection
	for _, result := range f.assign(t, f.admin, rows...) {
		if result.Status != TenantAssignmentCreated || result.Connection == nil {
			t.Fatalf("assignment of %s = %+v, want created", result.Address, result)
		}
		connections = append(connections, result.Connection)
	}
	return connections
}

func (f *tenantFixture) reloadTenant(t *testing.T) *domain.ExternalMailTenant {
	t.Helper()
	tenant, err := f.service.externalMail.FindExternalMailTenant(context.Background(), accessOrg, f.tenant.ID)
	if err != nil {
		t.Fatalf("FindExternalMailTenant: %v", err)
	}
	return tenant
}

// TestTenantAuthorizationIsForOwners: only the owner authorizes or revokes; admins list; members do
// nothing. Authorizing grants nobody any mailbox, and the audit never carries the credential.
func TestTenantAuthorizationIsForOwners(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	for name, viewer := range map[string]domain.OrganizationContext{"admin": f.admin, "member": f.b} {
		if _, err := f.service.AuthorizeExternalMailTenant(ctx, viewer, googleTenantInput()); !errors.Is(err, domain.ErrPermissionDenied) {
			t.Errorf("%s authorizing = %v, want permission denied", name, err)
		}
		if err := f.service.RevokeExternalMailTenant(ctx, viewer, f.tenant.ID); !errors.Is(err, domain.ErrPermissionDenied) {
			t.Errorf("%s revoking = %v, want permission denied", name, err)
		}
	}
	if _, err := f.service.ListExternalMailTenants(ctx, f.b); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Errorf("member listing = %v, want permission denied", err)
	}
	if _, err := f.service.ExternalMailTenantDirectory(ctx, f.c, f.tenant.ID, "", 10); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Errorf("member directory = %v, want permission denied", err)
	}
	if _, err := f.service.AssignExternalMailTenantMailboxes(ctx, f.c, f.tenant.ID, []ExternalMailTenantAssignmentInput{personalRow("alice", accessC)}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Errorf("member assigning = %v, want permission denied", err)
	}
	views, err := f.service.ListExternalMailTenants(ctx, f.admin)
	if err != nil || len(views) != 1 || views[0].Tenant.TenantIdentifier != tenantDomain || views[0].Assignments != 0 ||
		views[0].Tenant.Status != domain.ExternalMailTenantActive {
		t.Fatalf("admin listing = %+v, err %v", views, err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM external_mail_connections WHERE tenant_id IS NOT NULL`); n != 0 {
		t.Fatalf("authorizing created %d connections", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM mailboxes WHERE external_mail_tenant_id IS NOT NULL`); n != 0 {
		t.Fatalf("authorizing created %d mailboxes", n)
	}
	if f.tenant.EncryptedCredential == "" || strings.Contains(f.tenant.EncryptedCredential, tenantDomain) {
		t.Fatal("the credential is not sealed")
	}
	if len(f.audits) != 1 || f.audits[0].Action != "create" || f.audits[0].Resource != "external_mail_tenants" {
		t.Fatalf("audit = %+v", f.audits)
	}
	encoded, _ := json.Marshal(f.audits)
	if strings.Contains(string(encoded), "service_account") || strings.Contains(string(encoded), fakeTenantCredential) {
		t.Fatalf("audit carries the credential: %s", encoded)
	}

	// Authorizing the same tenant again refreshes it in place.
	again, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, googleTenantInput())
	if err != nil || again.Created || again.Tenant.Tenant.ID != f.tenant.ID {
		t.Fatalf("re-authorization = %+v, err %v", again, err)
	}
	// A refused credential, a provider without tenants, and a malformed grant.
	refused := googleTenantInput()
	refused.Grant.ServiceAccountKey = "refuse"
	if _, err = f.service.AuthorizeExternalMailTenant(ctx, f.owner, refused); !errors.Is(err, ErrExternalMailAuthorizationFailed) {
		t.Errorf("refused key = %v, want authorization failed", err)
	}
	unknown := googleTenantInput()
	unknown.Provider = "imap"
	if _, err = f.service.AuthorizeExternalMailTenant(ctx, f.owner, unknown); !errors.Is(err, ErrExternalMailProviderUnavailable) {
		t.Errorf("provider without tenants = %v, want unavailable", err)
	}

	// The tenant reads for one organization at a time.
	other := viewerOf(accessOrg+1, accessAdmin, domain.OrganizationRoleOwner)
	if _, err = f.service.AuthorizeExternalMailTenant(ctx, other, googleTenantInput()); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Errorf("another organization = %v, want account taken", err)
	}
}

// TestTenantMicrosoftOwnApplicationOnly: Microsoft tenants are accepted only with the organization's
// own application. A tenant id alone, the operator's application, is refused as invalid before the
// provider is asked, and so is a client id without its secret or the reverse.
func TestTenantMicrosoftOwnApplicationOnly(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	const entra = "0b1e2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	f.source.AddTenant(entra, ExternalMailDirectoryEntry{Address: "ops@m365.example", DirectoryID: "u-1"})
	for name, grant := range map[string]ExternalMailTenantGrant{
		"tenant id alone":    {TenantID: entra},
		"no credential":      {},
		"client id only":     {TenantID: entra, ClientID: "own-app"},
		"client secret only": {TenantID: entra, ClientSecret: "secret"},
	} {
		if _, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, ExternalMailTenantInput{
			Provider: domain.ExternalMailProviderOutlook, Grant: grant,
		}); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
			t.Errorf("%s = %v, want invalid", name, err)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM external_mail_authorizations`); n != 0 {
		t.Fatalf("%d pending sign-ins stored; tenants never use a consent redirect", n)
	}
	result, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, ExternalMailTenantInput{
		Provider: domain.ExternalMailProviderOutlook, Grant: ExternalMailTenantGrant{TenantID: entra, ClientID: "own-app", ClientSecret: "secret"},
	})
	if err != nil || !result.Created || result.Tenant.Tenant.TenantIdentifier != entra {
		t.Fatalf("own application = %+v, err %v", result, err)
	}
}

// TestTenantDirectoryAndAssignments: the directory pages and marks assignments; each row is checked
// and stored on its own, idempotently, and a failed row says why.
func TestTenantDirectoryAndAssignments(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	page, err := f.service.ExternalMailTenantDirectory(ctx, f.admin, f.tenant.ID, "", 2)
	if err != nil || len(page.Entries) != 2 || page.Next == "" || page.Entries[0].Entry.Address != tenantAddress("alice") ||
		!page.Entries[0].Available || page.Entries[0].Connection != nil {
		t.Fatalf("first page = %+v, err %v", page, err)
	}
	carol := f.bind(t, f.c, domain.ExternalMailProviderGmail, "sales") // C bound sales@ as her own

	results := f.assign(t, f.admin,
		personalRow("alice", accessB),
		sharedRow("support"),
		personalRow("nobody", accessB),
		personalRow("bob", notMember),
		personalRow("alice", accessC),
		personalRow("sales", accessD),
		ExternalMailTenantAssignmentInput{Address: tenantAddress("dave"), Assign: TenantAssignShared},
	)
	want := []struct{ status, code string }{
		{TenantAssignmentCreated, ""},
		{TenantAssignmentCreated, ""},
		{TenantAssignmentFailed, domain.CodeExternalMailAccountMismatch},
		{TenantAssignmentFailed, domain.CodeMailboxAccessInvalid},
		{TenantAssignmentFailed, domain.CodeMailboxAccessInvalid},
		{TenantAssignmentFailed, domain.CodeExternalMailAccountTaken},
		{TenantAssignmentFailed, domain.CodeMailboxAccessInvalid},
	}
	for i, result := range results {
		if result.Status != want[i].status || result.ErrorCode != want[i].code {
			t.Errorf("row %d (%s) = %+v, want %+v", i, result.Address, result, want[i])
		}
	}
	alice, support := results[0].Connection, results[1].Connection
	if alice.TenantID == nil || *alice.TenantID != f.tenant.ID || alice.Shared || alice.OwnerUserID == nil || *alice.OwnerUserID != accessB ||
		alice.EncryptedRefreshToken != "" || alice.TenantDirectoryID != "dir-1" || alice.Status != domain.ExternalMailStatusActive {
		t.Fatalf("alice = %+v", alice)
	}
	if !support.Shared || support.OwnerUserID != nil || support.TenantID == nil {
		t.Fatalf("support = %+v", support)
	}
	mailbox, err := f.repo.FindMailbox(ctx, accessOrg, alice.MailboxID)
	if err != nil || mailbox.Type != domain.MailboxTypePersonal || !mailbox.TenantAssigned() || mailbox.DisplayName != "Alice" {
		t.Fatalf("alice's mailbox = %+v, err %v", mailbox, err)
	}

	// The same rows again change nothing; a different target for a live assignment is refused.
	again := f.assign(t, f.admin, personalRow("alice", accessB), sharedRow("support"))
	other := f.assign(t, f.admin, sharedRow("alice"))
	if again[0].Status != TenantAssignmentUnchanged || again[0].Connection.ID != alice.ID || again[1].Status != TenantAssignmentUnchanged ||
		other[0].ErrorCode != domain.CodeExternalMailAccountTaken {
		t.Fatalf("again = %+v, other target = %+v", again, other)
	}
	// A member cannot self-bind an assigned address, and C's own sales@ is untouched.
	if _, created, bindErr := f.service.CompleteExternalMailAuthorization(ctx, f.b,
		f.signIn(t, f.b, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail}), "alice"); !errors.Is(bindErr, ErrExternalMailAccountTaken) || created {
		t.Fatalf("self-binding an assignment = %v", bindErr)
	}
	if f.reloadConnection(t, carol.ID).Status != domain.ExternalMailStatusActive {
		t.Fatal("C's own connection changed")
	}

	page, err = f.service.ExternalMailTenantDirectory(ctx, f.admin, f.tenant.ID, "", 10)
	if err != nil {
		t.Fatalf("directory: %v", err)
	}
	marks := map[string]ExternalMailTenantDirectoryEntry{}
	for _, entry := range page.Entries {
		marks[entry.Entry.Local()] = entry
	}
	if marks["alice"].Connection == nil || marks["alice"].Available || marks["sales"].Connection != nil || marks["sales"].Available ||
		marks["bob"].Connection != nil || !marks["bob"].Available {
		t.Fatalf("marks = %+v", marks)
	}
	views, _ := f.service.ListExternalMailTenants(ctx, f.admin)
	if views[0].Assignments != 2 {
		t.Fatalf("assignments = %d, want 2", views[0].Assignments)
	}
	// Every created row is in the audit log and the mailbox history, with the tenant.
	assigned := 0
	for _, change := range f.audits {
		if change.Metadata["reason"] == "tenant_assignment" {
			assigned++
		}
	}
	if assigned != 2 {
		t.Fatalf("%d assignment audits, want 2: %+v", assigned, f.audits)
	}
	bound := eventsOfType(accessEventsOf(t, f.accessFixture, alice.MailboxID), domain.MailboxAccessConnectionBound)
	if len(bound) != 1 || bound[0].Detail["tenant_id"] == nil {
		t.Fatalf("bound events = %+v", bound)
	}

	// Batch bounds.
	if _, err = f.service.AssignExternalMailTenantMailboxes(ctx, f.admin, f.tenant.ID, nil); !errors.Is(err, ErrMailboxAccessInvalid) {
		t.Fatalf("empty batch = %v", err)
	}
	many := make([]ExternalMailTenantAssignmentInput, MaxTenantAssignmentsPerRequest+1)
	if _, err = f.service.AssignExternalMailTenantMailboxes(ctx, f.admin, f.tenant.ID, many); !errors.Is(err, ErrMailboxAccessInvalid) {
		t.Fatalf("oversized batch = %v", err)
	}
}

// Local is the part of an address before the at sign, for test maps.
func (e ExternalMailDirectoryEntry) Local() string {
	local, _, _ := strings.Cut(e.Address, "@")
	return local
}

// TestTenantAssignmentLimits: the tenant ceiling and the organization's shared limit apply; a
// personal assignment never takes one of the member's own ten places.
func TestTenantAssignmentLimits(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	f.service.settings.ExternalMail.MaxTenantAssignments = 3
	f.service.settings.ExternalMail.MaxSharedConnections = 1
	results := f.assign(t, f.admin, personalRow("alice", accessB), sharedRow("support"), sharedRow("sales"),
		personalRow("bob", accessB), personalRow("dave", accessB))
	codes := []string{"", "", domain.CodeExternalMailConnectionLimit, "", domain.CodeExternalMailConnectionLimit}
	for i, result := range results {
		if result.ErrorCode != codes[i] {
			t.Errorf("row %d = %+v, want code %q", i, result, codes[i])
		}
	}
	own, err := f.service.externalMail.CountLiveExternalMailConnections(ctx, accessOrg, accessB)
	if err != nil || own != 1 {
		t.Fatalf("B's own live connections = %d, err %v; want only the fixture's", own, err)
	}
	listed, err := f.service.ListExternalMailConnections(ctx, f.b)
	if err != nil || len(listed) != 3 {
		t.Fatalf("B lists %d connections, err %v; want own + two assignments", len(listed), err)
	}
}

// TestTenantAssignmentAccess: a personal assignment is private to its member, administered but not
// read by managers; a shared one follows the shared matrix; both sync with a tenant token only.
func TestTenantAssignmentAccess(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	connections := f.mustAssign(t, personalRow("alice", accessB), sharedRow("support"))
	alice, support := connections[0], connections[1]
	now := f.service.now()
	f.source.Deliver(TenantMailboxToken(tenantAddress("alice")), externalMessage("a-1", "For Alice", now.Add(-time.Hour)))
	f.source.Deliver(TenantMailboxToken(tenantAddress("support")), externalMessage("s-1", "For support", now.Add(-time.Hour)))
	f.sync(t)
	if request := f.source.LastRequest(TenantMailboxToken(tenantAddress("alice"))); request.Token.AccessToken == "" ||
		request.Token.RefreshToken != "" || request.Token.DirectoryID != "dir-1" {
		t.Fatalf("alice was fetched with %+v", request.Token)
	}

	readable := func(viewer domain.OrganizationContext, mailboxID uint) bool {
		messages, _, listErr := f.service.ListMessages(ctx, viewer, mailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10})
		return listErr == nil && len(messages) == 1
	}
	if !readable(f.b, alice.MailboxID) || readable(f.admin, alice.MailboxID) || readable(f.owner, alice.MailboxID) || readable(f.c, alice.MailboxID) {
		t.Fatal("only B reads alice@")
	}
	if !readable(f.c, support.MailboxID) || !readable(f.d, support.MailboxID) || readable(f.b, support.MailboxID) || readable(f.admin, support.MailboxID) {
		t.Fatal("only support's members read it")
	}
	mailbox, err := f.service.authorizeMailbox(ctx, f.admin, alice.MailboxID, canAdminister)
	if err != nil || mailbox.Access.View || !mailbox.Access.Administer {
		t.Fatalf("admin on alice@ = %+v, err %v; want administer without reading", mailbox, err)
	}
	if _, err = f.service.authorizeMailbox(ctx, f.admin, f.mailbox.ID, canAdminister); err == nil {
		t.Fatal("admin administers B's own Gmail")
	}

	// B cannot unbind, reconnect, or purge an organization address; an admin unassigns it.
	if err = f.service.RevokeExternalMailConnection(ctx, f.b, alice.ID); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("B unbinding alice@ = %v, want permission denied", err)
	}
	if err = f.service.RevokeExternalMailConnection(ctx, f.c, alice.ID); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("C unbinding alice@ = %v, want not found", err)
	}
	if _, err = f.service.StartExternalMailAuthorization(ctx, f.b, ExternalMailAuthorizationInput{ConnectionID: &alice.ID}); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
		t.Fatalf("B reconnecting alice@ = %v, want invalid", err)
	}
	if err = f.service.RevokeExternalMailConnection(ctx, f.admin, alice.ID); err != nil {
		t.Fatalf("admin unassigning alice@: %v", err)
	}
	if _, err = f.service.PurgeExternalMailCopies(ctx, f.b, alice.ID); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("B purging alice@ = %v, want permission denied", err)
	}
	// Assigning alice@ to B again restores the same mailbox with its copies.
	restored := f.assign(t, f.admin, personalRow("alice", accessB))
	if restored[0].Status != TenantAssignmentRestored || restored[0].Connection.ID != alice.ID || !readable(f.b, alice.MailboxID) {
		t.Fatalf("restore = %+v", restored)
	}
}

// TestTenantPersonalAssignmentOnLeave: when its member leaves, a personal assignment awaits handover
// like a Zmail personal mailbox and keeps syncing, while the member's own accounts are revoked. The
// handover seals what arrived before the departure and makes the connection the organization's.
func TestTenantPersonalAssignmentOnLeave(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	alice := f.mustAssign(t, personalRow("alice", accessB))[0]
	token := TenantMailboxToken(tenantAddress("alice"))
	f.source.Deliver(token, externalMessage("a-1", "Before", f.service.now().Add(-time.Hour)))
	f.sync(t)

	left := f.advance(time.Minute)
	f.leave(t, f.b, left)
	if got := f.reloadConnection(t, alice.ID); got.Status != domain.ExternalMailStatusActive {
		t.Fatalf("assignment after leave = %+v, want still active", got)
	}
	if got := f.reload(t); got.Status != domain.ExternalMailStatusRevoked {
		t.Fatalf("B's own Gmail after leave = %s, want revoked", got.Status)
	}
	mailbox, err := f.repo.FindMailbox(ctx, accessOrg, alice.MailboxID)
	if err != nil || !mailbox.AwaitingHandover() {
		t.Fatalf("mailbox after leave = %+v, err %v; want awaiting handover", mailbox, err)
	}
	if len(eventsOfType(accessEventsOf(t, f.accessFixture, alice.MailboxID), domain.MailboxAccessOwnerLeft)) != 1 {
		t.Fatal("no owner_left event")
	}
	// Mail keeps arriving while it awaits the handover.
	f.advance(10 * time.Minute)
	f.source.Deliver(token, externalMessage("a-2", "After", f.service.now()))
	f.sync(t)

	handed, err := f.service.HandOverMailbox(ctx, f.admin, alice.MailboxID, accessC)
	if err != nil || handed.Type != domain.MailboxTypeShared || !handed.Connected() {
		t.Fatalf("handover = %+v, err %v", handed, err)
	}
	connection := f.reloadConnection(t, alice.ID)
	if !connection.Shared || connection.OwnerUserID != nil || connection.Status != domain.ExternalMailStatusActive {
		t.Fatalf("connection after handover = %+v", connection)
	}
	messages, _, err := f.service.ListMessages(ctx, f.c, alice.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10})
	if err != nil || len(messages) != 1 || messages[0].Subject != "After" {
		t.Fatalf("successor reads %d messages, err %v; want only what arrived after the departure", len(messages), err)
	}
	// The successor cannot send from the connected address.
	if _, _, err = f.service.SendMessage(ctx, f.c, alice.MailboxID, SendInput{To: []string{"x@example.com"}, Subject: "Hi", Text: "Hi"}); err == nil {
		t.Fatal("successor sent from a connected address")
	}
	// A member's own Gmail is still never handed over.
	if _, err = f.service.HandOverMailbox(ctx, f.admin, f.mailbox.ID, accessC); err == nil {
		t.Fatal("handed over B's own Gmail")
	}
}

// TestTenantCredentialLoss: a refused tenant credential stops every assignment at once; a refused
// mailbox stops only itself; re-authorizing the tenant resumes them.
func TestTenantCredentialLoss(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	connections := f.mustAssign(t, personalRow("alice", accessB), sharedRow("support"), personalRow("bob", accessC))

	f.source.RefuseTenantMailbox(tenantAddress("bob"))
	f.sync(t)
	if got := f.reloadConnection(t, connections[2].ID); got.Status != domain.ExternalMailStatusNeedsReconnect {
		t.Fatalf("bob = %s, want needs_reconnect", got.Status)
	}
	if f.reloadConnection(t, connections[0].ID).Status != domain.ExternalMailStatusActive || f.reloadTenant(t).Status != domain.ExternalMailTenantActive {
		t.Fatal("one refused mailbox stopped the tenant")
	}

	f.source.RefuseTenant(tenantDomain, true)
	f.advance(3 * time.Minute)
	f.sync(t)
	if tenant := f.reloadTenant(t); tenant.Status != domain.ExternalMailTenantNeedsReauthorization || tenant.LastErrorCode != domain.ExternalMailErrorTenantAuthFailed {
		t.Fatalf("tenant = %+v", tenant)
	}
	for _, connection := range connections[:2] {
		if got := f.reloadConnection(t, connection.ID); got.Status != domain.ExternalMailStatusNeedsReconnect {
			t.Fatalf("connection %d = %s, want needs_reconnect", connection.ID, got.Status)
		}
	}
	f.advance(3 * time.Minute)
	if handled := f.sync(t); handled != 1 {
		t.Fatalf("%d connections synced, want only B's own", handled)
	}
	if _, err := f.service.ExternalMailTenantDirectory(ctx, f.admin, f.tenant.ID, "", 10); !errors.Is(err, ErrExternalMailTenantInactive) {
		t.Fatalf("directory of a lost tenant = %v", err)
	}

	f.source.RefuseTenant(tenantDomain, false)
	if _, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, googleTenantInput()); err != nil {
		t.Fatalf("re-authorize: %v", err)
	}
	if f.reloadTenant(t).Status != domain.ExternalMailTenantActive {
		t.Fatal("tenant not active again")
	}
	for _, connection := range connections[:2] {
		if got := f.reloadConnection(t, connection.ID); got.Status != domain.ExternalMailStatusActive {
			t.Fatalf("connection %d = %s after re-authorization, want active", connection.ID, got.Status)
		}
	}
	// bob@ was stopped by its own refusal, not by the tenant credential: it stays stopped.
	if got := f.reloadConnection(t, connections[2].ID); got.Status != domain.ExternalMailStatusNeedsReconnect ||
		got.LastErrorCode != domain.ExternalMailErrorAuthFailed {
		t.Fatalf("bob after re-authorization = %+v, want still needs_reconnect", got)
	}
}

// TestTenantRevocation: revoking the tenant revokes every assignment at once and records it; the
// tenant row stays without its credential.
func TestTenantRevocation(t *testing.T) {
	f := newTenantFixture(t)
	ctx := context.Background()
	connections := f.mustAssign(t, personalRow("alice", accessB), sharedRow("support"))
	f.audits = nil
	if err := f.service.RevokeExternalMailTenant(ctx, f.owner, f.tenant.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	tenant := f.reloadTenant(t)
	if tenant.Status != domain.ExternalMailTenantRevoked || tenant.EncryptedCredential != "" || tenant.RevokedAt == nil {
		t.Fatalf("tenant = %+v", tenant)
	}
	for _, connection := range connections {
		if got := f.reloadConnection(t, connection.ID); got.Status != domain.ExternalMailStatusRevoked || got.RevokedAt == nil {
			t.Fatalf("connection %d = %+v", connection.ID, got)
		}
		revoked := eventsOfType(accessEventsOf(t, f.accessFixture, connection.MailboxID), domain.MailboxAccessConnectionRevoked)
		if len(revoked) != 1 || revoked[0].Detail["reason"] != "tenant_revoked" {
			t.Fatalf("revocation events = %+v", revoked)
		}
	}
	if len(f.audits) != 1 || f.audits[0].Action != "delete" || f.audits[0].Metadata["connections_revoked"] != 2 {
		t.Fatalf("audit = %+v", f.audits)
	}
	if handled := f.sync(t); handled != 1 {
		t.Fatalf("%d synced after the revocation, want only B's own", handled)
	}
	if err := f.service.RevokeExternalMailTenant(ctx, f.owner, f.tenant.ID); err != nil {
		t.Fatalf("revoking again: %v", err)
	}
	if _, err := f.service.AssignExternalMailTenantMailboxes(ctx, f.admin, f.tenant.ID, []ExternalMailTenantAssignmentInput{personalRow("bob", accessB)}); !errors.Is(err, ErrExternalMailTenantInactive) {
		t.Fatalf("assigning from a revoked tenant = %v", err)
	}
	// A revoked tenant frees its identifier for a new authorization.
	result, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, googleTenantInput())
	if err != nil || !result.Created || result.Tenant.Tenant.ID == f.tenant.ID {
		t.Fatalf("authorize after revoke = %+v, err %v", result, err)
	}
}

// agentKeys is the API key service of the route test: one live key with every mail scope.
type agentKeys struct{ apikey.Service }

func (agentKeys) Validate(_ context.Context, plaintext string, _ ...string) (*domain.APIKey, error) {
	if plaintext != "agent-key" {
		return nil, domain.ErrAPIKeyInvalid
	}
	return &domain.APIKey{ID: 77, UserID: accessB, Scopes: []string{ScopeMailAgent, ScopeMailSend, ScopeMailRead}, Mode: domain.APIKeyModeLive}, nil
}

// TestTenantRoutes: the HTTP shapes, the owner and member answers, and no tenant route reachable
// with an API key. The api_key group is the apikey starter's real middleware; the auth group admits
// only a session bearer, as the user starter's does.
func TestTenantRoutes(t *testing.T) {
	f := newTenantFixture(t)
	call := func(viewer domain.OrganizationContext, method, path, body string) (int, map[string]any) {
		t.Helper()
		gin.SetMode(gin.TestMode)
		handler := NewHandler(f.service)
		response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
		response.DefaultErrorMapper.Register(domain.ErrInvalidInput, http.StatusBadRequest, domain.CodeInvalidInput)
		response.DefaultErrorMapper.Register(domain.ErrAPIKeyInvalid, http.StatusUnauthorized, response.ErrorCodeUnauthorized)
		handler.RegisterErrorMappings(response.DefaultErrorMapper)
		engine := gin.New()
		routes := router.New(engine).Prefix("/v1")
		apikey.NewHandler(agentKeys{}).RegisterMiddleware(routes)
		routes.MiddlewareGroup("auth", func(c *gin.Context) {
			if !strings.HasPrefix(c.GetHeader("Authorization"), "Bearer session-") {
				response.AbortUnauthorized(c, "Authentication required")
				return
			}
			c.Next()
		})
		routes.MiddlewareGroup("organization_context", func(c *gin.Context) {
			current := viewer
			if c.GetUint("userID") != 0 {
				// An API key acts as its owner, a member.
				current = viewerOf(accessOrg, c.GetUint("userID"), domain.OrganizationRoleOwner)
			}
			c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), current))
			c.Next()
		})
		handler.RegisterRoutes(routes)
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if viewer.UserID == 0 {
			request.Header.Set("X-API-Key", "agent-key")
		} else {
			request.Header.Set("Authorization", "Bearer session-1")
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		decoded := map[string]any{}
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
		return recorder.Code, decoded
	}

	authorize := `{"provider":"gmail","service_account_key":{"type":"service_account"},"admin_subject":"it@external.invalid"}`
	if status, body := call(f.b, http.MethodPost, "/v1/external-mail-tenants", authorize); status != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("member authorizing = %d %v", status, body)
	}
	if status, body := call(f.owner, http.MethodPost, "/v1/external-mail-tenants", authorize); status != http.StatusOK {
		t.Fatalf("owner re-authorizing = %d %v", status, body)
	}
	status, body := call(f.owner, http.MethodPost, "/v1/external-mail-tenants",
		`{"provider":"outlook","tenant_id":"0b1e2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"}`)
	if status != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeExternalMailAuthorizationInvalid {
		t.Fatalf("operator-app consent = %d %v, want 422", status, body)
	}
	if status, _ = call(f.owner, http.MethodPost, "/v1/external-mail-tenants/completions", `{"state":"s","tenant":"t","admin_consent":"True"}`); status != http.StatusNotFound {
		t.Fatalf("consent completion route = %d, want gone", status)
	}
	status, body = call(f.admin, http.MethodGet, "/v1/external-mail-tenants", "")
	items, _ := body["data"].([]any)
	if status != http.StatusOK || len(items) != 1 {
		t.Fatalf("admin listing = %d %v", status, body)
	}
	tenant := items[0].(map[string]any)
	if _, leaked := tenant["encrypted_credential"]; leaked || tenant["tenant_identifier"] != tenantDomain || tenant["status"] != "active" {
		t.Fatalf("tenant = %v", tenant)
	}
	base := fmt.Sprintf("/v1/external-mail-tenants/%d", f.tenant.ID)
	status, body = call(f.admin, http.MethodGet, base+"/directory?per_page=2", "")
	if status != http.StatusOK || len(body["data"].([]any)) != 2 {
		t.Fatalf("directory = %d %v", status, body)
	}
	next := body["meta"].(map[string]any)["next_cursor"].(string)
	if status, body = call(f.admin, http.MethodGet, base+"/directory?per_page=2&cursor="+next, ""); status != http.StatusOK ||
		body["data"].([]any)[0].(map[string]any)["address"] != tenantAddress("support") {
		t.Fatalf("second page = %d %v", status, body)
	}
	if status, body = call(f.admin, http.MethodGet, base+"/directory?cursor=%25%25", ""); status != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d %v", status, body)
	}
	status, body = call(f.admin, http.MethodPost, base+"/assignments", `{"assignments":[
		{"address":"alice@external.invalid","assign":"personal","user_id":2},
		{"address":"support@external.invalid","assign":"shared","members":[{"user_id":3,"permission":"manage"}]},
		{"address":"nobody@external.invalid","assign":"personal","user_id":2}]}`)
	rows, _ := body["data"].([]any)
	if status != http.StatusOK || len(rows) != 3 || rows[0].(map[string]any)["status"] != "created" ||
		rows[2].(map[string]any)["error_code"] != domain.CodeExternalMailAccountMismatch {
		t.Fatalf("assignments = %d %v", status, body)
	}
	if status, _ = call(f.c, http.MethodPost, base+"/assignments", `{"assignments":[{"address":"bob@external.invalid","assign":"personal","user_id":3}]}`); status != http.StatusForbidden {
		t.Fatalf("member assigning = %d", status)
	}
	if status, _ = call(f.admin, http.MethodPost, base+"/assignments", `{"assignments":[{"address":"bob@external.invalid","assign":"owner","user_id":3}]}`); status != http.StatusUnprocessableEntity && status != http.StatusBadRequest {
		t.Fatalf("unknown assign = %d", status)
	}
	// The API key is valid: it reaches its own agent route.
	agent := domain.OrganizationContext{OrganizationID: accessOrg}
	if status, body = call(agent, http.MethodGet, "/v1/agent-mail-events", ""); status == http.StatusUnauthorized {
		t.Fatalf("agent route with a valid key = %d %v", status, body)
	}
	// It reaches none of the tenant routes.
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v1/external-mail-tenants"}, {http.MethodPost, "/v1/external-mail-tenants"},
		{http.MethodGet, base + "/directory"}, {http.MethodPost, base + "/assignments"}, {http.MethodDelete, base},
	} {
		if status, _ = call(agent, route.method, route.path, "{}"); status != http.StatusUnauthorized {
			t.Errorf("API key on %s %s = %d, want 401", route.method, route.path, status)
		}
	}
	if status, _ = call(f.admin, http.MethodDelete, base, ""); status != http.StatusForbidden {
		t.Fatalf("admin revoking = %d", status)
	}
	if status, _ = call(f.owner, http.MethodDelete, base, ""); status != http.StatusNoContent {
		t.Fatalf("owner revoking = %d", status)
	}
	if status, body = call(f.owner, http.MethodDelete, "/v1/external-mail-tenants/99999", ""); status != http.StatusNotFound ||
		body["error_code"] != domain.CodeExternalMailConnectionNotFound {
		t.Fatalf("unknown tenant = %d %v", status, body)
	}
	// The connector list advertises tenant support.
	status, body = f.call(t, f.b, http.MethodGet, "/external-mail-connectors")
	connector := body["data"].([]any)[0].(map[string]any)
	if status != http.StatusOK || connector["capabilities"].(map[string]any)["tenant"] != true {
		t.Fatalf("connectors = %d %v", status, body)
	}
}
