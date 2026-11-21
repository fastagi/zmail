package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// TestConnectedMailboxIsHiddenFromManagers: an admin's mailbox lists, directory and its count,
// export, unified inbox, its counts, and its search never reveal another member's connected
// mailbox or its address, while the owner still lists and reads it.
func TestConnectedMailboxIsHiddenFromManagers(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "Quarterly secret", time.Time{}))
	f.sync(t)
	ctx := context.Background()
	address := f.mailbox.Address

	listed := func(viewer domain.OrganizationContext, scope MailboxListScope) []*domain.Mailbox {
		t.Helper()
		mailboxes, err := f.service.ListMailboxes(ctx, viewer, scope)
		if err != nil {
			t.Fatalf("ListMailboxes(%q): %v", scope, err)
		}
		return mailboxes
	}
	contains := func(mailboxes []*domain.Mailbox) bool {
		for _, mailbox := range mailboxes {
			if mailbox.ID == f.mailbox.ID || mailbox.Address == address {
				return true
			}
		}
		return false
	}
	for _, scope := range []MailboxListScope{MailboxListScopeAll, MailboxListScopeUsable} {
		if contains(listed(f.admin, scope)) {
			t.Fatalf("an admin's %q listing reveals the member's connected mailbox", scope)
		}
		if contains(listed(f.c, scope)) {
			t.Fatalf("a colleague's %q listing reveals the member's connected mailbox", scope)
		}
		owned := listed(f.b, scope)
		if !contains(owned) {
			t.Fatalf("the owner's %q listing misses their connected mailbox", scope)
		}
		for _, mailbox := range owned {
			if mailbox.ID == f.mailbox.ID && (mailbox.Origin != domain.MailboxOriginConnected || mailbox.Type != domain.MailboxTypePersonal ||
				!mailbox.Access.View || mailbox.Access.Send || mailbox.Access.Organize || mailbox.Access.Administer) {
				t.Fatalf("the owner's connected mailbox = %+v", mailbox)
			}
		}
	}

	page, err := f.service.MailboxDirectory(ctx, f.admin, domain.MailboxDirectoryFilter{})
	if err != nil {
		t.Fatalf("MailboxDirectory: %v", err)
	}
	for _, entry := range page.Entries {
		if entry.Mailbox.ID == f.mailbox.ID {
			t.Fatal("the mailbox directory lists the member's connected mailbox")
		}
	}
	if page.MailboxCount != 2 {
		t.Fatalf("mailbox_count = %d, want 2: the organization's own mailboxes only", page.MailboxCount)
	}
	searched, err := f.service.MailboxDirectory(ctx, f.admin, domain.MailboxDirectoryFilter{Query: "gmail"})
	if err != nil || len(searched.Entries) != 0 {
		t.Fatalf("directory search for the outside address = %d entries, err %v; want none", len(searched.Entries), err)
	}
	rows, err := f.service.ExportMailboxes(ctx, f.admin, domain.MailboxTypePersonal)
	if err != nil {
		t.Fatalf("ExportMailboxes: %v", err)
	}
	for _, row := range rows {
		if row.Entry.Mailbox.ID == f.mailbox.ID {
			t.Fatal("the export carries the member's connected mailbox")
		}
	}

	for _, request := range []InboxRequest{{}, {Query: "Quarterly"}, {MailboxID: f.mailbox.ID}} {
		if items := listInbox(t, f.service, f.admin, request).Items; len(items) > 0 {
			for _, item := range items {
				if item.Message.MailboxID == f.mailbox.ID {
					t.Fatalf("the admin's unified inbox %+v shows connected mail", request)
				}
			}
		}
	}
	if found := listInbox(t, f.service, f.b, InboxRequest{Query: "Quarterly"}).Items; len(found) != 1 {
		t.Fatalf("the owner's search found %d connected messages, want 1", len(found))
	}
	counts, err := f.service.InboxCounts(ctx, f.admin, nil)
	if err != nil {
		t.Fatalf("InboxCounts: %v", err)
	}
	for _, mailbox := range counts.Mailboxes {
		if mailbox.MailboxID == f.mailbox.ID {
			t.Fatal("the admin's inbox counts include the connected mailbox")
		}
	}

	for name, call := range map[string]func() error{
		"rename": func() error {
			_, err := f.service.RenameMailbox(ctx, f.admin, f.mailbox.ID, "Renamed")
			return err
		},
		"delete": func() error { return f.service.DeleteMailbox(ctx, f.admin, f.mailbox.ID) },
		"history": func() error {
			_, _, err := f.service.ListMailboxAccessEvents(ctx, f.admin, f.mailbox.ID, 1, 10)
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrMailboxNotFound) {
			t.Fatalf("admin %s on the connected mailbox: err = %v, want not found", name, err)
		}
	}

	// Over HTTP, the admin's mailbox list carries no trace of the address, and the owner's carries
	// the origin.
	status, body := f.call(t, f.admin, http.MethodGet, "/mailboxes")
	if status != http.StatusOK || strings.Contains(fmt.Sprint(body), address) {
		t.Fatalf("admin GET /mailboxes = %d, leaks the outside address: %v", status, body)
	}
	status, body = f.call(t, f.b, http.MethodGet, "/mailboxes")
	if status != http.StatusOK || !strings.Contains(fmt.Sprint(body), "origin:connected") {
		t.Fatalf("owner GET /mailboxes = %d %v; want the connected origin", status, body)
	}
}

