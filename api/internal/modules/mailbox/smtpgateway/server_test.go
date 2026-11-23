package smtpgateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	testUser     = "alice@mail.example"
	testPassword = "abcd efgh ijkl mnop"
	testSubject  = "Quarterly secret plans"
)

// fakeStore signs testUser in with testPassword and lets them send from their own address.
type fakeStore struct {
	mu          sync.Mutex
	valid       bool
	submissions []domain.MailClientSubmission
	submitErr   error
	authErr     error
}

func newFakeStore() *fakeStore { return &fakeStore{valid: true} }

func (f *fakeStore) AuthenticateClient(_ context.Context, username, password string, protocol domain.MailClientProtocol, _ string) (*domain.MailClientPrincipal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.authErr != nil {
		return nil, f.authErr
	}
	if protocol != domain.MailClientProtocolSMTP || strings.ToLower(username) != testUser || password != testPassword {
		return nil, domain.ErrMailClientAuthFailed
	}
	return &domain.MailClientPrincipal{PasswordID: 7, Viewer: domain.OrganizationContext{OrganizationID: 1, MembershipID: 11, UserID: 2}}, nil
}

func (f *fakeStore) RevalidateClients(_ context.Context, principals []*domain.MailClientPrincipal) (map[uint]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := map[uint]bool{}
	for _, p := range principals {
		result[p.PasswordID] = f.valid
	}
	return result, nil
}

func (f *fakeStore) ClientSenderAllowed(_ context.Context, _ *domain.MailClientPrincipal, address string) (bool, error) {
	return address == testUser, nil
}

func (f *fakeStore) SubmitClientMessage(_ context.Context, _ *domain.MailClientPrincipal, submission domain.MailClientSubmission) (*domain.MailClientSubmissionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	f.submissions = append(f.submissions, submission)
	return &domain.MailClientSubmissionResult{Status: domain.MailClientSubmissionSent, MessageID: uint(len(f.submissions))}, nil
}

func (f *fakeStore) set(change func(*fakeStore)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

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

func selfSigned(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "smtp.test"},
		DNSNames: []string{"smtp.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
}

type gateway struct {
	server             *Server
	store              *fakeStore
	logs               *syncBuffer
	implicit, starttls string
}

func startGateway(t *testing.T, cfg Config) *gateway {
	t.Helper()
	if cfg.FailureDelay == 0 {
		cfg.FailureDelay = -1
	}
	cfg.Domain = "smtp.test"
	store := newFakeStore()
	logs := &syncBuffer{}
	server := NewServer(store, selfSigned(t), cfg, slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	implicit, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	starttls, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.ServeImplicitTLS(implicit) }()
	go func() { _ = server.ServeSubmission(starttls) }()
	t.Cleanup(func() { _ = server.Close() })
	return &gateway{server: server, store: store, logs: logs, implicit: implicit.Addr().String(), starttls: starttls.Addr().String()}
}

// textClient speaks raw SMTP lines.
type textClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dialText(t *testing.T, addr string, implicitTLS bool) *textClient {
	t.Helper()
	var conn net.Conn
	var err error
	if implicitTLS {
		conn, err = tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // self-signed test certificate
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	c := &textClient{t: t, conn: conn, r: bufio.NewReader(conn)}
	c.expect("220")
	return c
}

// reply reads one reply, joining its lines.
func (c *textClient) reply() string {
	c.t.Helper()
	var lines []string
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			lines = append(lines, "EOF")
			return strings.Join(lines, "\n")
		}
		line = strings.TrimRight(line, "\r\n")
		lines = append(lines, line)
		if len(line) < 4 || line[3] != '-' {
			return strings.Join(lines, "\n")
		}
	}
}

func (c *textClient) send(line string) string {
	c.t.Helper()
	if _, err := c.conn.Write([]byte(line + "\r\n")); err != nil {
		c.t.Fatalf("write %q: %v", line, err)
	}
	return c.reply()
}

func (c *textClient) expect(prefix string) string {
	c.t.Helper()
	got := c.reply()
	if !strings.HasPrefix(got, prefix) {
		c.t.Fatalf("reply %q; want %s", got, prefix)
	}
	return got
}

func (c *textClient) must(line, prefix string) string {
	c.t.Helper()
	got := c.send(line)
	if !strings.HasPrefix(got, prefix) {
		c.t.Fatalf("%s = %q; want %s", strings.SplitN(line, " ", 2)[0], got, prefix)
	}
	return got
}

