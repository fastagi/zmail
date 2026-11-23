package mailbox

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/imapgateway"
)

// The protocol tests drive the gateway with a real IMAP client over TLS, against the mail client
// scenario on PostgreSQL. They live here because the scenario is this package's test fixture.

// selfSignedTLS is a throwaway certificate for 127.0.0.1.
func selfSignedTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "imap.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

// syncBuffer collects the gateway's log lines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// imapGateway is a gateway serving the fixture on a loopback TLS listener.
type imapGateway struct {
	Server *imapgateway.Server
	Addr   string
	Logs   *syncBuffer
}

func startIMAPGateway(t *testing.T, f *mailClientFixture, cfg imapgateway.Config) *imapGateway {
	t.Helper()
	if cfg.FailureDelay == 0 {
		cfg.FailureDelay = -1
	}
	if cfg.Limits == nil {
		cfg.Limits = ratelimit.NewMemoryStores()
	}
	logs := &syncBuffer{}
	server := imapgateway.NewServer(f.Service, cfg, slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ln, err := tls.Listen("tcp", "127.0.0.1:0", selfSignedTLS(t))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = ln.Close()
		<-done
	})
	return &imapGateway{Server: server, Addr: ln.Addr().String(), Logs: logs}
}

func (g *imapGateway) dial(t *testing.T, options *imapclient.Options) *imapclient.Client {
	t.Helper()
	if options == nil {
		options = &imapclient.Options{}
	}
	options.TLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the test's own self-signed certificate
	client, err := imapclient.DialTLS(g.Addr, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func (g *imapGateway) login(t *testing.T, username, secret string) *imapclient.Client {
	t.Helper()
	client := g.dial(t, nil)
	if err := client.Login(username, secret).Wait(); err != nil {
		t.Fatalf("LOGIN %s: %v", username, err)
	}
	return client
}

// gatewayStatus is the response code of a refused command, or "" when it succeeded.
func gatewayStatus(t *testing.T, err error) (imap.StatusResponseType, imap.ResponseCode) {
	t.Helper()
	if err == nil {
		return imap.StatusResponseTypeOK, ""
	}
	var imapErr *imap.Error
	if !errors.As(err, &imapErr) {
		t.Fatalf("not an IMAP status response: %v", err)
	}
	return imapErr.Type, imapErr.Code
}

func fetchAll(t *testing.T, client *imapclient.Client, set imap.NumSet, options *imap.FetchOptions) []*imapclient.FetchMessageBuffer {
	t.Helper()
	messages, err := client.Fetch(set, options).Collect()
	if err != nil {
		t.Fatalf("FETCH: %v", err)
	}
	return messages
}

func TestIMAPLoginListSelectFetch(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	verification := clientMail("verify", "Your sign-in code is 482913.")
	f.Deliver(t, f.AlicePersonal, verification)
	f.StoreWithoutSource(t, f.AlicePersonal, "older")
	if _, err := f.Service.CreateFolder(ctx, f.Alice, f.AlicePersonal.ID, "Invoices/2026"); err != nil {
		t.Fatal(err)
	}
	gateway := startIMAPGateway(t, f, imapgateway.Config{})
	client := gateway.login(t, "alice@mail.example", f.AliceSecret)

	listed, err := client.List("", "*", nil).Collect()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string][]imap.MailboxAttr{}
	for _, data := range listed {
		names[data.Mailbox] = data.Attrs
	}
	for _, want := range []string{"INBOX", "Sent", "Archive", "Trash", "Invoices∕2026", "Shared",
		"Shared/helpdesk@mail.example", "Shared/helpdesk@mail.example/INBOX", "Shared/helpdesk@mail.example/Sent"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("LIST lacks %q: %v", want, names)
		}
	}
	for name := range names {
		if strings.Contains(name, "deals@") || strings.Contains(name, "connected@") || strings.Contains(name, "bob@") {
			t.Fatalf("LIST shows %q, which Alice cannot open", name)
		}
	}
	if !slices.Contains(names["Sent"], imap.MailboxAttrSent) || !slices.Contains(names["Shared"], imap.MailboxAttrNoSelect) {
		t.Fatalf("attributes: Sent %v, Shared %v", names["Sent"], names["Shared"])
	}

	selected, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if selected.NumMessages != 2 || selected.UIDNext != 3 || selected.UIDValidity == 0 {
		t.Fatalf("SELECT INBOX = %+v", selected)
	}
	messages := fetchAll(t, client, imap.SeqSetNum(1, 2), &imap.FetchOptions{
		UID: true, Flags: true, Envelope: true, RFC822Size: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}},
	})
	if len(messages) != 2 || messages[0].UID != 1 || messages[1].UID != 2 {
		t.Fatalf("FETCH = %+v", messages)
	}
	section := &imap.FetchItemBodySection{Peek: true}
	if body := messages[0].FindBodySection(section); !bytes.Equal(body, verification) {
		t.Fatalf("BODY[] of stored mail = %q", body)
	}
	if messages[0].Envelope == nil || messages[0].Envelope.Subject != "verify" || messages[0].RFC822Size != int64(len(verification)) {
		t.Fatalf("envelope %+v, size %d", messages[0].Envelope, messages[0].RFC822Size)
	}
	if body := messages[1].FindBodySection(section); !bytes.Contains(body, []byte("X-Zmail-Reconstructed: 1")) || !bytes.Contains(body, []byte("older body")) {
		t.Fatalf("BODY[] of older mail = %q", body)
	}
	if slices.Contains(messages[0].Flags, imap.FlagSeen) {
		t.Fatal("BODY.PEEK[] marked the message read")
	}

	again, err := client.Select("inbox", nil).Wait()
	if err != nil || again.UIDValidity != selected.UIDValidity || again.UIDNext != selected.UIDNext {
		t.Fatalf("second SELECT = %+v, %v", again, err)
	}
	shared, err := client.Select("Shared/helpdesk@mail.example/INBOX", nil).Wait()
	if err != nil || shared.NumMessages != 0 {
		t.Fatalf("SELECT shared inbox = %+v, %v", shared, err)
	}
	if _, err := client.Select("Shared", nil).Wait(); err == nil {
		t.Fatal("SELECT of a parent name succeeded")
	}
	if typ, code := gatewayStatus(t, func() error { _, err := client.Select("Shared/deals@mail.example/INBOX", nil).Wait(); return err }()); typ != imap.StatusResponseTypeNo || code != imap.ResponseCodeNonExistent {
		t.Fatalf("SELECT of a mailbox Alice cannot open = %s %s", typ, code)
	}
	if err := client.Logout().Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestIMAPAuthFailureIsGeneric(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	gateway := startIMAPGateway(t, f, imapgateway.Config{})
	client := gateway.dial(t, nil)
	var texts []string
	refuse := func(what, username, secret string) {
		t.Helper()
		err := client.Login(username, secret).Wait()
		typ, code := gatewayStatus(t, err)
		if typ != imap.StatusResponseTypeNo || code != imap.ResponseCodeAuthenticationFailed {
			t.Fatalf("%s = %v; want NO [AUTHENTICATIONFAILED]", what, err)
		}
		var imapErr *imap.Error
		errors.As(err, &imapErr)
		texts = append(texts, imapErr.Text)
	}
	refuse("wrong password", "alice@mail.example", "abcd efgh ijkl mnop")
	refuse("unknown user", "nobody@mail.example", f.AliceSecret)

	f.Exec(t, `UPDATE organization_memberships SET can_use_mail_clients = false WHERE id = ?`, f.Alice.MembershipID)
	refuse("feature not granted", "alice@mail.example", f.AliceSecret)
	f.Exec(t, `UPDATE organization_memberships SET can_use_mail_clients = true WHERE id = ?`, f.Alice.MembershipID)

	if _, err := f.Service.UpdateMailClientSettings(ctx, f.Owner, false); err != nil {
		t.Fatal(err)
	}
	refuse("organization switch off", "alice@mail.example", f.AliceSecret)
	if _, err := f.Service.UpdateMailClientSettings(ctx, f.Owner, true); err != nil {
		t.Fatal(err)
	}
	if err := f.Service.RevokeAppPassword(ctx, f.Alice, f.AlicePassword); err != nil {
		t.Fatal(err)
	}
	refuse("revoked", "alice@mail.example", f.AliceSecret)
	for _, text := range texts {
		if text != texts[0] {
			t.Fatalf("refusals differ: %q", texts)
		}
	}
}