// TestConnectedMailboxRefusesSendAndHandover: neither the owner nor an admin sends from or hands
// over a connected mailbox, and the owner leaving never puts it up for handover.
func TestConnectedMailboxRefusesSendAndHandover(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()

	if _, _, err := f.service.SendMessage(ctx, f.b, f.mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "Hi", Text: "Hi"}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("owner sending: err = %v, want permission denied", err)
	}
	if _, err := f.service.HandOverMailbox(ctx, f.admin, f.mailbox.ID, accessC); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("admin handing over: err = %v, want not found", err)
	}
	// Even a manager who reaches the mailbox (a future connected shared one, or their own) cannot.
	owner := uint(accessAdmin)
	own := &domain.Mailbox{OrganizationID: accessOrg, CreatedBy: accessAdmin, Address: "admin@gmail.example",
		Type: domain.MailboxTypePersonal, Origin: domain.MailboxOriginConnected, OwnerUserID: &owner}
	if err := f.repo.CreateMailbox(ctx, own, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if _, err := f.service.HandOverMailbox(ctx, f.admin, own.ID, accessC); err == nil {
		t.Fatal("an admin handed over their own connected mailbox")
	}
	converted, err := f.repo.HandOverMailbox(ctx, f.mailbox.ID, f.service.now())
	if err != nil || converted {
		t.Fatalf("repository handover of a connected mailbox = %v, %v; want refused", converted, err)
	}

	f.leave(t, f.b, f.service.now())
	usable, err := f.service.ListMailboxes(ctx, f.admin, MailboxListScopeUsable)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	for _, mailbox := range usable {
		if mailbox.ID == f.mailbox.ID {
			t.Fatal("a connected mailbox awaits handover after its owner left")
		}
	}
	mailbox, err := f.repo.FindMailbox(ctx, accessOrg, f.mailbox.ID)
	if err != nil || mailbox.OwnerLeftAt != nil || mailbox.AwaitingHandover() {
		t.Fatalf("connected mailbox after leaving = %+v, err %v", mailbox, err)
	}
}

// TestConnectedMailboxesDoNotCountTowardTheLimit: the organization's mailbox limit counts its own
// mailboxes only, and a connected address never takes delivery or blocks a Zmail address.
func TestConnectedMailboxesDoNotCountTowardTheLimit(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	// Two Zmail mailboxes exist; a limit of three still leaves room despite the connected one.
	third := &domain.Mailbox{OrganizationID: accessOrg, CreatedBy: accessAdmin, Address: "third@mail.example", Type: domain.MailboxTypeShared}
	if err := f.repo.CreateMailbox(ctx, third, 3); err != nil {
		t.Fatalf("third Zmail mailbox under a limit of 3: %v", err)
	}
	fourth := &domain.Mailbox{OrganizationID: accessOrg, CreatedBy: accessAdmin, Address: "fourth@mail.example", Type: domain.MailboxTypeShared}
	if err := f.repo.CreateMailbox(ctx, fourth, 3); !errors.Is(err, ErrMailboxLimitReached) {
		t.Fatalf("fourth Zmail mailbox under a limit of 3: err = %v, want limit reached", err)
	}
	if _, err := f.repo.FindMailboxByAddress(ctx, f.mailbox.Address); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("delivery lookup of a connected address: err = %v, want not found", err)
	}
	used, err := f.repo.(*repository).MailboxAddressUsed(ctx, f.mailbox.Address)
	if err != nil || used {
		t.Fatalf("connected address counted as used = %v, %v", used, err)
	}
}

