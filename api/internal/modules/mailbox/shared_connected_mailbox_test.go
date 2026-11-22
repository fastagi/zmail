package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// teamMembers is the usual shared target: C manages the team account and D views it. B, the
// fixture's other member, holds nothing on it.
func teamMembers() *domain.ExternalMailSharing {
	return &domain.ExternalMailSharing{DisplayName: "Support", Members: []domain.ExternalMailSharingMember{
		{UserID: accessC, Permission: domain.MailboxPermissionManage},
		{UserID: accessD, Permission: domain.MailboxPermissionView},
	}}
}

// bindShared signs viewer in to "<code>@external.invalid" as a team account shared as sharing asks.
func (f *externalFixture) bindShared(t *testing.T, viewer domain.OrganizationContext, code string, sharing *domain.ExternalMailSharing) (*domain.ExternalMailConnection, bool, error) {
	t.Helper()
	started, err := f.service.StartExternalMailAuthorization(context.Background(), viewer, ExternalMailAuthorizationInput{
		Provider: domain.ExternalMailProviderGmail, Sharing: sharing,
	})
	if err != nil {
		return nil, false, err
	}
	parsed, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	return f.service.CompleteExternalMailAuthorization(context.Background(), viewer, parsed.Query().Get("state"), code)
}

// mustBindShared binds a new team account or fails the test.
func (f *externalFixture) mustBindShared(t *testing.T, code string, sharing *domain.ExternalMailSharing) *domain.ExternalMailConnection {
	t.Helper()
	connection, created, err := f.bindShared(t, f.admin, code, sharing)
	if err != nil || !created {
		t.Fatalf("shared binding of %s = created %v, err %v", code, created, err)
	}
	return connection
}

// reconnectShared signs viewer in to code as a reconnect of the connection.
func (f *externalFixture) reconnectShared(t *testing.T, viewer domain.OrganizationContext, id uint, code string) (*domain.ExternalMailConnection, bool, error) {
	t.Helper()
	started, err := f.service.StartExternalMailAuthorization(context.Background(), viewer, ExternalMailAuthorizationInput{ConnectionID: &id})
	if err != nil {
		return nil, false, err
	}
	parsed, _ := url.Parse(started.AuthorizationURL)
	return f.service.CompleteExternalMailAuthorization(context.Background(), viewer, parsed.Query().Get("state"), code)
}

func (f *externalFixture) countSharedConnections(t *testing.T) int64 {
	t.Helper()
	return f.count(t, `SELECT COUNT(*) FROM external_mail_connections WHERE shared`)
}

// TestSharedBindingIsForOwnersAndAdmins: only owners and admins start a shared binding, on either
// route; a member is refused before anything is stored or any server is contacted, and a shared
// target never combines with a reconnect.
func TestSharedBindingIsForOwnersAndAdmins(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	if _, _, err := f.bindShared(t, f.c, "support", teamMembers()); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member starting a shared binding = %v, want permission denied", err)
	}
	if _, err := f.service.StartExternalMailAuthorization(ctx, f.admin, ExternalMailAuthorizationInput{
		Provider: domain.ExternalMailProviderGmail, ConnectionID: &f.connection.ID, Sharing: teamMembers(),
	}); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
		t.Fatalf("sharing with connection_id = %v, want invalid", err)
	}
	owner := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleOwner)
	if _, created, err := f.bindShared(t, owner, "support", teamMembers()); err != nil || !created {
		t.Fatalf("owner binding = created %v, err %v", created, err)
	}

	// A role lost between the start and the completion refuses the completion.
	started, err := f.service.StartExternalMailAuthorization(ctx, f.admin, ExternalMailAuthorizationInput{
		Provider: domain.ExternalMailProviderGmail, Sharing: teamMembers(),
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	parsed, _ := url.Parse(started.AuthorizationURL)
	demoted := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleMember)
	if _, _, err = f.service.CompleteExternalMailAuthorization(ctx, demoted, parsed.Query().Get("state"), "sales"); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("demoted completion = %v, want permission denied", err)
	}
	if n := f.countSharedConnections(t); n != 1 {
		t.Fatalf("%d shared connections, want only the owner's", n)
	}

	// Over HTTP a member gets 403 and an admin 201.
	body := `{"provider":"gmail","sharing":{"members":[{"user_id":3,"permission":"manage"}]}}`
	if status, answer := f.callWith(t, f.b, http.MethodPost, "/external-mail-authorizations", body); status != http.StatusForbidden ||
		answer["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("member over HTTP = %d %v", status, answer)
	}
	if status, answer := f.callWith(t, f.admin, http.MethodPost, "/external-mail-authorizations", body); status != http.StatusCreated {
		t.Fatalf("admin over HTTP = %d %v", status, answer)
	}
}