func TestIMAPStoreSeenFlagged(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	first := f.Deliver(t, f.AlicePersonal, clientMail("first", "one"))
	second := f.Deliver(t, f.AlicePersonal, clientMail("second", "two"))
	gateway := startIMAPGateway(t, f, imapgateway.Config{})
	client := gateway.login(t, "alice@mail.example", f.AliceSecret)
	web := func(id uint) *domain.MailMessage {
		t.Helper()
		message, err := f.Service.GetMessage(ctx, f.Alice, f.AlicePersonal.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		return message
	}

	examined, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil || len(examined.PermanentFlags) != 0 {
		t.Fatalf("EXAMINE = %+v, %v", examined, err)
	}
	fetchAll(t, client, imap.SeqSetNum(1), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{{}}})
	if web(first.ID).Read {
		t.Fatal("BODY[] under EXAMINE marked the message read")
	}
	storeErr := client.Store(imap.SeqSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
	if typ, _ := gatewayStatus(t, storeErr); typ != imap.StatusResponseTypeNo {
		t.Fatalf("STORE under EXAMINE = %v", storeErr)
	}

	if _, err = client.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	stored, err := client.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged}}, nil).Collect()
	if err != nil || len(stored) != 1 || !slices.Contains(stored[0].Flags, imap.FlagSeen) || !slices.Contains(stored[0].Flags, imap.FlagFlagged) {
		t.Fatalf("STORE +FLAGS = %+v, %v", stored, err)
	}
	if message := web(first.ID); !message.Read || !message.Starred {
		t.Fatalf("on the web after STORE: read %v, starred %v", message.Read, message.Starred)
	}
	unstarred := false
	if _, err := f.Service.UpdateMessages(ctx, f.Alice, f.AlicePersonal.ID, []uint{first.ID}, MessageUpdate{Starred: &unstarred}); err != nil {
		t.Fatal(err)
	}
	flags := fetchAll(t, client, imap.SeqSetNum(1), &imap.FetchOptions{Flags: true})
	if len(flags) != 1 || slices.Contains(flags[0].Flags, imap.FlagFlagged) || !slices.Contains(flags[0].Flags, imap.FlagSeen) {
		t.Fatalf("FLAGS after unstarring on the web = %+v", flags)
	}
	if err := client.Store(imap.SeqSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsDel, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if web(first.ID).Read {
		t.Fatal("STORE -FLAGS \\Seen left the message read")
	}

	read := fetchAll(t, client, imap.SeqSetNum(2), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{{}}})
	if len(read) != 1 || !slices.Contains(read[0].Flags, imap.FlagSeen) || !web(second.ID).Read {
		t.Fatalf("BODY[] under SELECT: flags %+v, web read %v", read, web(second.ID).Read)
	}
}

