package mailbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/database/migrations"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/migration"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// Organization 10 of the mail client scenarios: the owner, Alice, and Bob, with the membership ids
// fakeMemberships derives (organization*1000 + user).
const (
	clientOrg   = 10
	clientOwner = 901
	clientAlice = 902
	clientBob   = 903
)

// mailClientFixture is the mail client scenario on the real migrations: Alice and Bob own personal
// mailboxes; helpdesk@ is shared with Alice (view) and Bob (send); deals@ is Bob's alone; connected@
// is a connected mailbox shared with Alice. Alice and Bob each hold an IMAP password.
type mailClientFixture struct {
	Service                 *service
	DB                      *gorm.DB
	Owner, Alice, Bob       domain.OrganizationContext
	AlicePersonal, BobBox   *domain.Mailbox
	Support, Sales          *domain.Mailbox
	ConnectedID             uint
	AliceSecret, BobSecret  string
	AlicePassword, BobPwdID uint
}

// newMailClientFixture migrates a disposable schema and seeds the scenario.
func newMailClientFixture(t *testing.T) *mailClientFixture {
	t.Helper()
	db := testplatform.OpenPostgres(t, &gorm.Config{TranslateError: true})
	migrator := migration.NewMigrator(migration.NewDatabaseRepository(db, "migrations"), db, nil)
	migrator.RegisterMany(migrations.All())
	if _, err := migrator.Run(migration.MigratorOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	m := func(user uint) uint { return membershipIDOf(clientOrg, user) }
	for _, statement := range []string{
		`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES
			(901, 'owner', 'owner@example.com', 'x', now(), now()),
			(902, 'alice', 'Alice@Example.com', 'x', now(), now()),
			(903, 'bob', 'bob@example.com', 'x', now(), now())`,
		`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`,
		fmt.Sprintf(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES
			(%d, 10, 901, 'owner', now(), now()), (%d, 10, 902, 'member', now(), now()), (%d, 10, 903, 'member', now(), now())`,
			m(clientOwner), m(clientAlice), m(clientBob)),
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	repo := NewRepository(db)
	settings := accessSettings()
	settings.AppPasswordPepper = testAppPasswordPepper
	svc := NewService(repo, newAccessMemberships(), repo, repo, repo, repo, nil, nil, nil, &fakeTransport{},
		&fakeNotifier{}, settings, nil, ratelimit.NewMemoryStores(), nil, nil)
	svc.reverifier = &fakeReverifier{}
	viewer := func(user uint, role domain.OrganizationRole) domain.OrganizationContext {
		return domain.OrganizationContext{
			OrganizationID: clientOrg, OrganizationName: "Acme", OrganizationSlug: "acme",
			MembershipID: m(user), UserID: user, Role: role, Features: domain.DefaultMemberFeatures(),
		}
	}
	f := &mailClientFixture{
		Service: svc, DB: db,
		Owner: viewer(clientOwner, domain.OrganizationRoleOwner),
		Alice: viewer(clientAlice, domain.OrganizationRoleMember),
		Bob:   viewer(clientBob, domain.OrganizationRoleMember),
	}
	ctx := context.Background()
	create := func(input CreateMailboxInput) *domain.Mailbox {
		mailbox, err := svc.CreateMailbox(ctx, f.Owner, input)
		if err != nil {
			t.Fatalf("CreateMailbox(%s): %v", input.LocalPart, err)
		}
		return mailbox
	}
	f.AlicePersonal = create(CreateMailboxInput{LocalPart: "alice", Type: domain.MailboxTypePersonal, OwnerUserID: clientAlice})
	f.BobBox = create(CreateMailboxInput{LocalPart: "bob", Type: domain.MailboxTypePersonal, OwnerUserID: clientBob})
	f.Support = create(CreateMailboxInput{LocalPart: "helpdesk", Type: domain.MailboxTypeShared, Members: []MailboxMemberInput{
		{UserID: clientAlice, Permission: domain.MailboxPermissionView},
		{UserID: clientBob, Permission: domain.MailboxPermissionSend},
	}})
	f.Sales = create(CreateMailboxInput{LocalPart: "deals", Type: domain.MailboxTypeShared, Members: []MailboxMemberInput{
		{UserID: clientBob, Permission: domain.MailboxPermissionManage},
	}})
	if err := db.Raw(`INSERT INTO mailboxes (organization_id, created_by, address, display_name, type, origin, created_at, updated_at)
		VALUES (10, 901, 'connected@outside.test', 'Outside', 'shared', 'connected', now(), now()) RETURNING id`).Scan(&f.ConnectedID).Error; err != nil {
		t.Fatalf("seed connected mailbox: %v", err)
	}
	if err := db.Exec(`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at)
		VALUES (?, ?, ?, 'view', 901, now(), now())`, f.ConnectedID, m(clientAlice), clientAlice).Error; err != nil {
		t.Fatalf("seed connected member: %v", err)
	}
	f.AlicePassword, f.AliceSecret = f.newPassword(t, f.Alice)
	f.BobPwdID, f.BobSecret = f.newPassword(t, f.Bob)
	return f
}

func (f *mailClientFixture) newPassword(t *testing.T, viewer domain.OrganizationContext) (uint, string) {
	t.Helper()
	created, secret, err := f.Service.CreateAppPassword(context.Background(), viewer, CreateAppPasswordInput{
		Name: "Phone", Scopes: []domain.MailClientProtocol{domain.MailClientProtocolIMAP}, CurrentPassword: "right",
	})
	if err != nil {
		t.Fatalf("CreateAppPassword: %v", err)
	}
	return created.ID, secret
}

// Deliver stores raw as an inbound message of mailbox, with its source, as the mail edge would.
func (f *mailClientFixture) Deliver(t *testing.T, mailbox *domain.Mailbox, raw []byte) *domain.MailMessage {
	t.Helper()
	message, _, err := f.Service.ReceiveInbound(context.Background(), InboundDelivery{
		Recipient: mailbox.Address, Signature: signedHeader(time.Now(), mailbox.Address, raw), Raw: raw,
	})
	if err != nil {
		t.Fatalf("ReceiveInbound: %v", err)
	}
	return message
}

// StoreWithoutSource stores an inbound message the way mail was kept before sources existed.
func (f *mailClientFixture) StoreWithoutSource(t *testing.T, mailbox *domain.Mailbox, subject string) *domain.MailMessage {
	t.Helper()
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "carol@sender.test", To: []string{mailbox.Address}, Subject: subject, TextBody: "older body",
		DedupeKey: subject,
	}
	if _, err := f.Service.repo.CreateInboundMessage(context.Background(), message); err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	return message
}

// Login signs in as username with secret, failing the test on refusal.
func (f *mailClientFixture) Login(t *testing.T, username, secret string) *domain.MailClientPrincipal {
	t.Helper()
	principal, err := f.Service.AuthenticateClient(context.Background(), username, secret, domain.MailClientProtocolIMAP, "203.0.113.9")
	if err != nil {
		t.Fatalf("AuthenticateClient(%s): %v", username, err)
	}
	return principal
}

// Exec runs one statement of test setup.
func (f *mailClientFixture) Exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	if err := f.DB.Exec(statement, args...).Error; err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func clientMail(subject, body string) []byte {
	return []byte("From: Dana <dana@sender.test>\r\nTo: someone@mail.example\r\nSubject: " + subject +
		"\r\nMessage-ID: <" + subject + "@sender.test>\r\nDate: Mon, 23 Nov 2026 08:00:00 +0000\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n")
}

func TestAuthenticateClientUniformFailure(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()

	for username, root := range map[string]uint{
		"alice@mail.example": f.AlicePersonal.ID, " ALICE@mail.example ": f.AlicePersonal.ID,
		"alice@example.com": f.AlicePersonal.ID, "helpdesk@mail.example": f.Support.ID,
	} {
		principal := f.Login(t, username, f.AliceSecret)
		if principal.PasswordID != f.AlicePassword || principal.RootMailboxID != root || principal.Viewer.MembershipID != f.Alice.MembershipID ||
			!principal.Viewer.IsValid() {
			t.Fatalf("%q signed in as %+v; want root %d", username, principal, root)
		}
	}
	if _, err := f.Service.AuthenticateClient(ctx, "alice@mail.example", "  "+f.AliceSecret+" ", domain.MailClientProtocolIMAP, "203.0.113.9"); err != nil {
		t.Fatalf("spaces around the secret: %v", err)
	}
	var lastUsed struct {
		LastUsedIP       *string
		LastUsedProtocol *string
		LastUsedAt       *time.Time
	}
	f.DB.Raw(`SELECT last_used_ip, last_used_protocol, last_used_at FROM mail_app_passwords WHERE id = ?`, f.AlicePassword).Scan(&lastUsed)
	if lastUsed.LastUsedAt == nil || lastUsed.LastUsedIP == nil || *lastUsed.LastUsedIP != "203.0.113.0" ||
		lastUsed.LastUsedProtocol == nil || *lastUsed.LastUsedProtocol != "imap" {
		t.Fatalf("last use = %+v", lastUsed)
	}

	refuse := func(what, username, secret string) {
		t.Helper()
		principal, err := f.Service.AuthenticateClient(ctx, username, secret, domain.MailClientProtocolIMAP, "203.0.113.9")
		if err != domain.ErrMailClientAuthFailed || principal != nil { //nolint:errorlint // the very same value
			t.Fatalf("%s = %+v, %v; want exactly ErrMailClientAuthFailed", what, principal, err)
		}
	}
	refuse("wrong password", "alice@mail.example", "abcd efgh ijkl mnop")
	refuse("empty password", "alice@mail.example", "")
	refuse("another member's address", "bob@mail.example", f.AliceSecret)
	refuse("a shared mailbox without view", "deals@mail.example", f.AliceSecret)
	refuse("a connected mailbox", "connected@outside.test", f.AliceSecret)
	refuse("an unknown address", "nobody@mail.example", f.AliceSecret)
	if _, err := f.Service.AuthenticateClient(ctx, "alice@mail.example", f.AliceSecret, domain.MailClientProtocolSMTP, "203.0.113.9"); err != domain.ErrMailClientAuthFailed { //nolint:errorlint // same value
		t.Fatalf("an imap-only password over SMTP = %v", err)
	}

	f.Exec(t, `UPDATE organization_memberships SET can_use_mail_clients = false WHERE id = ?`, f.Alice.MembershipID)
	refuse("feature withdrawn", "alice@mail.example", f.AliceSecret)
	f.Exec(t, `UPDATE organization_memberships SET can_use_mail_clients = true WHERE id = ?`, f.Alice.MembershipID)
	f.Login(t, "alice@mail.example", f.AliceSecret)

	if _, err := f.Service.UpdateMailClientSettings(ctx, f.Owner, false); err != nil {
		t.Fatal(err)
	}
	refuse("organization switch off", "alice@mail.example", f.AliceSecret)
	if _, err := f.Service.UpdateMailClientSettings(ctx, f.Owner, true); err != nil {
		t.Fatal(err)
	}

	f.Exec(t, `UPDATE mail_app_passwords SET scope_imap = false, scope_smtp = true WHERE id = ?`, f.BobPwdID)
	refuse("smtp-only password", "bob@mail.example", f.BobSecret)

	if err := f.Service.RevokeAppPassword(ctx, f.Alice, f.AlicePassword); err != nil {
		t.Fatal(err)
	}
	refuse("revoked", "alice@mail.example", f.AliceSecret)
}

func folderNamed(folders []domain.MailClientFolder, mailboxID uint, key string) *domain.MailClientFolder {
	for i := range folders {
		if folders[i].MailboxID == mailboxID && folders[i].Key == key {
			return &folders[i]
		}
	}
	return nil
}

func TestClientFoldersFollowMailboxAccess(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	folder, err := f.Service.CreateFolder(ctx, f.Alice, f.AlicePersonal.ID, "Invoices/2026")
	if err != nil {
		t.Fatal(err)
	}
	principal := f.Login(t, "alice@mail.example", f.AliceSecret)
	folders, err := f.Service.ClientFolders(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"inbox", "sent", "archive", "trash"} {
		if got := folderNamed(folders, f.AlicePersonal.ID, key); got == nil || got.Shared {
			t.Fatalf("root %s = %+v in %+v", key, got, folders)
		}
		if got := folderNamed(folders, f.Support.ID, key); got == nil || !got.Shared || got.MailboxAddress != "helpdesk@mail.example" {
			t.Fatalf("shared %s = %+v", key, got)
		}
	}
	if got := folderNamed(folders, f.AlicePersonal.ID, fmt.Sprintf("folder:%d", folder.ID)); got == nil || got.Name != "Invoices/2026" {
		t.Fatalf("user folder = %+v", got)
	}
	for _, folder := range folders {
		if folder.MailboxID == f.Sales.ID || folder.MailboxID == f.ConnectedID || folder.MailboxID == f.BobBox.ID {
			t.Fatalf("Alice sees %+v", folder)
		}
	}

	asSupport := f.Login(t, "helpdesk@mail.example", f.AliceSecret)
	folders, err = f.Service.ClientFolders(ctx, asSupport)
	if err != nil {
		t.Fatal(err)
	}
	if got := folderNamed(folders, f.Support.ID, "inbox"); got == nil || got.Shared {
		t.Fatalf("support@ at the root = %+v", got)
	}
	if got := folderNamed(folders, f.AlicePersonal.ID, "inbox"); got != nil {
		t.Fatalf("signed in as support@, the personal mailbox shows: %+v", got)
	}

	// Losing view hides the mailbox and refuses its folders.
	if err := f.Service.RemoveMailboxMember(ctx, f.Owner, f.Support.ID, clientAlice); err != nil {
		t.Fatal(err)
	}
	folders, _ = f.Service.ClientFolders(ctx, principal)
	if folderNamed(folders, f.Support.ID, "inbox") != nil {
		t.Fatal("a removed member still lists the shared mailbox")
	}
	if visible, err := f.Service.ClientMailboxVisible(ctx, principal, f.Support.ID); err != nil || visible {
		t.Fatalf("ClientMailboxVisible after removal = %v, %v", visible, err)
	}
	if _, err := f.Service.ClientFolderState(ctx, principal, f.Support.ID, "inbox"); !errors.Is(err, domain.ErrMailClientFolderNotFound) {
		t.Fatalf("state of a hidden mailbox = %v", err)
	}
	if _, err := f.Service.ClientFolderState(ctx, principal, f.AlicePersonal.ID, "folder:999999"); !errors.Is(err, domain.ErrMailClientFolderNotFound) {
		t.Fatalf("state of a missing folder = %v", err)
	}
}

func TestClientFolderStateMatchesWebCounts(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	principal := f.Login(t, "alice@mail.example", f.AliceSecret)

	empty, err := f.Service.ClientFolderState(ctx, principal, f.AlicePersonal.ID, "archive")
	if err != nil || len(empty.UIDs) != 0 || empty.UIDNext != 1 || empty.UIDValidity == 0 {
		t.Fatalf("empty archive = %+v, %v", empty, err)
	}
	first := f.Deliver(t, f.AlicePersonal, clientMail("one", "first body"))
	f.Deliver(t, f.AlicePersonal, clientMail("two", "second body"))
	f.StoreWithoutSource(t, f.AlicePersonal, "three")
	if _, err = f.Service.SetMessageRead(ctx, f.Alice, f.AlicePersonal.ID, first.ID, true); err != nil {
		t.Fatal(err)
	}
	state, err := f.Service.ClientFolderState(ctx, principal, f.AlicePersonal.ID, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	counts, err := f.Service.MessageCounts(ctx, f.Alice, f.AlicePersonal.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, total, err := f.Service.ListMessages(ctx, f.Alice, f.AlicePersonal.ID, domain.MailMessageFilter{
		Direction: domain.MailDirectionInbound, Placement: domain.MailPlacementDefault,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.UIDs, []uint32{1, 2, 3}) || state.UIDNext != 4 || int64(len(state.UIDs)) != total || state.Unseen != counts.DefaultInboundUnread {
		t.Fatalf("state = %+v; web total %d unread %d", state, total, counts.DefaultInboundUnread)
	}
	again, _ := f.Service.ClientFolderState(ctx, principal, f.AlicePersonal.ID, "inbox")
	archive, _ := f.Service.ClientFolderState(ctx, principal, f.AlicePersonal.ID, "archive")
	if again.UIDValidity != state.UIDValidity || archive.UIDValidity != empty.UIDValidity {
		t.Fatal("UIDVALIDITY changed between reads")
	}

	messages, err := f.Service.ClientMessages(ctx, principal, f.AlicePersonal.ID, "inbox", nil)
	if err != nil || len(messages) != 3 || !messages[0].Seen || messages[1].Seen || messages[0].MessageID != first.ID {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
	some, err := f.Service.ClientMessages(ctx, principal, f.AlicePersonal.ID, "inbox", []uint32{2, 9})
	if err != nil || len(some) != 1 || some[0].UID != 2 {
		t.Fatalf("messages 2 and 9 = %+v, %v", some, err)
	}

	raw, err := f.Service.ClientMessageSource(ctx, principal, f.AlicePersonal.ID, "inbox", 1)
	if err != nil || !bytes.Equal(raw, clientMail("one", "first body")) {
		t.Fatalf("stored source = %q, %v", raw, err)
	}
	composed, err := f.Service.ClientMessageSource(ctx, principal, f.AlicePersonal.ID, "inbox", 3)
	if err != nil || !bytes.Contains(composed, []byte("X-Zmail-Reconstructed: 1")) {
		t.Fatalf("composed source = %q, %v", composed, err)
	}
	if _, err := f.Service.ClientMessageSource(ctx, principal, f.AlicePersonal.ID, "inbox", 77); !errors.Is(err, domain.ErrMailClientMessageNotFound) {
		t.Fatalf("a missing uid = %v", err)
	}
}

func TestSetClientFlagIsPerMember(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	message := f.Deliver(t, f.Support, clientMail("shared", "hello"))
	alice := f.Login(t, "alice@mail.example", f.AliceSecret)
	bob := f.Login(t, "bob@mail.example", f.BobSecret)

	if err := f.Service.SetClientFlag(ctx, alice, f.Support.ID, "inbox", []uint32{1}, domain.MailClientFlagSeen, true); err != nil {
		t.Fatal(err)
	}
	if err := f.Service.SetClientFlag(ctx, alice, f.Support.ID, "inbox", []uint32{1}, domain.MailClientFlagFlagged, true); err != nil {
		t.Fatal(err)
	}
	aliceState, _ := f.Service.ClientFolderState(ctx, alice, f.Support.ID, "inbox")
	bobState, _ := f.Service.ClientFolderState(ctx, bob, f.Support.ID, "inbox")
	if aliceState.Unseen != 0 || bobState.Unseen != 1 {
		t.Fatalf("unseen: Alice %d, Bob %d", aliceState.Unseen, bobState.Unseen)
	}
	web, err := f.Service.GetMessage(ctx, f.Alice, f.Support.ID, message.ID)
	if err != nil || !web.Read || !web.Starred {
		t.Fatalf("Alice on the web = %+v, %v", web, err)
	}
	if err := f.Service.SetClientFlag(ctx, alice, f.Support.ID, "inbox", []uint32{1}, domain.MailClientFlagFlagged, false); err != nil {
		t.Fatal(err)
	}
	listed, _ := f.Service.ClientMessages(ctx, alice, f.Support.ID, "inbox", nil)
	if len(listed) != 1 || !listed[0].Seen || listed[0].Flagged {
		t.Fatalf("after unflagging = %+v", listed)
	}
	if err := f.Service.SetClientFlag(ctx, alice, f.Sales.ID, "inbox", []uint32{1}, domain.MailClientFlagSeen, true); !errors.Is(err, domain.ErrMailClientFolderNotFound) {
		t.Fatalf("flagging in a mailbox Alice cannot see = %v", err)
	}
}

func TestSearchClient(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	f.Deliver(t, f.AlicePersonal, clientMail("invoice-march", "please pay 100%"))
	read := f.Deliver(t, f.AlicePersonal, clientMail("lunch", "noodles today"))
	f.StoreWithoutSource(t, f.AlicePersonal, "older invoice")
	if _, err := f.Service.SetMessageRead(ctx, f.Alice, f.AlicePersonal.ID, read.ID, true); err != nil {
		t.Fatal(err)
	}
	principal := f.Login(t, "alice@mail.example", f.AliceSecret)
	search := func(criteria domain.MailClientSearch) []uint32 {
		t.Helper()
		uids, err := f.Service.SearchClient(ctx, principal, f.AlicePersonal.ID, "inbox", criteria)
		if err != nil {
			t.Fatalf("SearchClient(%+v): %v", criteria, err)
		}
		return uids
	}
	yes, no := true, false
	cases := []struct {
		name     string
		criteria domain.MailClientSearch
		want     []uint32
	}{
		{"all", domain.MailClientSearch{}, []uint32{1, 2, 3}},
		{"subject", domain.MailClientSearch{Subject: []string{"INVOICE"}}, []uint32{1, 3}},
		{"literal percent", domain.MailClientSearch{Body: []string{"100%"}}, []uint32{1}},
		{"from", domain.MailClientSearch{From: []string{"carol@"}}, []uint32{3}},
		{"seen", domain.MailClientSearch{Seen: &yes}, []uint32{2}},
		{"unseen", domain.MailClientSearch{Seen: &no}, []uint32{1, 3}},
		{"uid range", domain.MailClientSearch{UIDSets: [][]domain.MailClientUIDRange{{{Start: 2, Stop: 3}}}}, []uint32{2, 3}},
		{"not", domain.MailClientSearch{Not: []domain.MailClientSearch{{Subject: []string{"invoice"}}}}, []uint32{2}},
		{"or", domain.MailClientSearch{Or: [][2]domain.MailClientSearch{{{Subject: []string{"lunch"}}, {Text: []string{"older"}}}}}, []uint32{2, 3}},
		{"nothing", domain.MailClientSearch{MatchNothing: true}, nil},
	}
	for _, test := range cases {
		if got := search(test.criteria); !slices.Equal(got, test.want) {
			t.Fatalf("%s = %v; want %v", test.name, got, test.want)
		}
	}
}

func TestRevalidateClients(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	alice := f.Login(t, "alice@mail.example", f.AliceSecret)
	bob := f.Login(t, "bob@mail.example", f.BobSecret)
	check := func(wantAlice, wantBob bool) {
		t.Helper()
		valid, err := f.Service.RevalidateClients(ctx, []*domain.MailClientPrincipal{alice, bob})
		if err != nil || valid[alice.PasswordID] != wantAlice || valid[bob.PasswordID] != wantBob {
			t.Fatalf("RevalidateClients = %v, %v; want Alice %v, Bob %v", valid, err, wantAlice, wantBob)
		}
	}
	check(true, true)
	f.Exec(t, `UPDATE organization_memberships SET can_use_mail_clients = false WHERE id = ?`, f.Bob.MembershipID)
	check(true, false)
	f.Exec(t, `UPDATE organization_memberships SET can_use_mail_clients = true WHERE id = ?`, f.Bob.MembershipID)
	check(true, true)
	if _, err := f.Service.UpdateMailClientSettings(ctx, f.Owner, false); err != nil {
		t.Fatal(err)
	}
	check(false, false)
	if _, err := f.Service.UpdateMailClientSettings(ctx, f.Owner, true); err != nil {
		t.Fatal(err)
	}
	if err := f.Service.RevokeAppPassword(ctx, f.Alice, f.AlicePassword); err != nil {
		t.Fatal(err)
	}
	check(false, true)
	f.Exec(t, `DELETE FROM organization_memberships WHERE id = ?`, f.Bob.MembershipID)
	check(false, false)
}

func TestClientMessageSourceRefusesOversizedComposed(t *testing.T) {
	f := newMailClientFixture(t)
	principal := f.Login(t, "alice@mail.example", f.AliceSecret)
	message := f.StoreWithoutSource(t, f.AlicePersonal, "huge")
	f.Exec(t, `UPDATE mail_messages SET text_body = repeat('x', ?) WHERE id = ?`, domain.MailClientMaxSourceBytes+1, message.ID)
	if _, err := f.Service.ClientMessageSource(context.Background(), principal, f.AlicePersonal.ID, "inbox", 1); !errors.Is(err, domain.ErrMailClientMessageTooLarge) {
		t.Fatalf("an oversized composed source = %v; want ErrMailClientMessageTooLarge", err)
	}
}