// TestSharedBindingValidatesMembers: the members follow POST /v1/mailboxes, at the start and again
// at the completion, and a refusal stores nothing.
func TestSharedBindingValidatesMembers(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	many := &domain.ExternalMailSharing{}
	for i := range domain.MaxMailboxMembers + 1 {
		many.Members = append(many.Members, domain.ExternalMailSharingMember{UserID: uint(1000 + i), Permission: domain.MailboxPermissionView})
	}
	for name, sharing := range map[string]*domain.ExternalMailSharing{
		"no members":  {},
		"101 members": many,
		"duplicate": {Members: []domain.ExternalMailSharingMember{
			{UserID: accessC, Permission: domain.MailboxPermissionView}, {UserID: accessC, Permission: domain.MailboxPermissionSend},
		}},
		"not a member":   {Members: []domain.ExternalMailSharingMember{{UserID: notMember, Permission: domain.MailboxPermissionView}}},
		"bad permission": {Members: []domain.ExternalMailSharingMember{{UserID: accessC, Permission: "owner"}}},
		"no user":        {Members: []domain.ExternalMailSharingMember{{Permission: domain.MailboxPermissionView}}},
	} {
		if _, err := f.service.StartExternalMailAuthorization(ctx, f.admin, ExternalMailAuthorizationInput{
			Provider: domain.ExternalMailProviderGmail, Sharing: sharing,
		}); !errors.Is(err, ErrMailboxAccessInvalid) {
			t.Errorf("%s = %v, want access invalid", name, err)
		}
	}
	badName := teamMembers()
	badName.DisplayName = "<script>"
	if _, err := f.service.StartExternalMailAuthorization(ctx, f.admin, ExternalMailAuthorizationInput{
		Provider: domain.ExternalMailProviderGmail, Sharing: badName,
	}); !errors.Is(err, ErrMailboxAddressInvalid) {
		t.Fatalf("bad display name = %v, want address invalid", err)
	}

	// D leaves between the start and the completion.
	started, err := f.service.StartExternalMailAuthorization(ctx, f.admin, ExternalMailAuthorizationInput{
		Provider: domain.ExternalMailProviderGmail, Sharing: teamMembers(),
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	f.memberships.absent[accessD] = true
	parsed, _ := url.Parse(started.AuthorizationURL)
	if _, _, err = f.service.CompleteExternalMailAuthorization(ctx, f.admin, parsed.Query().Get("state"), "support"); !errors.Is(err, ErrMailboxAccessInvalid) {
		t.Fatalf("completion after a member left = %v, want access invalid", err)
	}
	if n := f.countSharedConnections(t); n != 0 {
		t.Fatalf("%d shared connections stored after refusals", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM mailboxes WHERE type = 'shared' AND origin = 'connected'`); n != 0 {
		t.Fatalf("%d shared connected mailboxes stored after refusals", n)
	}
	// Over HTTP the refusal is MAILBOX.ACCESS_INVALID, as on POST /v1/mailboxes.
	status, answer := f.callWith(t, f.admin, http.MethodPost, "/external-mail-authorizations", `{"provider":"gmail","sharing":{"members":[]}}`)
	if status != http.StatusUnprocessableEntity || answer["error_code"] != domain.CodeMailboxAccessInvalid {
		t.Fatalf("empty members over HTTP = %d %v", status, answer)
	}
}

// TestSharedBindingCreatesTheTeamMailbox: a completed shared sign-in creates a shared, connected,
// ownerless mailbox with its members and history, and an organization connection recording who
// bound it, which then syncs the account's mail.
func TestSharedBindingCreatesTheTeamMailbox(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	connection := f.mustBindShared(t, "support", teamMembers())

	if !connection.Shared || connection.OwnerUserID != nil || connection.BoundByUserID == nil || *connection.BoundByUserID != accessAdmin ||
		connection.AccountAddress != "support@external.invalid" || connection.Status != domain.ExternalMailStatusActive {
		t.Fatalf("connection = %+v", connection)
	}
	mailbox, err := f.repo.FindMailbox(ctx, accessOrg, connection.MailboxID)
	if err != nil || mailbox.Type != domain.MailboxTypeShared || !mailbox.Connected() || mailbox.OwnerUserID != nil ||
		mailbox.DisplayName != "Support" || mailbox.Address != connection.AccountAddress || mailbox.WebhooksEnabled {
		t.Fatalf("mailbox = %+v, err %v", mailbox, err)
	}
	members, err := f.service.ListMailboxMembers(ctx, f.c, mailbox.ID)
	if err != nil || len(members) != 2 || members[0].UserID != accessC || members[0].Permission != domain.MailboxPermissionManage ||
		members[1].UserID != accessD || members[1].Permission != domain.MailboxPermissionView {
		t.Fatalf("members = %+v, err %v", members, err)
	}
	events := accessEvents(t, f.accessFixture, f.admin, mailbox.ID)
	types := []domain.MailboxAccessEventType{}
	for _, event := range events {
		types = append(types, event.Type)
	}
	want := fmt.Sprint([]domain.MailboxAccessEventType{domain.MailboxAccessConnectionBound, domain.MailboxAccessMemberSet,
		domain.MailboxAccessMemberSet, domain.MailboxAccessCreated})
	if fmt.Sprint(types) != want {
		t.Fatalf("history = %v, want %v (newest first)", types, want)
	}
	if bound := events[0]; !sameID(bound.ActorUserID, accessAdmin) || bound.SubjectUserID != nil || strings.Contains(fmt.Sprint(bound.Detail), "@") {
		t.Fatalf("connection_bound = %+v", bound)
	}
	// The binder holds none of their own ten places.
	if live, _ := f.service.externalMail.CountLiveExternalMailConnections(ctx, accessOrg, accessAdmin); live != 0 {
		t.Fatalf("the binder holds %d personal connections, want 0", live)
	}

	f.source.Deliver("fake-support", externalMessage("s-1", "Ticket", time.Time{}))
	f.sync(t)
	messages, _, err := f.service.ListMessages(ctx, f.d, mailbox.ID, domain.MailMessageFilter{Page: 1, PerPage: 10})
	if err != nil || len(messages) != 1 || messages[0].Subject != "Ticket" {
		t.Fatalf("D's listing = %d messages, err %v", len(messages), err)
	}

	// Without a display name the mailbox is named after the account.
	unnamed := teamMembers()
	unnamed.DisplayName = ""
	other := f.mustBindShared(t, "sales", unnamed)
	otherMailbox, _ := f.repo.FindMailbox(ctx, accessOrg, other.MailboxID)
	if otherMailbox.DisplayName != "sales@external.invalid" {
		t.Fatalf("default display name = %q", otherMailbox.DisplayName)
	}
}

// TestSharedIMAPBinding: the app password route binds a team account the same way, refuses members,
// and restores a shared connection through connection_id for whoever manages it.
func TestSharedIMAPBinding(t *testing.T) {
	f, server := newIMAPFixture(t)
	ctx := context.Background()
	input := imapInput(server, "support@imap.test")
	input.Sharing = teamMembers()

	if _, _, err := f.service.BindExternalMailCredentials(ctx, f.b, input); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member binding a team account = %v, want permission denied", err)
	}
	connection, created, err := f.service.BindExternalMailCredentials(ctx, f.admin, input)
	if err != nil || !created || !connection.Shared || connection.OwnerUserID != nil || connection.AccountAddress != "support@imap.test" {
		t.Fatalf("admin binding = %+v, created %v, err %v", connection, created, err)
	}
	mailbox, _ := f.repo.FindMailbox(ctx, accessOrg, connection.MailboxID)
	if mailbox.Type != domain.MailboxTypeShared || !mailbox.Connected() {
		t.Fatalf("mailbox = %+v", mailbox)
	}
	// Binding the same team account again is not a restore: it already has its mailbox.
	if _, _, err = f.service.BindExternalMailCredentials(ctx, f.admin, input); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Fatalf("second shared binding = %v, want taken", err)
	}
	both := input
	both.ConnectionID = &connection.ID
	if _, _, err = f.service.BindExternalMailCredentials(ctx, f.admin, both); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
		t.Fatalf("sharing with connection_id = %v, want invalid", err)
	}

	reconnect := imapInput(server, "support@imap.test")
	reconnect.ConnectionID = &connection.ID
	if _, _, err = f.service.BindExternalMailCredentials(ctx, f.d, reconnect); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("view member reconnecting = %v, want not found", err)
	}
	restored, created, err := f.service.BindExternalMailCredentials(ctx, f.c, reconnect)
	if err != nil || created || restored.ID != connection.ID {
		t.Fatalf("manage member reconnecting = %+v, created %v, err %v", restored, created, err)
	}
	wrong := imapInput(server, "other@imap.test")
	wrong.ConnectionID = &connection.ID
	if _, _, err = f.service.BindExternalMailCredentials(ctx, f.c, wrong); !errors.Is(err, ErrExternalMailAccountMismatch) {
		t.Fatalf("reconnect to another account = %v, want mismatch", err)
	}
	// Without connection_id the manager's request is a personal binding of the team's account.
	if _, _, err = f.service.BindExternalMailCredentials(ctx, f.c, imapInput(server, "support@imap.test")); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Fatalf("personal binding of the team account = %v, want taken", err)
	}

	// Over HTTP the shared target and connection_id are read from the body.
	payload := fmt.Sprintf(`{"provider":"imap","host":"127.0.0.1","port":%d,"username":"desk@imap.test","password":%q,
		"sharing":{"display_name":"Desk","members":[{"user_id":%d,"permission":"view"}]}}`, server.port, fakeIMAPPassword, accessC)
	status, answer := f.callWith(t, f.admin, http.MethodPost, "/external-mail-credentials", payload)
	data, _ := answer["data"].(map[string]any)
	if status != http.StatusCreated || data["shared"] != true || data["account_address"] != "desk@imap.test" {
		t.Fatalf("HTTP shared credentials = %d %v", status, answer)
	}
}

// TestSharedConnectedMailboxIsReadByMembersOnly: each member reads with their own read state; a
// colleague without a member row and an admin who is not a member read nothing until the admin adds
// themselves, which is recorded.
func TestSharedConnectedMailboxIsReadByMembersOnly(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	connection := f.mustBindShared(t, "support", teamMembers())
	f.source.Deliver("fake-support", externalMessage("s-1", "Ticket", time.Time{}))
	f.sync(t)
	filter := domain.MailMessageFilter{Page: 1, PerPage: 10}

	cMessages, _, err := f.service.ListMessages(ctx, f.c, connection.MailboxID, filter)
	if err != nil || len(cMessages) != 1 || cMessages[0].Read {
		t.Fatalf("C's listing = %+v, err %v", cMessages, err)
	}
	if _, err = f.service.SetMessageRead(ctx, f.c, connection.MailboxID, cMessages[0].ID, true); err != nil {
		t.Fatalf("C marking read: %v", err)
	}
	cMessages, _, _ = f.service.ListMessages(ctx, f.c, connection.MailboxID, filter)
	dMessages, _, err := f.service.ListMessages(ctx, f.d, connection.MailboxID, filter)
	if err != nil || len(dMessages) != 1 || !cMessages[0].Read || dMessages[0].Read {
		t.Fatalf("read state: C %v, D %v (err %v); want read for C only", cMessages[0].Read, dMessages[0].Read, err)
	}
	if _, _, err = f.service.ListMessages(ctx, f.b, connection.MailboxID, filter); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("non-member listing = %v, want not found", err)
	}
	if _, err = f.service.GetMessage(ctx, f.b, connection.MailboxID, cMessages[0].ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("non-member reading = %v, want not found", err)
	}
	if _, _, err = f.service.ListMessages(ctx, f.admin, connection.MailboxID, filter); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("non-member admin listing = %v, want permission denied", err)
	}
	if listed := listedMailbox(t, f.service, f.admin, connection.MailboxID); listed == nil || listed.Access.View || !listed.Access.ManageMembers {
		t.Fatalf("admin's access = %+v; want members management without reading", listed)
	}

	if _, err = f.service.SetMailboxMember(ctx, f.admin, connection.MailboxID, accessAdmin, domain.MailboxPermissionView); err != nil {
		t.Fatalf("admin adding themselves: %v", err)
	}
	if latest := accessEvents(t, f.accessFixture, f.admin, connection.MailboxID)[0]; latest.Type != domain.MailboxAccessMemberSet ||
		!sameID(latest.ActorUserID, accessAdmin) || !sameID(latest.SubjectUserID, accessAdmin) {
		t.Fatalf("self-grant event = %+v", latest)
	}
	adminMessages, _, err := f.service.ListMessages(ctx, f.admin, connection.MailboxID, filter)
	if err != nil || len(adminMessages) != 1 || adminMessages[0].Read {
		t.Fatalf("admin's listing after joining = %+v, err %v", adminMessages, err)
	}
}

// TestSharedConnectedMailboxNeverSends: the origin is read-only whatever the permission: no member
// sends, replies, or organizes destructively, and handover does not apply.
func TestSharedConnectedMailboxNeverSends(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	connection := f.mustBindShared(t, "support", teamMembers())
	if _, err := f.service.SetMailboxMember(ctx, f.c, connection.MailboxID, accessD, domain.MailboxPermissionSend); err != nil {
		t.Fatalf("raising D to send: %v", err)
	}
	f.source.Deliver("fake-support", externalMessage("s-1", "Ticket", time.Time{}))
	f.sync(t)
	messages, _, _ := f.service.ListMessages(ctx, f.c, connection.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10})

	for _, viewer := range []domain.OrganizationContext{f.c, f.d, f.admin} {
		listed := listedMailbox(t, f.service, viewer, connection.MailboxID)
		if listed == nil || listed.Access.Send || listed.Access.Organize {
			t.Fatalf("viewer %d access = %+v; want neither send nor organize", viewer.UserID, listed)
		}
		if _, _, err := f.service.SendMessage(ctx, viewer, connection.MailboxID, SendInput{To: []string{"x@example.com"}, Subject: "Hi", Text: "Hi"}); err == nil {
			t.Fatalf("viewer %d sent from the team account", viewer.UserID)
		}
		reply := SendInput{To: []string{"carol@sender.test"}, Subject: "Re: Ticket", Text: "On it", InReplyTo: messages[0].RFCMessageID}
		if _, _, err := f.service.SendMessage(ctx, viewer, connection.MailboxID, reply); err == nil {
			t.Fatalf("viewer %d replied from the team account", viewer.UserID)
		}
	}
	if _, err := f.service.HandOverMailbox(ctx, f.admin, connection.MailboxID, accessC); err == nil {
		t.Fatal("a shared connected mailbox was handed over")
	}
}

// TestSharedConnectionOutlivesTheBinder: the connection is the organization's, so the binder
// leaving neither revokes it nor touches its mailbox.
func TestSharedConnectionOutlivesTheBinder(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	connection := f.mustBindShared(t, "support", teamMembers())
	f.leave(t, f.admin, f.service.now())
	stored, err := f.service.externalMail.FindExternalMailConnection(context.Background(), accessOrg, connection.ID)
	if err != nil || stored.Status != domain.ExternalMailStatusActive || stored.EncryptedRefreshToken == "" || f.source.Revoked("fake-support") {
		t.Fatalf("after the binder left: %+v, err %v", stored, err)
	}
	f.source.Deliver("fake-support", externalMessage("s-1", "Still synced", time.Time{}))
	f.sync(t)
	if messages, _, _ := f.service.ListMessages(context.Background(), f.c, connection.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10}); len(messages) != 1 {
		t.Fatalf("members read %d messages after the binder left, want 1", len(messages))
	}
}

// TestSharedConnectionLimit: an organization holds at most its configured live shared connections;
// revoked ones do not count until they are restored.
func TestSharedConnectionLimit(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.service.settings.ExternalMail.MaxSharedConnections = 2
	ctx := context.Background()
	first := f.mustBindShared(t, "one", teamMembers())
	f.mustBindShared(t, "two", teamMembers())
	if _, _, err := f.bindShared(t, f.admin, "three", teamMembers()); !errors.Is(err, ErrExternalMailConnectionLimit) {
		t.Fatalf("third shared binding = %v, want limit", err)
	}
	if !f.source.Revoked("fake-three") {
		t.Fatal("a refused grant was not handed back")
	}
	// Personal connections live under their own limit.
	f.bind(t, f.c, domain.ExternalMailProviderGmail, "carol")

	if err := f.service.RevokeExternalMailConnection(ctx, f.admin, first.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	f.mustBindShared(t, "three", teamMembers())
	if _, _, err := f.reconnectShared(t, f.admin, first.ID, "one"); !errors.Is(err, ErrExternalMailConnectionLimit) {
		t.Fatalf("restoring a revoked shared connection at the limit = %v, want limit", err)
	}
}

// TestSharedAndPersonalAccountsAreExclusive: within one organization and connector an account is a
// member's own or the team's, never both. A revoked personal connection is freed as for any binder;
// a revoked shared one stays the team's while its mailbox exists.
func TestSharedAndPersonalAccountsAreExclusive(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	carol := f.bind(t, f.c, domain.ExternalMailProviderGmail, "carol")
	if _, _, err := f.bindShared(t, f.admin, "carol", teamMembers()); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Fatalf("sharing a member's live account = %v, want taken", err)
	}
	team := f.mustBindShared(t, "team", teamMembers())
	state := f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail})
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "team"); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Fatalf("binding the team account as one's own = %v, want taken", err)
	}
	// A personal reconnect that signs in to the team account is the wrong account.
	if _, _, err := f.reconnectShared(t, f.c, carol.ID, "team"); !errors.Is(err, ErrExternalMailAccountMismatch) {
		t.Fatalf("personal reconnect into the team account = %v, want mismatch", err)
	}

	if err := f.service.RevokeExternalMailConnection(ctx, f.c, carol.ID); err != nil {
		t.Fatalf("revoke carol: %v", err)
	}
	shared, created, err := f.bindShared(t, f.admin, "carol", teamMembers())
	if err != nil || !created || shared.ID == carol.ID || !shared.Shared {
		t.Fatalf("sharing a revoked personal account = %+v, created %v, err %v", shared, created, err)
	}
	if _, err = f.repo.FindMailbox(ctx, accessOrg, carol.MailboxID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("the revoked personal mailbox survived: %v", err)
	}

	if err = f.service.RevokeExternalMailConnection(ctx, f.admin, team.ID); err != nil {
		t.Fatalf("disconnect team: %v", err)
	}
	state = f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail})
	if _, _, err = f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "team"); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Fatalf("taking a revoked team account whose mailbox exists = %v, want taken", err)
	}
	if _, _, err = f.bindShared(t, f.admin, "team", teamMembers()); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Fatalf("binding a revoked team account again instead of reconnecting = %v, want taken", err)
	}
	if err = f.service.DeleteMailbox(ctx, f.admin, team.MailboxID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	own := f.bind(t, f.c, domain.ExternalMailProviderGmail, "team")
	if own.Shared || own.OwnerUserID == nil || *own.OwnerUserID != accessC {
		t.Fatalf("binding a team account whose mailbox was deleted = %+v", own)
	}
}

// TestSharedReconnectAndDisconnectRules: whoever manages the mailbox's members (a manage member, an
// owner or admin) reconnects, unbinds, reads the sync runs, and purges; a view member and an
// outsider see no such connection.
func TestSharedReconnectAndDisconnectRules(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	var changes []auditstarter.Change
	f.service.recordAudit = func(_ context.Context, change auditstarter.Change) { changes = append(changes, change) }
	connection := f.mustBindShared(t, "support", teamMembers())
	f.sync(t)

	for _, outsider := range []domain.OrganizationContext{f.d, f.b} {
		if _, err := f.service.StartExternalMailAuthorization(ctx, outsider, ExternalMailAuthorizationInput{ConnectionID: &connection.ID}); !errors.Is(err, ErrExternalMailConnectionNotFound) {
			t.Fatalf("viewer %d reconnecting = %v, want not found", outsider.UserID, err)
		}
		if _, err := f.service.ListExternalMailSyncRuns(ctx, outsider, connection.ID); !errors.Is(err, ErrExternalMailConnectionNotFound) {
			t.Fatalf("viewer %d reading sync runs = %v, want not found", outsider.UserID, err)
		}
		if err := f.service.RevokeExternalMailConnection(ctx, outsider, connection.ID); !errors.Is(err, ErrExternalMailConnectionNotFound) {
			t.Fatalf("viewer %d unbinding = %v, want not found", outsider.UserID, err)
		}
	}
	if runs, err := f.service.ListExternalMailSyncRuns(ctx, f.c, connection.ID); err != nil || len(runs) != 1 {
		t.Fatalf("manage member's sync runs = %d, err %v", len(runs), err)
	}

	f.source.FailAuth("fake-support")
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if _, _, err := f.reconnectShared(t, f.c, connection.ID, "someone-else"); !errors.Is(err, ErrExternalMailAccountMismatch) {
		t.Fatalf("reconnect to another account = %v, want mismatch", err)
	}
	restored, created, err := f.reconnectShared(t, f.c, connection.ID, "support")
	if err != nil || created || restored.ID != connection.ID || restored.Status != domain.ExternalMailStatusActive {
		t.Fatalf("manage member reconnecting = %+v, created %v, err %v", restored, created, err)
	}
	if latest := accessEvents(t, f.accessFixture, f.admin, connection.MailboxID)[0]; latest.Type != domain.MailboxAccessConnectionReconnected ||
		!sameID(latest.ActorUserID, accessC) {
		t.Fatalf("reconnect event = %+v", latest)
	}

	// A manage member demoted between the start and the completion is refused, and the grant handed back.
	started, err := f.service.StartExternalMailAuthorization(ctx, f.c, ExternalMailAuthorizationInput{ConnectionID: &connection.ID})
	if err != nil {
		t.Fatalf("start reconnect: %v", err)
	}
	if _, err = f.service.SetMailboxMember(ctx, f.admin, connection.MailboxID, accessC, domain.MailboxPermissionView); err != nil {
		t.Fatalf("demote C: %v", err)
	}
	parsed, _ := url.Parse(started.AuthorizationURL)
	if _, _, err = f.service.CompleteExternalMailAuthorization(ctx, f.c, parsed.Query().Get("state"), "support"); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("demoted completion = %v, want not found", err)
	}
	if _, err = f.service.SetMailboxMember(ctx, f.admin, connection.MailboxID, accessC, domain.MailboxPermissionManage); err != nil {
		t.Fatalf("restore C: %v", err)
	}

	if err = f.service.RevokeExternalMailConnection(ctx, f.c, connection.ID); err != nil {
		t.Fatalf("manage member unbinding: %v", err)
	}
	stored := f.reloadConnection(t, connection.ID)
	if stored.Status != domain.ExternalMailStatusRevoked || stored.RevokedAt == nil || stored.EncryptedRefreshToken != "" || !f.source.Revoked("fake-support") {
		t.Fatalf("after unbinding = %+v", stored)
	}
	latest := accessEvents(t, f.accessFixture, f.admin, connection.MailboxID)[0]
	if latest.Type != domain.MailboxAccessConnectionRevoked || !sameID(latest.ActorUserID, accessC) || latest.SubjectUserID != nil {
		t.Fatalf("revoke event = %+v", latest)
	}
	if len(changes) != 1 || changes[0].Metadata["reason"] != "shared_disconnect" || strings.Contains(fmt.Sprint(changes), "@") {
		t.Fatalf("audit = %+v", changes)
	}

	// The admin restores it, and an admin disconnects it again through the directory.
	if _, _, err = f.reconnectShared(t, f.admin, connection.ID, "support"); err != nil {
		t.Fatalf("admin restoring: %v", err)
	}
	if status, body := f.call(t, f.admin, http.MethodDelete, fmt.Sprintf("/external-mail-connection-directory/%d", connection.ID)); status != http.StatusNoContent {
		t.Fatalf("directory disconnect = %d %v", status, body)
	}
	if latest = accessEvents(t, f.accessFixture, f.admin, connection.MailboxID)[0]; latest.Type != domain.MailboxAccessConnectionRevoked {
		t.Fatalf("directory disconnect event = %+v", latest)
	}
	if _, err = f.service.PurgeExternalMailCopies(ctx, f.d, connection.ID); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("view member purging = %v, want not found", err)
	}
	// Purging deletes the mailbox, so it is for those who may delete it: not a manage member.
	if _, err = f.service.PurgeExternalMailCopies(ctx, f.c, connection.ID); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("manage member purging = %v, want permission denied", err)
	}
	if status, body := f.opsCall(t, f.c, http.MethodPost, fmt.Sprintf("/external-mail-connections/%d/purges", connection.ID)); status != http.StatusForbidden {
		t.Fatalf("manage member purging over HTTP = %d %v", status, body)
	}
	if _, err = f.repo.FindMailbox(ctx, accessOrg, connection.MailboxID); err != nil {
		t.Fatalf("a refused purge touched the mailbox: %v", err)
	}
	purge, err := f.service.PurgeExternalMailCopies(ctx, f.admin, connection.ID)
	if err != nil || !purge.Completed {
		t.Fatalf("admin purging = %+v, err %v", purge, err)
	}
	if _, err = f.repo.FindMailbox(ctx, accessOrg, connection.MailboxID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("the purged mailbox survived: %v", err)
	}
}

func (f *externalFixture) reloadConnection(t *testing.T, id uint) *domain.ExternalMailConnection {
	t.Helper()
	connection, err := f.service.externalMail.FindExternalMailConnection(context.Background(), accessOrg, id)
	if err != nil {
		t.Fatalf("FindExternalMailConnection: %v", err)
	}
	return connection
}

// TestSharedConnectionListingAndDirectory: the connections route lists one's own accounts and the
// shared ones one manages; the manager directory shows a team account in full, a member's masked.
func TestSharedConnectionListingAndDirectory(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	connection := f.mustBindShared(t, "support", teamMembers())
	f.bind(t, f.c, domain.ExternalMailProviderOutlook, "carol")

	listed := func(viewer domain.OrganizationContext) []uint {
		t.Helper()
		connections, err := f.service.ListExternalMailConnections(ctx, viewer)
		if err != nil {
			t.Fatalf("ListExternalMailConnections(%d): %v", viewer.UserID, err)
		}
		ids := []uint{}
		for _, item := range connections {
			ids = append(ids, item.ID)
		}
		return ids
	}
	contains := func(ids []uint, id uint) bool {
		for _, candidate := range ids {
			if candidate == id {
				return true
			}
		}
		return false
	}
	if ids := listed(f.c); len(ids) != 2 || !contains(ids, connection.ID) {
		t.Fatalf("manage member lists %v; want carol's own and the team's", ids)
	}
	if ids := listed(f.d); contains(ids, connection.ID) {
		t.Fatalf("view member lists the team connection: %v", ids)
	}
	if ids := listed(f.b); len(ids) != 1 || contains(ids, connection.ID) {
		t.Fatalf("B lists %v; want only their own", ids)
	}
	if ids := listed(f.admin); len(ids) != 1 || ids[0] != connection.ID {
		t.Fatalf("admin lists %v; want the team connection only", ids)
	}
	status, body := f.call(t, f.c, http.MethodGet, "/external-mail-connections")
	if items, _ := body["data"].([]any); status != http.StatusOK || len(items) != 2 || items[0].(map[string]any)["shared"] != false ||
		items[1].(map[string]any)["shared"] != true {
		t.Fatalf("HTTP listing = %d %v", status, body)
	}

	status, body = f.call(t, f.admin, http.MethodGet, "/external-mail-connection-directory")
	items, _ := body["data"].([]any)
	if status != http.StatusOK || len(items) != 3 {
		t.Fatalf("directory = %d %v", status, body)
	}
	entries := map[string]map[string]any{}
	for _, item := range items {
		entry := item.(map[string]any)
		entries[entry["masked_account_address"].(string)] = entry
	}
	team := entries["su***@external.invalid"]
	if team == nil || team["shared"] != true || team["account_address"] != "support@external.invalid" ||
		team["mailbox_id"] != float64(connection.MailboxID) || team["owner_user_id"] != nil || team["owner_display_name"] != nil {
		t.Fatalf("team entry = %v", team)
	}
	personal := entries["ca***@external.invalid"]
	if personal == nil || personal["shared"] != false || personal["account_address"] != nil || personal["mailbox_id"] != nil {
		t.Fatalf("personal entry = %v", personal)
	}
	if strings.Contains(fmt.Sprint(body), "carol@external.invalid") {
		t.Fatalf("the directory leaks a member's address: %v", body)
	}
}

// TestSharedConnectedMailboxSwitches: the webhook and AI triage switches follow the shared mailbox
// rules: member managers and owners or admins, never view members or outsiders.
func TestSharedConnectedMailboxSwitches(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.service.settings.Triage = config.AITriageConfig{Provider: config.AITriageProviderFake, DailyCap: 100}
	f.switchTriage(t, true)
	ctx := context.Background()
	connection := f.mustBindShared(t, "support", teamMembers())

	if mailbox, err := f.service.SetMailboxWebhooks(ctx, f.c, connection.MailboxID, true); err != nil || !mailbox.WebhooksEnabled {
		t.Fatalf("manage member enabling webhooks = %+v, err %v", mailbox, err)
	}
	if _, err := f.service.SetMailboxWebhooks(ctx, f.d, connection.MailboxID, false); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("view member switching webhooks = %v, want permission denied", err)
	}
	if _, err := f.service.SetMailboxWebhooks(ctx, f.b, connection.MailboxID, false); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("outsider switching webhooks = %v, want not found", err)
	}
	if mailbox, err := f.service.SetMailboxWebhooks(ctx, f.admin, connection.MailboxID, false); err != nil || mailbox.WebhooksEnabled {
		t.Fatalf("admin disabling webhooks = %+v, err %v", mailbox, err)
	}

	if mailbox, err := f.service.SetMailboxTriage(ctx, f.c, connection.MailboxID, true); err != nil || !mailbox.AITriageEnabled {
		t.Fatalf("manage member enabling triage = %+v, err %v", mailbox, err)
	}
	if _, err := f.service.SetMailboxTriage(ctx, f.d, connection.MailboxID, false); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("view member switching triage = %v, want permission denied", err)
	}
	f.source.Deliver("fake-support", externalMessage("s-1", "Ticket", time.Time{}))
	f.sync(t)
	stored := f.count(t, `SELECT COUNT(*) FROM mail_triage t JOIN mail_messages m ON m.id = t.message_id WHERE m.mailbox_id = ?`, connection.MailboxID)
	if stored != 1 {
		t.Fatalf("queued triage rows = %d, want 1", stored)
	}
}

// TestSharedConnectedMailboxAgentGrants: grants follow the shared mailbox rules (a manage member
// issues, a view member or a non-member admin does not), and draft is refused for the read-only
// origin.
func TestSharedConnectedMailboxAgentGrants(t *testing.T) {
	f := newAgentFixture(t)
	connection := f.mustBindShared(t, "support", teamMembers())
	mailbox, err := f.repo.FindMailbox(context.Background(), accessOrg, connection.MailboxID)
	if err != nil {
		t.Fatalf("FindMailbox: %v", err)
	}
	_, err = f.grant(f.c, mailbox, "c", func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "draft"} })
	expectError(t, "draft on a shared connected mailbox", err, ErrMailAgentGrantInvalid)
	_, err = f.grant(f.d, mailbox, "d", nil)
	expectError(t, "view member issuing", err, domain.ErrPermissionDenied)
	_, err = f.grant(f.admin, mailbox, "admin", nil)
	expectError(t, "non-member admin issuing", err, domain.ErrPermissionDenied)
	_, err = f.grant(f.b, mailbox, "b", nil)
	expectError(t, "outsider issuing", err, ErrMailboxNotFound)
	grant := f.mustGrant(t, f.c, mailbox, "c", nil)
	if grant.CanDraft {
		t.Fatal("a shared connected mailbox grant drafts")
	}
}

// TestDeletingASharedConnectedMailboxRevokesItsConnection: only owners and admins delete it, and the
// delete revokes the connection and hands its grant back in the same transaction.
func TestDeletingASharedConnectedMailboxRevokesItsConnection(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	connection := f.mustBindShared(t, "support", teamMembers())
	if err := f.service.DeleteMailbox(ctx, f.c, connection.MailboxID); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("manage member deleting = %v, want permission denied", err)
	}
	if err := f.service.DeleteMailbox(ctx, f.admin, connection.MailboxID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	stored := f.reloadConnection(t, connection.ID)
	if stored.Status != domain.ExternalMailStatusRevoked || stored.EncryptedRefreshToken != "" || !f.source.Revoked("fake-support") {
		t.Fatalf("connection after delete = %+v", stored)
	}
	// Nobody manages the connection of a deleted mailbox.
	if ids, _ := f.service.ListExternalMailConnections(ctx, f.admin); len(ids) != 0 {
		t.Fatalf("admin still lists %d connections", len(ids))
	}
	if _, err := f.service.StartExternalMailAuthorization(ctx, f.admin, ExternalMailAuthorizationInput{ConnectionID: &connection.ID}); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("reconnecting a deleted mailbox's connection = %v, want not found", err)
	}
}

// TestSharedConnectionLimitHoldsUnderConcurrency: two managers completing shared bindings at once
// each hold only their own member lock, so the organization lock alone keeps them from both passing
// the organization's limit; a member's own binding at the same time is not held up by it.
func TestSharedConnectionLimitHoldsUnderConcurrency(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.service.settings.ExternalMail.MaxSharedConnections = 2
	ctx := context.Background()
	f.mustBindShared(t, "first", teamMembers())
	owner := viewerOf(accessOrg, accessB, domain.OrganizationRoleOwner)
	viewers := []domain.OrganizationContext{f.admin, owner}
	codes := []string{"team-a", "team-b"}
	states := make([]string, len(viewers))
	for i, viewer := range viewers {
		started, err := f.service.StartExternalMailAuthorization(ctx, viewer, ExternalMailAuthorizationInput{
			Provider: domain.ExternalMailProviderGmail, Sharing: teamMembers(),
		})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		parsed, _ := url.Parse(started.AuthorizationURL)
		states[i] = parsed.Query().Get("state")
	}
	personalState := f.signIn(t, f.d, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail})
	errs := make([]error, len(viewers))
	var personalErr error
	var wg sync.WaitGroup
	for i := range viewers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = f.service.CompleteExternalMailAuthorization(ctx, viewers[i], states[i], codes[i])
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, personalErr = f.service.CompleteExternalMailAuthorization(ctx, f.d, personalState, "dan")
	}()
	wg.Wait()
	succeeded, limited := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrExternalMailConnectionLimit):
			limited++
		default:
			t.Fatalf("completion: %v", err)
		}
	}
	if succeeded != 1 || limited != 1 || personalErr != nil {
		t.Fatalf("racing completions: %d succeeded, %d limited, personal %v; want 1, 1, nil", succeeded, limited, personalErr)
	}
	if live, _ := f.service.externalMail.CountLiveSharedExternalMailConnections(ctx, accessOrg); live != 2 {
		t.Fatalf("live shared connections = %d, want 2", live)
	}
}

// TestSharedSignInFailsOnAnInstanceWithoutSharing: a shared sign-in seals its verifier with a marker,
// so an instance that ignores the sharing column (an older API during a rollout, simulated here by
// clearing it) refuses the completion before exchanging the code, instead of binding the team
// account as the caller's own.
func TestSharedSignInFailsOnAnInstanceWithoutSharing(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	started, err := f.service.StartExternalMailAuthorization(ctx, f.admin, ExternalMailAuthorizationInput{
		Provider: domain.ExternalMailProviderGmail, Sharing: teamMembers(),
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var sealed string
	db := f.repo.(*repository).db
	if err = db.Raw(`SELECT sealed_verifier FROM external_mail_authorizations`).Scan(&sealed).Error; err != nil {
		t.Fatalf("read sign-in: %v", err)
	}
	if !strings.HasPrefix(sealed, sharedVerifierMarker) {
		t.Fatalf("a shared sign-in is not marked: %q", sealed)
	}
	if _, err = f.service.encryptor.DecryptString(sealed); err == nil {
		t.Fatal("a marked verifier opens without knowing the marker")
	}
	if err = db.Exec(`UPDATE external_mail_authorizations SET sharing = NULL`).Error; err != nil {
		t.Fatalf("clear sharing: %v", err)
	}
	parsed, _ := url.Parse(started.AuthorizationURL)
	if _, _, err = f.service.CompleteExternalMailAuthorization(ctx, f.admin, parsed.Query().Get("state"), "support"); !errors.Is(err, ErrExternalMailAuthorizationNotFound) {
		t.Fatalf("completion without the sharing column = %v, want not found", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM external_mail_connections WHERE lower(account_address) = 'support@external.invalid'`); n != 0 {
		t.Fatalf("the team account was bound %d times", n)
	}
}

// TestDisplayNamesCountCharacters: the 120-character display-name limit counts characters, as the
// column and the contract do, for every mailbox creation; a long account address used as the
// default name is cut to fit.
func TestDisplayNamesCountCharacters(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	full := strings.Repeat("客", 120)
	mailbox, err := f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{
		LocalPart: "wide", DisplayName: full, Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessC, Permission: domain.MailboxPermissionView}},
	})
	if err != nil || mailbox.DisplayName != full {
		t.Fatalf("120 multi-byte characters = %+v, err %v", mailbox, err)
	}
	if _, err = f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{
		LocalPart: "wider", DisplayName: full + "客", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessC, Permission: domain.MailboxPermissionView}},
	}); !errors.Is(err, ErrMailboxAddressInvalid) {
		t.Fatalf("121 characters = %v, want address invalid", err)
	}
	named := teamMembers()
	named.DisplayName = full
	connection := f.mustBindShared(t, "support", named)
	stored, _ := f.repo.FindMailbox(ctx, accessOrg, connection.MailboxID)
	if stored.DisplayName != full {
		t.Fatalf("shared display name = %q", stored.DisplayName)
	}
	unnamed := teamMembers()
	unnamed.DisplayName = ""
	long := strings.Repeat("l", 150)
	connection = f.mustBindShared(t, long, unnamed)
	stored, _ = f.repo.FindMailbox(ctx, accessOrg, connection.MailboxID)
	if stored.Address != long+"@external.invalid" || len([]rune(stored.DisplayName)) != maxMailboxDisplayNameLength {
		t.Fatalf("long address mailbox = %q named %q", stored.Address, stored.DisplayName)
	}
}