// TestConnectionDirectoryMasksAndIsManagerOnly: owners and admins page through every connection
// with the address masked, never the token or the mail; members get 403.
func TestConnectionDirectoryMasksAndIsManagerOnly(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.bind(t, f.c, domain.ExternalMailProviderOutlook, "carol")
	f.bind(t, f.d, domain.ExternalMailProviderGmail, "dan")

	status, body := f.call(t, f.admin, http.MethodGet, "/external-mail-connection-directory?per_page=2")
	items, _ := body["data"].([]any)
	if status != http.StatusOK || len(items) != 2 {
		t.Fatalf("first page = %d %v", status, body)
	}
	newest := items[0].(map[string]any)
	if newest["masked_account_address"] != "da***@external.invalid" || newest["provider"] != "gmail" ||
		newest["status"] != "active" || newest["owner_display_name"] != fmt.Sprintf("user%d", accessD) {
		t.Fatalf("newest entry = %v", newest)
	}
	text := fmt.Sprint(body)
	for _, leak := range []string{"dan@external.invalid", "carol@external.invalid", f.mailbox.Address, "refresh", "mailbox_id"} {
		if strings.Contains(text, leak) {
			t.Fatalf("the connection directory leaks %q: %v", leak, body)
		}
	}
	meta, _ := body["meta"].(map[string]any)
	cursor, _ := meta["next_cursor"].(string)
	if cursor == "" || meta["has_more"] != true {
		t.Fatalf("first page meta = %v; want a next cursor", meta)
	}
	status, body = f.call(t, f.admin, http.MethodGet, "/external-mail-connection-directory?per_page=2&cursor="+cursor)
	items, _ = body["data"].([]any)
	if status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["masked_account_address"] != "b.***@gmail.example" {
		t.Fatalf("second page = %d %v", status, body)
	}

	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/external-mail-connection-directory"},
		{http.MethodDelete, fmt.Sprintf("/external-mail-connection-directory/%d", f.connection.ID)},
	} {
		if memberStatus, memberBody := f.call(t, f.b, request.method, request.path); memberStatus != http.StatusForbidden {
			t.Fatalf("member %s %s = %d %v; want 403", request.method, request.path, memberStatus, memberBody)
		}
	}
	if status, _ = f.call(t, f.admin, http.MethodGet, "/external-mail-connection-directory?cursor=bad!"); status != http.StatusBadRequest {
		t.Fatalf("malformed cursor = %d; want 400", status)
	}
}

func TestMaskAccountAddress(t *testing.T) {
	for address, want := range map[string]string{
		"stark@gmail.com": "st***@gmail.com",
		"ab@outlook.com":  "a***@outlook.com",
		"a@example.com":   "a***@example.com",
		"@example.com":    "***@example.com",
		"no-at-sign":      "***",
		"zhāng@mail.cn":   "zh***@mail.cn",
	} {
		if got := maskAccountAddress(address); got != want {
			t.Fatalf("maskAccountAddress(%q) = %q, want %q", address, got, want)
		}
	}
}