func (c *textClient) startTLS() {
	c.t.Helper()
	c.must("STARTTLS", "220")
	tlsConn := tls.Client(c.conn, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // self-signed test certificate
	if err := tlsConn.Handshake(); err != nil {
		c.t.Fatal(err)
	}
	c.conn, c.r = tlsConn, bufio.NewReader(tlsConn)
}

func plain(password string) string {
	return base64.StdEncoding.EncodeToString([]byte("\x00" + testUser + "\x00" + password))
}

func message() string {
	return "From: " + testUser + "\r\nTo: carol@example.com" + "\r\nSubject: " + testSubject + "\r\nMessage-ID: <m1@laptop>\r\n\r\nHello there\r\n"
}

func TestImplicitTLSSendsWithStdlibClient(t *testing.T) {
	g := startGateway(t, Config{})
	conn, err := tls.Dial("tcp", g.implicit, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // self-signed test certificate
	if err != nil {
		t.Fatal(err)
	}
	client, err := smtp.NewClient(conn, "smtp.test")
	if err != nil {
		t.Fatal(err)
	}
	if ok, mechanisms := client.Extension("AUTH"); !ok || !strings.Contains(mechanisms, "PLAIN") || !strings.Contains(mechanisms, "LOGIN") {
		t.Fatalf("AUTH = %t %q", ok, mechanisms)
	}
	if ok, size := client.Extension("SIZE"); !ok || size != "8388608" {
		t.Fatalf("SIZE = %t %q", ok, size)
	}
	if err = client.Auth(smtp.PlainAuth("", testUser, testPassword, "smtp.test")); err != nil {
		t.Fatalf("auth: %v", err)
	}
	if err = client.Mail(testUser); err != nil {
		t.Fatal(err)
	}
	for _, rcpt := range []string{"Carol@Example.com", "carol@example.com", "dan@example.com"} {
		if err = client.Rcpt(rcpt); err != nil {
			t.Fatalf("rcpt %s: %v", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(message())); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("data: %v", err)
	}
	if err := client.Quit(); err != nil {
		t.Fatal(err)
	}
	if len(g.store.submissions) != 1 {
		t.Fatalf("submissions %d", len(g.store.submissions))
	}
	got := g.store.submissions[0]
	if got.EnvelopeFrom != testUser || fmt.Sprint(got.Recipients) != "[carol@example.com dan@example.com]" || !bytes.Contains(got.Raw, []byte(testSubject)) {
		t.Fatalf("submission %+v", got)
	}
	assertNoSecrets(t, g.logs.String())
}

func TestSubmissionPortRequiresSTARTTLSAndLogin(t *testing.T) {
	g := startGateway(t, Config{})
	c := dialText(t, g.starttls, false)
	ehlo := c.must("EHLO laptop", "250")
	if strings.Contains(ehlo, "AUTH") || !strings.Contains(ehlo, "STARTTLS") {
		t.Fatalf("EHLO before TLS = %q", ehlo)
	}
	c.must("AUTH PLAIN "+plain(testPassword), "523 5.7.10")
	c.must("MAIL FROM:<"+testUser+">", "530 5.7.0")
	c.startTLS()
	if ehlo := c.must("EHLO laptop", "250"); !strings.Contains(ehlo, "AUTH PLAIN LOGIN") || strings.Contains(ehlo, "STARTTLS") {
		t.Fatalf("EHLO after TLS = %q", ehlo)
	}
	c.must("AUTH LOGIN", "334 "+base64.StdEncoding.EncodeToString([]byte("Username:")))
	c.must(base64.StdEncoding.EncodeToString([]byte(testUser)), "334 "+base64.StdEncoding.EncodeToString([]byte("Password:")))
	c.must(base64.StdEncoding.EncodeToString([]byte(testPassword)), "235")
	c.must("MAIL FROM:<"+testUser+"> SIZE=9000000", "552 5.3.4")
	c.must("MAIL FROM:<someone-else@mail.example>", "550 5.7.1")
	c.must("MAIL FROM:<>", "550 5.7.1")
	c.must("MAIL FROM:<"+testUser+">", "250")
	c.must("RCPT TO:<someone@localhost>", "501 5.1.3")
	for i := range 50 {
		c.must(fmt.Sprintf("RCPT TO:<r%d@example.com>", i), "250")
	}
	c.must("RCPT TO:<r0@example.com>", "250")
	c.must("RCPT TO:<r50@example.com>", "452 4.5.3")
	c.must("RSET", "250")
	c.must("MAIL FROM:<"+testUser+">", "250")
	c.must("RCPT TO:<carol@example.com>", "250")
	c.must("DATA", "354")
	c.must(message()+".", "250 2.0.0 Ok")
	c.must("QUIT", "221")
	assertNoSecrets(t, g.logs.String())
}

func TestAuthLoginWithInitialResponse(t *testing.T) {
	g := startGateway(t, Config{})
	c := dialText(t, g.implicit, true)
	c.must("EHLO laptop", "250")
	c.must("AUTH LOGIN "+base64.StdEncoding.EncodeToString([]byte(testUser)), "334 "+base64.StdEncoding.EncodeToString([]byte("Password:")))
	c.must(base64.StdEncoding.EncodeToString([]byte(testPassword)), "235")
}

func TestWrongPasswordsLockTheUsername(t *testing.T) {
	g := startGateway(t, Config{})
	for i := range 10 {
		c := dialText(t, g.implicit, true)
		c.must("EHLO laptop", "250")
		c.must("AUTH PLAIN "+plain(fmt.Sprintf("wrong %d", i)), "535 5.7.8")
	}
	c := dialText(t, g.implicit, true)
	c.must("EHLO laptop", "250")
	c.must("AUTH PLAIN "+plain(testPassword), "454 4.7.0")
	// Another identity than the username is refused like a wrong password.
	other := startGateway(t, Config{})
	d := dialText(t, other.implicit, true)
	d.must("EHLO laptop", "250")
	d.must("AUTH PLAIN "+base64.StdEncoding.EncodeToString([]byte("admin\x00"+testUser+"\x00"+testPassword)), "535 5.7.8")
	assertNoSecrets(t, g.logs.String())
}

func TestStoreFailureIsTemporary(t *testing.T) {
	g := startGateway(t, Config{})
	g.store.set(func(f *fakeStore) { f.authErr = errors.New("connection refused to 10.0.0.1 for " + testUser) })
	c := dialText(t, g.implicit, true)
	c.must("EHLO laptop", "250")
	c.must("AUTH PLAIN "+plain(testPassword), "454 4.7.0")
	assertNoSecrets(t, g.logs.String())
}

func TestRevokedPasswordEndsTheConnectionAtData(t *testing.T) {
	g := startGateway(t, Config{})
	c := dialText(t, g.implicit, true)
	c.must("EHLO laptop", "250")
	c.must("AUTH PLAIN "+plain(testPassword), "235")
	c.must("MAIL FROM:<"+testUser+">", "250")
	c.must("RCPT TO:<carol@example.com>", "250")
	g.store.set(func(f *fakeStore) { f.valid = false })
	c.must("DATA", "354")
	c.must(message()+".", "421 4.7.0")
	if got := c.reply(); !strings.HasPrefix(got, "421") && got != "EOF" {
		t.Fatalf("after revocation = %q; want the connection closed", got)
	}
	if len(g.store.submissions) != 0 {
		t.Fatal("a revoked password sent a message")
	}
}

func TestSubmitRefusalsMapToReplies(t *testing.T) {
	g := startGateway(t, Config{})
	g.store.set(func(f *fakeStore) {
		f.submitErr = &domain.MailClientSubmitError{Kind: domain.MailClientSubmitLimitReached}
	})
	c := dialText(t, g.implicit, true)
	c.must("EHLO laptop", "250")
	c.must("AUTH PLAIN "+plain(testPassword), "235")
	c.must("MAIL FROM:<"+testUser+">", "250")
	c.must("RCPT TO:<carol@example.com>", "250")
	c.must("DATA", "354")
	c.must(message()+".", "452 4.3.1")
}

func TestReplyFor(t *testing.T) {
	for kind, want := range map[domain.MailClientSubmitErrorKind]string{
		domain.MailClientSubmitSenderNotAllowed: "550 5.7.1",
		domain.MailClientSubmitTooLarge:         "552 5.3.4",
		domain.MailClientSubmitInvalid:          "554 5.6.0",
		domain.MailClientSubmitConflict:         "554 5.6.0",
		domain.MailClientSubmitLimitReached:     "452 4.3.1",
		domain.MailClientSubmitPaused:           "550 5.7.1",
		domain.MailClientSubmitDeliveryFailed:   "451 4.4.1",
		domain.MailClientSubmitInProgress:       "451 4.4.1",
		domain.MailClientSubmitNotConfigured:    "451 4.3.5",
	} {
		got, outcome := replyFor(&domain.MailClientSubmitError{Kind: kind, Reason: "Some reason"})
		if code := fmt.Sprintf("%d %d.%d.%d", got.Code, got.EnhancedCode[0], got.EnhancedCode[1], got.EnhancedCode[2]); code != want || outcome != string(kind) {
			t.Errorf("%s = %s (%s); want %s", kind, code, outcome, want)
		}
	}
	if got, _ := replyFor(&domain.MailClientSubmitError{Kind: domain.MailClientSubmitTooLarge, Reason: "More than 50 recipients"}); got.Code != 452 {
		t.Errorf("too many recipients = %d", got.Code)
	}
	if got, _ := replyFor(&domain.MailClientSubmitError{Kind: domain.MailClientSubmitInvalid, Reason: "Add at least one recipient in To"}); got.Message != "Add at least one recipient in To" {
		t.Errorf("reason = %q", got.Message)
	}
	if got, outcome := replyFor(errors.New("database is down")); got.Code != 451 || outcome != outcomeError || strings.Contains(got.Message, "database") {
		t.Errorf("unknown error = %+v %s", got, outcome)
	}
}

func TestConnectionsWithoutSignInTimeOut(t *testing.T) {
	g := startGateway(t, Config{AuthTimeout: 300 * time.Millisecond})
	c := dialText(t, g.starttls, false)
	c.must("EHLO laptop", "250")
	c.startTLS()
	c.must("EHLO laptop", "250")
	time.Sleep(500 * time.Millisecond)
	if got := c.send("NOOP"); !strings.HasPrefix(got, "421") && got != "EOF" {
		t.Fatalf("after the sign-in timeout = %q", got)
	}
	// Signing in in time stops the clock.
	d := dialText(t, g.implicit, true)
	d.must("EHLO laptop", "250")
	d.must("AUTH PLAIN "+plain(testPassword), "235")
	time.Sleep(500 * time.Millisecond)
	d.must("NOOP", "250")
}

func TestConnectionCaps(t *testing.T) {
	g := startGateway(t, Config{MaxPerIP: 2, MaxPerUser: 1})
	first := dialText(t, g.starttls, false)
	second := dialText(t, g.implicit, true)
	conn, err := net.Dial("tcp", g.starttls)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	if !strings.HasPrefix(line, "421 4.7.0") {
		t.Fatalf("third connection from one address = %q", line)
	}
	second.must("EHLO laptop", "250")
	second.must("AUTH PLAIN "+plain(testPassword), "235")
	first.must("EHLO laptop", "250")
	first.startTLS()
	first.must("EHLO laptop", "250")
	first.must("AUTH PLAIN "+plain(testPassword), "454 4.7.0")
	if g.server.Connections() != 2 {
		t.Fatalf("connections = %d", g.server.Connections())
	}
}

func TestShutdownEndsIdleConnections(t *testing.T) {
	g := startGateway(t, Config{})
	c := dialText(t, g.implicit, true)
	c.must("EHLO laptop", "250")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !g.server.Closing() {
		t.Fatal("not closing")
	}
	if got := c.reply(); !strings.HasPrefix(got, "421") && got != "EOF" {
		t.Fatalf("after shutdown = %q", got)
	}
}

func TestLoginServerSteps(t *testing.T) {
	var gotUser, gotPassword string
	server := &loginServer{authenticate: func(u, p string) error { gotUser, gotPassword = u, p; return nil }}
	if challenge, done, err := server.Next(nil); string(challenge) != "Username:" || done || err != nil {
		t.Fatalf("first = %q %t %v", challenge, done, err)
	}
	if challenge, done, err := server.Next([]byte("u")); string(challenge) != "Password:" || done || err != nil {
		t.Fatalf("second = %q %t %v", challenge, done, err)
	}
	if _, done, err := server.Next([]byte("p")); !done || err != nil || gotUser != "u" || gotPassword != "p" {
		t.Fatalf("third = %t %v %q %q", done, err, gotUser, gotPassword)
	}
	if _, _, err := server.Next([]byte("x")); err == nil {
		t.Fatal("a fourth response is refused")
	}
	var _ gosmtp.Backend = backend{}
}

// assertNoSecrets fails when a log line carries a username, a password, an address, or a subject.
func assertNoSecrets(t *testing.T, logs string) {
	t.Helper()
	for _, secret := range []string{testUser, testPassword, "carol@example.com", testSubject, "Hello there", "127.0.0.1"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs carry %q:\n%s", secret, logs)
		}
	}
}