func TestIMAPReadOnlyCommandsRefused(t *testing.T) {
	f := newMailClientFixture(t)
	f.Deliver(t, f.AlicePersonal, clientMail("keep", "stay"))
	gateway := startIMAPGateway(t, f, imapgateway.Config{})
	client := gateway.login(t, "alice@mail.example", f.AliceSecret)
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	want := func(what string, err error, wantCode imap.ResponseCode) {
		t.Helper()
		typ, code := gatewayStatus(t, err)
		if typ != imap.StatusResponseTypeNo || code != wantCode {
			t.Fatalf("%s = %v; want NO [%s]", what, err, wantCode)
		}
	}
	want("CREATE", client.Create("Projects", nil).Wait(), imap.ResponseCodeNoPerm)
	want("DELETE", client.Delete("Archive").Wait(), imap.ResponseCodeNoPerm)
	want("RENAME", client.Rename("Archive", "Old", nil).Wait(), imap.ResponseCodeNoPerm)
	_, copyErr := client.Copy(imap.SeqSetNum(1), "Archive").Wait()
	want("COPY", copyErr, imap.ResponseCodeCannot)
	_, moveErr := client.Move(imap.SeqSetNum(1), "Archive").Wait()
	want("MOVE", moveErr, imap.ResponseCodeCannot)
	raw := clientMail("appended", "from the app")
	appendCmd := client.Append("INBOX", int64(len(raw)), nil)
	if _, err := appendCmd.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := appendCmd.Close(); err != nil {
		t.Fatal(err)
	}
	_, appendErr := appendCmd.Wait()
	want("APPEND", appendErr, imap.ResponseCodeCannot)

	if err := client.Store(imap.SeqSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatalf("STORE \\Deleted: %v", err)
	}
	if err := client.Expunge().Close(); err != nil {
		t.Fatalf("EXPUNGE: %v", err)
	}
	status, err := client.Status("INBOX", &imap.StatusOptions{NumMessages: true}).Wait()
	if err != nil || status.NumMessages == nil || *status.NumMessages != 1 {
		t.Fatalf("INBOX after EXPUNGE = %+v, %v", status, err)
	}
}