// TestForcedDisconnectRevokesAndIsRecorded: an admin's forced disconnect has the owner's revoke
// effect, and every connection change lands in the mailbox's access history without the address.
func TestForcedDisconnectRevokesAndIsRecorded(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	carol := f.bind(t, f.c, domain.ExternalMailProviderOutlook, "carol")
	// Carol signs in again: a reconnect.
	state := f.signIn(t, f.c, ExternalMailAuthorizationInput{ConnectionID: &carol.ID})
	if _, created, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "carol"); err != nil || created {
		t.Fatalf("reconnect = created %v, err %v", created, err)
	}

	status, body := f.call(t, f.admin, http.MethodDelete, fmt.Sprintf("/external-mail-connection-directory/%d", carol.ID))
	if status != http.StatusNoContent {
		t.Fatalf("forced disconnect = %d %v", status, body)
	}
	revoked, err := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, carol.ID)
	if err != nil || revoked.Status != domain.ExternalMailStatusRevoked || revoked.EncryptedRefreshToken != "" {
		t.Fatalf("after the forced disconnect = %+v, err %v", revoked, err)
	}
	if !f.source.Revoked("fake-carol") {
		t.Fatal("the forced disconnect did not hand the grant back to the provider")
	}
	// Repeating it changes and records nothing; an unknown id is not found.
	if status, _ = f.call(t, f.admin, http.MethodDelete, fmt.Sprintf("/external-mail-connection-directory/%d", carol.ID)); status != http.StatusNoContent {
		t.Fatalf("repeated forced disconnect = %d", status)
	}
	if status, _ = f.call(t, f.admin, http.MethodDelete, "/external-mail-connection-directory/99999"); status != http.StatusNotFound {
		t.Fatalf("unknown connection = %d; want 404", status)
	}

	// B unbinds their own; then B leaves after binding another account.
	if err = f.service.RevokeExternalMailConnection(ctx, f.b, f.connection.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	second := f.bind(t, f.b, domain.ExternalMailProviderOutlook, "bee")
	f.leave(t, f.b, f.service.now())

	history := func(mailboxID uint) []*domain.MailboxAccessEvent {
		t.Helper()
		events, _, listErr := f.repo.ListMailboxAccessEvents(ctx, mailboxID, 1, 50)
		if listErr != nil {
			t.Fatalf("ListMailboxAccessEvents: %v", listErr)
		}
		return events
	}
	types := func(events []*domain.MailboxAccessEvent) []domain.MailboxAccessEventType {
		out := make([]domain.MailboxAccessEventType, 0, len(events))
		for i := len(events) - 1; i >= 0; i-- {
			out = append(out, events[i].Type)
		}
		return out
	}
	want := []domain.MailboxAccessEventType{
		domain.MailboxAccessConnectionBound, domain.MailboxAccessConnectionReconnected, domain.MailboxAccessConnectionForceRevoked,
	}
	carolEvents := history(carol.MailboxID)
	if got := types(carolEvents); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("carol's history = %v, want %v", got, want)
	}
	forced := carolEvents[0]
	if forced.ActorUserID == nil || *forced.ActorUserID != accessAdmin || forced.SubjectUserID == nil || *forced.SubjectUserID != accessC {
		t.Fatalf("forced disconnect event = %+v", forced)
	}
	if got := types(history(f.mailbox.ID)); fmt.Sprint(got) != fmt.Sprint([]domain.MailboxAccessEventType{domain.MailboxAccessConnectionRevoked}) {
		t.Fatalf("b's first history = %v", got)
	}
	leftEvents := history(second.MailboxID)
	if got := types(leftEvents); fmt.Sprint(got) != fmt.Sprint([]domain.MailboxAccessEventType{
		domain.MailboxAccessConnectionBound, domain.MailboxAccessConnectionRevokedLeave,
	}) {
		t.Fatalf("b's second history = %v", got)
	}
	if leftEvents[0].ActorUserID != nil {
		t.Fatalf("the leave disconnect has actor %v; want the system", *leftEvents[0].ActorUserID)
	}
	for _, events := range [][]*domain.MailboxAccessEvent{carolEvents, leftEvents} {
		for _, event := range events {
			if strings.Contains(fmt.Sprint(event.Detail), "@") || event.Detail["connection_id"] == nil || event.Detail["provider"] == nil {
				t.Fatalf("event detail = %v; want connection_id and provider, never the address", event.Detail)
			}
		}
	}
}

