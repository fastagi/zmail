package imapgateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/fastagi/zmail/api/internal/domain"
)

// fakeStore signs in "alice" (password 1, root mailbox 10) and "bob" (password 2, root mailbox
// 20), each with one shared mailbox 30, and answers revalidation as the test sets it.
type fakeStore struct {
	mu          sync.Mutex
	valid       map[uint]bool
	hidden      map[uint]bool
	revalidErr  error
	revalidated int
}

func newFakeStore() *fakeStore {
	return &fakeStore{valid: map[uint]bool{1: true, 2: true}, hidden: map[uint]bool{}}
}

func (f *fakeStore) set(change func(*fakeStore)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeStore) AuthenticateClient(_ context.Context, username, password string, _ domain.MailClientProtocol, _ string) (*domain.MailClientPrincipal, error) {
	if password != "secret" {
		return nil, domain.ErrMailClientAuthFailed
	}
	switch username {
	case "alice":
		return &domain.MailClientPrincipal{PasswordID: 1, RootMailboxID: 10, Viewer: domain.OrganizationContext{OrganizationID: 1, MembershipID: 101, UserID: 1}}, nil
	case "bob":
		return &domain.MailClientPrincipal{PasswordID: 2, RootMailboxID: 20, Viewer: domain.OrganizationContext{OrganizationID: 1, MembershipID: 102, UserID: 2}}, nil
	}
	return nil, domain.ErrMailClientAuthFailed
}

func (f *fakeStore) RevalidateClients(_ context.Context, principals []*domain.MailClientPrincipal) (map[uint]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revalidated++
	if f.revalidErr != nil {
		return nil, f.revalidErr
	}
	valid := map[uint]bool{}
	for _, principal := range principals {
		valid[principal.PasswordID] = f.valid[principal.PasswordID]
	}
	return valid, nil
}

func (f *fakeStore) ClientMailboxVisible(_ context.Context, _ *domain.MailClientPrincipal, mailboxID uint) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.hidden[mailboxID], nil
}

func (f *fakeStore) ClientFolders(_ context.Context, p *domain.MailClientPrincipal) ([]domain.MailClientFolder, error) {
	return []domain.MailClientFolder{
		{MailboxID: p.RootMailboxID, Key: domain.MailClientFolderInbox, Name: "Inbox"},
		{MailboxID: 30, Key: domain.MailClientFolderInbox, Name: "Inbox", Shared: true, MailboxAddress: "team@example.test"},
	}, nil
}

func (f *fakeStore) ClientFolderState(context.Context, *domain.MailClientPrincipal, uint, string) (*domain.MailClientFolderState, error) {
	return &domain.MailClientFolderState{UIDValidity: 7, UIDNext: 1}, nil
}

func (f *fakeStore) ClientMessages(context.Context, *domain.MailClientPrincipal, uint, string, []uint32) ([]domain.MailClientMessage, error) {
	return nil, nil
}

func (f *fakeStore) ClientMessageSource(context.Context, *domain.MailClientPrincipal, uint, string, uint32) ([]byte, error) {
	return nil, domain.ErrMailClientMessageNotFound
}

func (f *fakeStore) SetClientFlag(context.Context, *domain.MailClientPrincipal, uint, string, []uint32, domain.MailClientFlag, bool) error {
	return nil
}

func (f *fakeStore) SearchClient(context.Context, *domain.MailClientPrincipal, uint, string, domain.MailClientSearch) ([]uint32, error) {
	return nil, nil
}

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "imap.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

func serveFake(t *testing.T, store domain.MailClientStore, cfg Config) (*Server, string) {
	t.Helper()
	if cfg.FailureDelay == 0 {
		cfg.FailureDelay = -1
	}
	server := NewServer(store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ln, err := tls.Listen("tcp", "127.0.0.1:0", testTLSConfig(t))
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
		<-done
	})
	return server, ln.Addr().String()
}

func signIn(t *testing.T, addr, username string) *imapclient.Client {
	t.Helper()
	client, err := imapclient.DialTLS(addr, &imapclient.Options{TLSConfig: &tls.Config{InsecureSkipVerify: true}}) //nolint:gosec // the test's own certificate
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Login(username, "secret").Wait(); err != nil {
		t.Fatalf("LOGIN %s: %v", username, err)
	}
	return client
}