func TestIMAPUIDStableAcrossWebMove(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	first := f.Deliver(t, f.AlicePersonal, clientMail("first", "one"))
	f.Deliver(t, f.AlicePersonal, clientMail("second", "two"))
	gateway := startIMAPGateway(t, f, imapgateway.Config{})
	var mu sync.Mutex
	var expunged []uint32
	client := gateway.dial(t, &imapclient.Options{UnilateralDataHandler: &imapclient.UnilateralDataHandler{
		Expunge: func(seq uint32) { mu.Lock(); expunged = append(expunged, seq); mu.Unlock() },
	}})
	if err := client.Login("alice@mail.example", f.AliceSecret).Wait(); err != nil {
		t.Fatal(err)
	}
	before, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatal(err)
	}

	archive := domain.MailPlacementArchive
	if _, err = f.Service.UpdateMessages(ctx, f.Alice, f.AlicePersonal.ID, []uint{first.ID}, MessageUpdate{Placement: &archive}); err != nil {
		t.Fatal(err)
	}
	if err = client.Noop().Wait(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotExpunged := slices.Clone(expunged)
	mu.Unlock()
	if !slices.Equal(gotExpunged, []uint32{1}) {
		t.Fatalf("expunges after archiving on the web = %v", gotExpunged)
	}
	left := fetchAll(t, client, imap.SeqSetNum(1), &imap.FetchOptions{UID: true})
	if len(left) != 1 || left[0].UID != 2 {
		t.Fatalf("INBOX after the move = %+v", left)
	}
	archived, err := client.Select("Archive", nil).Wait()
	if err != nil || archived.NumMessages != 1 {
		t.Fatalf("SELECT Archive = %+v, %v", archived, err)
	}
	inbox, err := client.Select("INBOX", nil).Wait()
	if err != nil || inbox.UIDValidity != before.UIDValidity || inbox.UIDNext != before.UIDNext || inbox.NumMessages != 1 {
		t.Fatalf("INBOX before %+v, after %+v, %v", before, inbox, err)
	}
	if found, err := client.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "second"}}}, nil).Wait(); err != nil ||
		!slices.Equal(found.AllUIDs(), []imap.UID{2}) {
		t.Fatalf("UID SEARCH = %+v, %v", found, err)
	}
}

func TestIMAPRateLimitLockout(t *testing.T) {
	f := newMailClientFixture(t)
	gateway := startIMAPGateway(t, f, imapgateway.Config{})
	client := gateway.dial(t, nil)
	login := func(username, secret string) error { return client.Login(username, secret).Wait() }

	for range 10 {
		if login("alice@mail.example", "abcd efgh ijkl mnop") == nil {
			t.Fatal("a wrong password signed in")
		}
	}
	if typ, code := gatewayStatus(t, login("alice@mail.example", f.AliceSecret)); typ != imap.StatusResponseTypeNo || code != imap.ResponseCodeAuthenticationFailed {
		t.Fatalf("Alice's right password while her account is locked = %s %s", typ, code)
	}
	// Bob signs in from the same address before it is locked.
	gateway.login(t, "bob@mail.example", f.BobSecret)
	for i := range 10 {
		if login("guess"+string(rune('a'+i))+"@mail.example", "abcd efgh ijkl mnop") == nil {
			t.Fatal("a guess signed in")
		}
	}
	other := gateway.dial(t, nil)
	if typ, code := gatewayStatus(t, other.Login("bob@mail.example", f.BobSecret).Wait()); typ != imap.StatusResponseTypeNo || code != imap.ResponseCodeAuthenticationFailed {
		t.Fatalf("Bob's right password while the address is locked = %s %s", typ, code)
	}
}

