package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
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