// createConnected stores a member's connected personal mailbox with the given address directly.
func createConnected(t *testing.T, repo *repository, organizationID, ownerUserID uint, address string) *domain.Mailbox {
	t.Helper()
	mailbox := &domain.Mailbox{OrganizationID: organizationID, CreatedBy: ownerUserID, Address: address,
		Type: domain.MailboxTypePersonal, Origin: domain.MailboxOriginConnected, OwnerUserID: &ownerUserID}
	if err := repo.CreateMailbox(context.Background(), mailbox, 0); err != nil {
		t.Fatalf("CreateMailbox(%q): %v", address, err)
	}
	return mailbox
}

// TestConnectedAddressesStayOutOfDomains: a member's connected copy of an address on an
// organization domain gets no routing rule and neither blocks nor reveals itself to a domain
// deletion.
func TestConnectedAddressesStayOutOfDomains(t *testing.T) {
	repo := openDomainRepository(t)
	ctx := context.Background()
	mailDomain := &domain.MailDomain{OrganizationID: 1, Name: "news.example.com", ZoneID: "z", ZoneName: "example.com", CreatedBy: 1}
	if err := repo.CreateDomain(ctx, mailDomain, 10); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	createConnected(t, repo, 1, 7, "member@news.example.com")

	listed, err := repo.ListMailboxesWithoutRoutingRule(ctx, 1, "news.example.com")
	if err != nil || len(listed) != 0 {
		t.Fatalf("unrouted mailboxes = %+v, %v; want none for a connected address", listed, err)
	}
	if err := repo.DeleteDomain(ctx, 1, mailDomain.ID); err != nil {
		t.Fatalf("DeleteDomain with only a connected address on it: %v", err)
	}
}

// TestListingsNeverLoadOtherMembersConnectedMailboxes: the repository reads behind mailbox lists
// and the unified inbox drop another member's connected personal mailbox in SQL, keep the
// viewer's own, and a connected mailbox is created whatever the organization's limit.
func TestListingsNeverLoadOtherMembersConnectedMailboxes(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	repo := f.repo.(*repository)
	ctx := context.Background()
	has := func(ids []uint) bool { return slices.Contains(ids, f.mailbox.ID) }

	for _, viewer := range []domain.OrganizationContext{f.admin, f.b} {
		listed, err := repo.ListMailboxes(ctx, accessOrg, viewer.MembershipID, viewer.UserID)
		if err != nil {
			t.Fatalf("ListMailboxes: %v", err)
		}
		rows, err := repo.ListMailboxAccessRows(ctx, accessOrg, viewer.MembershipID, viewer.UserID)
		if err != nil {
			t.Fatalf("ListMailboxAccessRows: %v", err)
		}
		listedIDs, rowIDs := []uint{}, []uint{}
		for _, mailbox := range listed {
			listedIDs = append(listedIDs, mailbox.ID)
		}
		for _, row := range rows {
			rowIDs = append(rowIDs, row.Mailbox.ID)
		}
		owner := viewer.UserID == accessB
		if has(listedIDs) != owner || has(rowIDs) != owner {
			t.Fatalf("viewer %d loads the connected mailbox: listing %v, access rows %v; want %v",
				viewer.UserID, has(listedIDs), has(rowIDs), owner)
		}
	}

	extra := createConnected(t, repo, accessOrg, accessB, "second@gmail.example")
	if extra.ID == 0 {
		t.Fatal("a connected mailbox was not created under a zero organization limit")
	}
	if err := repo.CreateMailbox(ctx, &domain.Mailbox{OrganizationID: accessOrg, Address: "x@mail.example", Type: domain.MailboxTypeShared}, 0); !errors.Is(err, ErrMailboxLimitReached) {
		t.Fatalf("a Zmail mailbox under a zero limit: err = %v, want limit reached", err)
	}
}