// TestSharedDisconnectReachesEveryManager: unbinding a shared connection, by a manage member or an
// admin through the directory, files the audit record under every other owner and admin too.
func TestSharedDisconnectReachesEveryManager(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	f.memberships.managers = []uint{accessAdmin, accessB}
	var changes []auditstarter.Change
	f.service.recordAudit = func(_ context.Context, change auditstarter.Change) { changes = append(changes, change) }

	byMember := f.mustBindShared(t, "team-one", teamMembers())
	if err := f.service.RevokeExternalMailConnection(ctx, f.c, byMember.ID); err != nil {
		t.Fatalf("manage member unbinding: %v", err)
	}
	byAdmin := f.mustBindShared(t, "team-two", teamMembers())
	if err := f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, byAdmin.ID); err != nil {
		t.Fatalf("admin disconnecting: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("audit changes = %+v; want two shared disconnects", changes)
	}
	for index, want := range [][]uint{{accessAdmin, accessB}, {accessB}} {
		if changes[index].Metadata["reason"] != "shared_disconnect" || !slices.Equal(changes[index].AlsoVisibleTo, want) {
			t.Fatalf("change %d = %+v; want shared_disconnect visible to %v", index, changes[index], want)
		}
	}
}

// TestFakeDeliveryToSharedConnection: whoever manages a shared connection may deliver to its
// account; the message is stored once in the shared mailbox. A view member may not.
func TestFakeDeliveryToSharedConnection(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	shared := f.mustBindShared(t, "team", teamMembers())
	body := `{"from":"carol@sender.test","subject":"Ticket","text":"Help","message_id":"t-1@sender.test"}`

	for _, viewer := range []domain.OrganizationContext{f.c, f.admin} {
		if status, response := f.deliverFake(t, viewer, shared.ID, body); status != http.StatusCreated {
			t.Fatalf("delivery by user %d = %d %v", viewer.UserID, status, response)
		}
	}
	messages, _, err := f.service.ListMessages(ctx, f.c, shared.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 50})
	if err != nil || len(messages) != 1 || messages[0].Source != domain.MailSourceGmail || messages[0].Subject != "Ticket" {
		t.Fatalf("shared mailbox messages = %+v, err %v; want the ticket once from gmail", messages, err)
	}
	if status, response := f.deliverFake(t, f.d, shared.ID, body); status != http.StatusNotFound ||
		response["error_code"] != domain.CodeExternalMailConnectionNotFound {
		t.Fatalf("view member delivery = %d %v", status, response)
	}
}