func waitClosed(t *testing.T, client *imapclient.Client) {
	t.Helper()
	select {
	case <-client.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("the session is still open after 2s")
	}
}

func stillOpen(t *testing.T, client *imapclient.Client) {
	t.Helper()
	if err := client.Noop().Wait(); err != nil {
		t.Fatalf("the session was closed: %v", err)
	}
}

func waitConnections(t *testing.T, server *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for server.Connections() != want {
		if time.Now().After(deadline) {
			t.Fatalf("connections = %d; want %d", server.Connections(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSweepClosesRevokedSessions(t *testing.T) {
	store := newFakeStore()
	server, addr := serveFake(t, store, Config{})
	alice := signIn(t, addr, "alice")
	bob := signIn(t, addr, "bob")
	ctx := context.Background()

	if err := server.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	stillOpen(t, alice)
	stillOpen(t, bob)

	store.set(func(f *fakeStore) { f.valid[1] = false })
	if err := server.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, alice)
	stillOpen(t, bob)
	waitConnections(t, server, 1)
}

func TestSweepClosesSessionsOfAHiddenMailbox(t *testing.T) {
	store := newFakeStore()
	server, addr := serveFake(t, store, Config{})
	alice := signIn(t, addr, "alice")
	bob := signIn(t, addr, "bob")
	if _, err := alice.Select("Shared/team@example.test/INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	store.set(func(f *fakeStore) { f.hidden[30] = true })
	if err := server.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, alice)
	stillOpen(t, bob)

	// Losing the root mailbox ends the session too.
	store.set(func(f *fakeStore) { f.hidden[20] = true })
	if err := server.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, bob)
}

func TestSweepFailsClosed(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	var clock sync.Mutex
	server, addr := serveFake(t, store, Config{Now: func() time.Time { clock.Lock(); defer clock.Unlock(); return now }})
	advance := func(d time.Duration) { clock.Lock(); now = now.Add(d); clock.Unlock() }
	alice := signIn(t, addr, "alice")
	ctx := context.Background()
	if err := server.Sweep(ctx); err != nil {
		t.Fatal(err)
	}

	store.set(func(f *fakeStore) { f.revalidErr = errors.New("database unavailable") })
	advance(20 * time.Second)
	if err := server.Sweep(ctx); err == nil {
		t.Fatal("a failed sweep reported success")
	}
	stillOpen(t, alice)
	advance(25 * time.Second)
	_ = server.Sweep(ctx)
	waitClosed(t, alice)
}

func TestRunSweeperRevokesWithinTheInterval(t *testing.T) {
	store := newFakeStore()
	server, addr := serveFake(t, store, Config{})
	alice := signIn(t, addr, "alice")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.RunSweeper(ctx, 50*time.Millisecond)
	}()
	store.set(func(f *fakeStore) { f.valid[1] = false })
	waitClosed(t, alice)
	cancel()
	<-done
}

func TestSessionRegistryLimits(t *testing.T) {
	registry := newSessionRegistry(3, 1, 2)
	a := &session{id: 1, ip: "198.51.100.1"}
	b := &session{id: 2, ip: "198.51.100.1"}
	c := &session{id: 3, ip: "198.51.100.1"}
	d := &session{id: 4, ip: "198.51.100.2"}
	e := &session{id: 5, ip: "198.51.100.3"}
	if registry.open(a) != nil || registry.open(b) != nil {
		t.Fatal("two connections from one address were refused")
	}
	if !errors.Is(registry.open(c), errTooManyFromAddress) {
		t.Fatal("a third connection from one address was admitted")
	}
	if registry.open(d) != nil {
		t.Fatal("another address was refused")
	}
	if !errors.Is(registry.open(e), errTooManyConnections) {
		t.Fatal("a connection past the total was admitted")
	}
	if registry.signIn(101) != nil || !errors.Is(registry.signIn(101), errTooManyForAccount) {
		t.Fatal("the per-account cap did not hold")
	}
	registry.close(a, 101)
	if registry.signIn(101) != nil || registry.open(c) != nil || registry.count() != 3 {
		t.Fatal("closing a session did not free its slots")
	}
}