// TestConnectedTriageStopsWhenTheConnectionDoes: leaving the organization turns the connected
// mailbox's triage switch off and drops its pending rows, and a connected mailbox whose connection
// is not active neither queues nor hands rows to the model.
func TestConnectedTriageStopsWhenTheConnectionDoes(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	repo := f.repo.(*repository)
	f.service.settings.Triage = config.AITriageConfig{Provider: config.AITriageProviderFake, DailyCap: 100}
	ctx := context.Background()
	f.switchTriage(t, true)
	if _, err := f.service.SetMailboxTriage(ctx, f.b, f.mailbox.ID, true); err != nil {
		t.Fatalf("owner switching triage on: %v", err)
	}
	f.source.Deliver(externalTestToken, externalMessage("g-1", "Pending", time.Time{}))
	f.sync(t)
	pending := func() int64 {
		t.Helper()
		var count int64
		if err := repo.db.Model(&MailTriagePO{}).Where("mailbox_id = ? AND status = ?", f.mailbox.ID, "pending").Count(&count).Error; err != nil {
			t.Fatalf("count pending triage: %v", err)
		}
		return count
	}
	if pending() != 1 {
		t.Fatalf("pending triage rows before leaving = %d, want 1", pending())
	}

	// A connection that is no longer active blocks queueing and claiming on its own.
	if err := repo.db.Exec(`UPDATE external_mail_connections SET status = 'needs_reconnect' WHERE id = ?`, f.connection.ID).Error; err != nil {
		t.Fatalf("mark needs_reconnect: %v", err)
	}
	extra := &domain.MailMessage{MailboxID: f.mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "x@sender.test", Subject: "Late", ExternalID: "g-late", Source: domain.MailSourceGmail, DedupeKey: "late"}
	if _, err := repo.CreateInboundMessage(ctx, extra); err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	if queued, err := repo.EnqueueTriage(ctx, extra.ID, f.mailbox.ID, f.service.now()); err != nil || queued {
		t.Fatalf("EnqueueTriage on an inactive connection = %v, %v; want not queued", queued, err)
	}
	claims, err := repo.ClaimTriage(ctx, f.service.now().Add(time.Hour), 10)
	if err != nil || len(claims) != 1 || claims[0].MailboxOn {
		t.Fatalf("claims = %+v, %v; want the pending row with its mailbox off", claims, err)
	}
	if err = repo.db.Exec(`UPDATE external_mail_connections SET status = 'active' WHERE id = ?`, f.connection.ID).Error; err != nil {
		t.Fatalf("mark active: %v", err)
	}
	if err = repo.db.Exec(`UPDATE mail_triage SET status = 'pending' WHERE mailbox_id = ?`, f.mailbox.ID).Error; err != nil {
		t.Fatalf("reset pending: %v", err)
	}

	f.leave(t, f.b, f.service.now())
	if pending() != 0 {
		t.Fatalf("pending triage rows after leaving = %d, want 0", pending())
	}
	mailbox, err := f.repo.FindMailbox(ctx, accessOrg, f.mailbox.ID)
	if err != nil || mailbox.AITriageEnabled {
		t.Fatalf("connected mailbox after leaving = %+v, %v; want triage off", mailbox, err)
	}
}