// runSweeper sweeps the gateway every interval until the test ends.
func (g *imapGateway) runSweeper(t *testing.T, interval time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.Server.RunSweeper(ctx, interval)
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func waitDisconnected(t *testing.T, client *imapclient.Client, within time.Duration) {
	t.Helper()
	select {
	case <-client.Closed():
	case <-time.After(within):
		t.Fatalf("the session is still open after %s", within)
	}
}

func TestIMAPRevokeDisconnectsWithinInterval(t *testing.T) {
	f := newMailClientFixture(t)
	gateway := startIMAPGateway(t, f, imapgateway.Config{})
	gateway.runSweeper(t, 200*time.Millisecond)
	alice := gateway.login(t, "alice@mail.example", f.AliceSecret)
	bob := gateway.login(t, "bob@mail.example", f.BobSecret)
	if _, err := alice.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := f.Service.RevokeAppPassword(context.Background(), f.Alice, f.AlicePassword); err != nil {
		t.Fatal(err)
	}
	waitDisconnected(t, alice, 2*time.Second)
	if err := bob.Noop().Wait(); err != nil {
		t.Fatalf("Bob's session ended with Alice's: %v", err)
	}
}

func TestIMAPAccessChangesDisconnect(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	gateway := startIMAPGateway(t, f, imapgateway.Config{})
	gateway.runSweeper(t, 200*time.Millisecond)
	alice := gateway.login(t, "alice@mail.example", f.AliceSecret)
	if _, err := alice.Select("Shared/helpdesk@mail.example/INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `DELETE FROM mailbox_members WHERE mailbox_id = ? AND membership_id = ?`, f.Support.ID, f.Alice.MembershipID)
	waitDisconnected(t, alice, 2*time.Second)

	bob := gateway.login(t, "bob@mail.example", f.BobSecret)
	if _, err := f.Service.UpdateMailClientSettings(ctx, f.Owner, false); err != nil {
		t.Fatal(err)
	}
	waitDisconnected(t, bob, 2*time.Second)
}

func TestIMAPConnectionCaps(t *testing.T) {
	f := newMailClientFixture(t)
	gateway := startIMAPGateway(t, f, imapgateway.Config{MaxPerIP: 2, MaxPerUser: 1})
	first := gateway.login(t, "alice@mail.example", f.AliceSecret)
	second := gateway.dial(t, nil)
	if typ, code := gatewayStatus(t, second.Login("alice@mail.example", f.AliceSecret).Wait()); typ != imap.StatusResponseTypeNo || code != imap.ResponseCodeLimit {
		t.Fatalf("a second session for Alice = %s %s; want NO [LIMIT]", typ, code)
	}
	third, err := imapclient.DialTLS(gateway.Addr, &imapclient.Options{TLSConfig: &tls.Config{InsecureSkipVerify: true}}) //nolint:gosec // the test's own certificate
	if err == nil {
		defer third.Close()
		if err = third.Noop().Wait(); err == nil {
			t.Fatal("a third connection from one address was served")
		}
	}
	if err := first.Logout().Wait(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return gateway.Server.Connections() == 1 })
	if err := second.Login("alice@mail.example", f.AliceSecret).Wait(); err != nil {
		t.Fatalf("signing in after the first session ended: %v", err)
	}
}

// TestIMAPLogsContainNoSecrets checks the gateway's log lines never carry a username, password,
// address, subject, or body.
func TestIMAPLogsContainNoSecrets(t *testing.T) {
	f := newMailClientFixture(t)
	f.Deliver(t, f.AlicePersonal, clientMail("confidential-subject", "confidential-body"))
	gateway := startIMAPGateway(t, f, imapgateway.Config{})
	client := gateway.dial(t, nil)
	_ = client.Login("alice@mail.example", "wrong wrong wrong wrong").Wait()
	if err := client.Login("alice@mail.example", f.AliceSecret).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	fetchAll(t, client, imap.SeqSetNum(1), &imap.FetchOptions{Envelope: true, BodySection: []*imap.FetchItemBodySection{{}}})
	_, _ = client.Select("Shared/nowhere@mail.example/INBOX", nil).Wait()
	_ = client.Logout().Wait()
	waitFor(t, func() bool { return gateway.Server.Connections() == 0 })
	logs := gateway.Logs.String()
	if !strings.Contains(logs, "imap_gateway.session_opened") {
		t.Fatalf("no sign-in was logged: %s", logs)
	}
	for _, secret := range []string{"alice", "mail.example", f.AliceSecret, "wrong wrong", "confidential", "nowhere", "dana"} {
		if strings.Contains(strings.ToLower(logs), strings.ToLower(secret)) {
			t.Fatalf("the gateway logged %q: %s", secret, logs)
		}
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