// TestDeletingAConnectedMailboxRevokesItsConnection: no stored grant outlives its mailbox. A
// connected shared mailbox (the only kind a manager administers) is deleted together with its
// connection's revoke: the grant is handed back and the token erased.
func TestDeletingAConnectedMailboxRevokesItsConnection(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	repo := f.repo.(*repository)
	ctx := context.Background()
	if err := repo.db.Exec(`UPDATE mailboxes SET type = 'shared', owner_user_id = NULL WHERE id = ?`, f.mailbox.ID).Error; err != nil {
		t.Fatalf("make the mailbox shared: %v", err)
	}
	if err := f.service.DeleteMailbox(ctx, f.admin, f.mailbox.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	connection := f.reload(t)
	if connection.Status != domain.ExternalMailStatusRevoked || connection.EncryptedRefreshToken != "" || connection.RevokedAt == nil {
		t.Fatalf("connection after deleting its mailbox = %+v; want revoked, token erased, revoked_at set", connection)
	}
	if !f.source.Revoked(externalTestToken) {
		t.Fatal("the grant was not handed back when its mailbox was deleted")
	}
	if _, err := f.repo.FindMailbox(ctx, accessOrg, f.mailbox.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("mailbox after delete: err = %v, want not found", err)
	}
}

// TestForcedDisconnectIsAudited: a forced disconnect lands in the organization audit log with the
// connection and provider, never the address; repeating it records nothing more, and the owner's
// own revoke is not a forced disconnect.
func TestForcedDisconnectIsAudited(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	var changes []auditstarter.Change
	f.service.recordAudit = func(_ context.Context, change auditstarter.Change) { changes = append(changes, change) }

	carol := f.bind(t, f.c, domain.ExternalMailProviderOutlook, "carol")
	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, f.connection.ID); err != nil {
		t.Fatalf("ForceDisconnectExternalMailConnection: %v", err)
	}
	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, f.connection.ID); err != nil {
		t.Fatalf("repeated forced disconnect: %v", err)
	}
	// The manager using the owner's route is a forced disconnect too; the owner's own revoke is not.
	if err := f.service.RevokeExternalMailConnection(ctx, f.admin, carol.ID); err != nil {
		t.Fatalf("manager revoke: %v", err)
	}
	second := f.bind(t, f.d, domain.ExternalMailProviderGmail, "dan")
	if err := f.service.RevokeExternalMailConnection(ctx, f.d, second.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("audit changes = %+v; want two forced disconnects", changes)
	}
	first := changes[0]
	if first.Resource != "external_mail_connections" || first.TargetID != fmt.Sprint(f.connection.ID) ||
		first.Metadata["provider"] != "gmail" || first.Metadata["owner_user_id"] != uint(accessB) {
		t.Fatalf("audit change = %+v", first)
	}
	if strings.Contains(fmt.Sprint(changes), "@") {
		t.Fatalf("the audit log carries an address: %+v", changes)
	}
}

// TestRevokedAccountOfAnotherMemberIsReboundFresh: an account bound by a member stays theirs while
// live, but once their connection is revoked a member who proves control of the account gets it:
// the old copies, mailbox, and connection are purged in the binding's transaction and recorded,
// and the account is bound into a new mailbox.
func TestRevokedAccountOfAnotherMemberIsReboundFresh(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	repo := f.repo.(*repository)
	ctx := context.Background()
	carol := f.bind(t, f.c, domain.ExternalMailProviderGmail, "shared")
	f.source.Deliver("fake-shared", externalMessage("g-old", "Carol's copy", time.Time{}))
	f.sync(t)

	// While Carol's connection is live, Dan is refused.
	state := f.signIn(t, f.d, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail})
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.d, state, "shared"); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Fatalf("binding a live account of another member = %v, want taken", err)
	}

	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, carol.ID); err != nil {
		t.Fatalf("force disconnect: %v", err)
	}
	dan := f.bind(t, f.d, domain.ExternalMailProviderGmail, "shared")
	if dan.ID == carol.ID || dan.MailboxID == carol.MailboxID || dan.OwnerUserID == nil || *dan.OwnerUserID != accessD {
		t.Fatalf("Dan's connection = %+v; want a new connection and mailbox of his own", dan)
	}
	if _, err := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, carol.ID); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("Carol's old connection: err = %v, want purged", err)
	}
	var copies int64
	if err := repo.db.Model(&MailMessagePO{}).Where("mailbox_id = ?", carol.MailboxID).Count(&copies).Error; err != nil || copies != 0 {
		t.Fatalf("Carol's copies after the rebind = %d, %v; want none", copies, err)
	}
	if _, err := f.repo.FindMailbox(ctx, accessOrg, carol.MailboxID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("Carol's old mailbox: err = %v, want deleted", err)
	}
	var reason string
	if err := repo.db.Raw(`SELECT detail->>'reason' FROM mailbox_access_events WHERE mailbox_id = ? AND type = 'copies_purged' AND actor_user_id = ?`,
		carol.MailboxID, accessD).Scan(&reason).Error; err != nil || reason != "rebound" {
		t.Fatalf("copies_purged reason = %q, %v; want rebound by Dan", reason, err)
	}
	if listed, _, err := f.service.ListMessages(ctx, f.d, dan.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10}); err != nil || len(listed) != 0 {
		t.Fatalf("Dan's new mailbox holds %d messages, %v; want none of Carol's copies", len(listed), err)
	}
}
