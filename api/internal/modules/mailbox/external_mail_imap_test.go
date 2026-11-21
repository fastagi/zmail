package mailbox

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

const fakeIMAPPassword = "app-password"

// fakeIMAPMessage is one message of the fake server's INBOX.
type fakeIMAPMessage struct {
	uid  uint32
	date time.Time
	raw  []byte
}

// fakeIMAPServer is a scripted IMAP4rev1 server over TLS with a self-signed certificate. It
// accepts any username with fakeIMAPPassword and serves one INBOX.
type fakeIMAPServer struct {
	t        *testing.T
	listener net.Listener
	roots    *x509.CertPool
	port     int

	mu          sync.Mutex
	uidValidity uint32
	nextUID     uint32
	messages    []fakeIMAPMessage
	// commands are the raw command lines received, literals included.
	commands []string
	// bodies are the UIDs whose body was served.
	bodies      []uint32
	connections int
}

func newFakeIMAPServer(t *testing.T) *fakeIMAPServer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "imap.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"imap.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeIMAPServer{
		t: t, listener: listener, roots: roots, port: listener.Addr().(*net.TCPAddr).Port,
		uidValidity: 7001, nextUID: 1,
	}
	t.Cleanup(func() { _ = listener.Close() })
	go server.serve()
	return server
}

// source is an IMAP source that trusts the server and may reach it on loopback.
func (f *fakeIMAPServer) source() *imapSource {
	source := newIMAPSource()
	source.policy = imapNetworkPolicy{AllowPrivate: true, AllowAnyPort: true, RootCAs: f.roots}
	source.dialTimeout, source.commandTimeout = 5*time.Second, 5*time.Second
	return source
}

func (f *fakeIMAPServer) credentials(username string) ExternalMailPasswordCredentials {
	return ExternalMailPasswordCredentials{Host: "127.0.0.1", Port: f.port, Username: username, Password: fakeIMAPPassword}
}

// deliver appends a message received at date and returns its UID.
func (f *fakeIMAPServer) deliver(subject string, date time.Time) uint32 {
	raw := fmt.Sprintf("From: Carol <carol@sender.test>\r\nTo: dave@imap.test\r\nSubject: %s\r\n"+
		"Message-ID: <%s@sender.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBody of %s\r\n",
		subject, strings.ReplaceAll(subject, " ", "-"), subject)
	return f.deliverRaw([]byte(raw), date)
}

func (f *fakeIMAPServer) deliverRaw(raw []byte, date time.Time) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	uid := f.nextUID
	f.nextUID++
	f.messages = append(f.messages, fakeIMAPMessage{uid: uid, date: date, raw: raw})
	return uid
}

// renumber changes UIDVALIDITY and gives every message a new UID, as a rebuilt mailbox does.
func (f *fakeIMAPServer) renumber() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uidValidity++
	for i := range f.messages {
		f.messages[i].uid = f.nextUID
		f.nextUID++
	}
}

func (f *fakeIMAPServer) served() (commands []string, bodies []uint32, connections int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.commands), slices.Clone(f.bodies), f.connections
}

func (f *fakeIMAPServer) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.connections++
		f.mu.Unlock()
		go f.session(conn)
	}
}

// session answers one client. Arguments are quoted strings, atoms, or synchronizing literals.
func (f *fakeIMAPServer) session(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	write := func(format string, args ...any) { _, _ = fmt.Fprintf(conn, format+"\r\n", args...) }
	write("* OK [CAPABILITY IMAP4rev1] fake ready")
	authenticated := false
	for {
		raw, args, err := readFakeCommand(reader, conn)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.commands = append(f.commands, raw)
		f.mu.Unlock()
		if len(args) < 2 {
			write("* BAD malformed")
			continue
		}
		tag, command := args[0], strings.ToUpper(args[1])
		switch {
		case command == "LOGIN" && len(args) == 4:
			if args[3] != fakeIMAPPassword {
				write("%s NO [AUTHENTICATIONFAILED] Invalid credentials", tag)
				continue
			}
			authenticated = true
			write("%s OK LOGIN completed", tag)
		case command == "LOGOUT":
			write("* BYE logging out")
			write("%s OK LOGOUT completed", tag)
			return
		case !authenticated:
			write("%s BAD not authenticated", tag)
		case command == "EXAMINE" || command == "SELECT":
			f.mu.Lock()
			write("* %d EXISTS", len(f.messages))
			write("* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)")
			write("* OK [UIDVALIDITY %d] UIDs valid", f.uidValidity)
			write("* OK [UIDNEXT %d] Predicted next UID", f.nextUID)
			f.mu.Unlock()
			write("%s OK [READ-ONLY] EXAMINE completed", tag)
		case command == "UID" && len(args) >= 4 && strings.EqualFold(args[2], "SEARCH"):
			write("* SEARCH%s", f.search(args[3:]))
			write("%s OK SEARCH completed", tag)
		case command == "UID" && len(args) >= 5 && strings.EqualFold(args[2], "FETCH"):
			f.fetch(conn, args[3], strings.Join(args[4:], " "))
			write("%s OK FETCH completed", tag)
		default:
			write("%s BAD unknown command", tag)
		}
	}
}

func (f *fakeIMAPServer) search(criteria []string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []string
	switch {
	case len(criteria) == 2 && strings.EqualFold(criteria[0], "SINCE"):
		since, err := time.Parse("2-Jan-2006", criteria[1])
		if err != nil {
			f.t.Errorf("SINCE date %q: %v", criteria[1], err)
		}
		for _, message := range f.messages {
			if !message.date.Before(since) {
				found = append(found, " "+strconv.Itoa(int(message.uid)))
			}
		}
	case len(criteria) == 2 && strings.EqualFold(criteria[0], "UID"):
		low, _ := strconv.Atoi(strings.TrimSuffix(criteria[1], ":*"))
		var highest uint32
		for _, message := range f.messages {
			highest = max(highest, message.uid)
			if int(message.uid) >= low {
				found = append(found, " "+strconv.Itoa(int(message.uid)))
			}
		}
		// "n:*" always matches the highest UID, even below n (RFC 3501, section 6.4.8).
		if len(found) == 0 && highest > 0 {
			found = append(found, " "+strconv.Itoa(int(highest)))
		}
	default:
		f.t.Errorf("unexpected SEARCH criteria %q", criteria)
	}
	return strings.Join(found, "")
}

func (f *fakeIMAPServer) fetch(conn net.Conn, set, items string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wantBody := strings.Contains(strings.ToUpper(items), "BODY.PEEK[]")
	for _, part := range strings.Split(set, ",") {
		uid, _ := strconv.Atoi(part)
		for sequence, message := range f.messages {
			if int(message.uid) != uid {
				continue
			}
			response := fmt.Sprintf("* %d FETCH (UID %d RFC822.SIZE %d", sequence+1, message.uid, len(message.raw))
			if wantBody {
				f.bodies = append(f.bodies, message.uid)
				response += fmt.Sprintf(" FLAGS (\\Seen) INTERNALDATE %q BODY[] {%d}\r\n",
					message.date.Format("02-Jan-2006 15:04:05 -0700"), len(message.raw))
				_, _ = io.WriteString(conn, response)
				_, _ = conn.Write(message.raw)
				_, _ = io.WriteString(conn, ")\r\n")
				continue
			}
			_, _ = io.WriteString(conn, response+")\r\n")
		}
	}
}

// readFakeCommand reads one command, inviting each synchronizing literal, and splits it into
// arguments.
func readFakeCommand(reader *bufio.Reader, conn net.Conn) (string, []string, error) {
	var raw strings.Builder
	var args []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", nil, err
		}
		raw.WriteString(line)
		line = strings.TrimRight(line, "\r\n")
		size := -1
		if open := strings.LastIndexByte(line, '{'); open >= 0 && strings.HasSuffix(line, "}") {
			if parsed, err := strconv.Atoi(line[open+1 : len(line)-1]); err == nil {
				size, line = parsed, line[:open]
			}
		}
		args = append(args, splitFakeArgs(line)...)
		if size < 0 {
			return raw.String(), args, nil
		}
		_, _ = io.WriteString(conn, "+ go ahead\r\n")
		literal := make([]byte, size)
		if _, err := io.ReadFull(reader, literal); err != nil {
			return "", nil, err
		}
		raw.Write(literal)
		args = append(args, string(literal))
	}
}

func splitFakeArgs(line string) []string {
	var args []string
	for i := 0; i < len(line); {
		switch line[i] {
		case ' ':
			i++
		case '"':
			var value strings.Builder
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				value.WriteByte(line[i])
				i++
			}
			i++
			args = append(args, value.String())
		default:
			end := strings.IndexByte(line[i:], ' ')
			if end < 0 {
				end = len(line) - i
			}
			args = append(args, line[i:i+end])
			i += end
		}
	}
	return args
}

// verifyIMAP binds the username on the fake server and returns the stored grant.
func verifyIMAP(t *testing.T, source *imapSource, credentials ExternalMailPasswordCredentials) ExternalMailToken {
	t.Helper()
	token, account, err := source.VerifyPassword(context.Background(), credentials)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if account.Address != strings.ToLower(credentials.Username) {
		t.Fatalf("account = %q, want %q", account.Address, credentials.Username)
	}
	return token
}

func fetchIMAP(t *testing.T, source *imapSource, token ExternalMailToken, cursor string, now time.Time) ExternalMailFetch {
	t.Helper()
	fetched, err := source.Fetch(context.Background(), ExternalMailFetchRequest{
		Token: token, Cursor: cursor, BackfillDays: 30, Now: now,
	})
	if err != nil {
		t.Fatalf("Fetch(cursor %q): %v", cursor, err)
	}
	return fetched
}

func TestIMAPVerifyRefusesWrongPassword(t *testing.T) {
	server := newFakeIMAPServer(t)
	source := server.source()
	credentials := server.credentials("dave@imap.test")
	credentials.Password = "wrong"
	if _, _, err := source.VerifyPassword(context.Background(), credentials); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("wrong password = %v, want ErrExternalMailAuth", err)
	}
	token := verifyIMAP(t, source, server.credentials("dave@imap.test"))
	var sealed imapCredentials
	if err := json.Unmarshal([]byte(token.RefreshToken), &sealed); err != nil || sealed.Password != fakeIMAPPassword ||
		sealed.Host != "127.0.0.1" || sealed.Port != server.port || sealed.Username != "dave@imap.test" {
		t.Fatalf("grant = %+v, err %v", sealed, err)
	}
	// A password changed at the provider later stops the sync with an authorization failure.
	sealed.Password = "revoked"
	changed, _ := json.Marshal(sealed)
	if _, err := source.Fetch(context.Background(), ExternalMailFetchRequest{
		Token: ExternalMailToken{RefreshToken: string(changed)}, Now: time.Now(),
	}); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("fetch with a changed password = %v, want ErrExternalMailAuth", err)
	}
}

// TestIMAPBackfillPagesAndIncrementalFetch: an empty cursor reads the backfill window page by
// page; later fetches return only newer mail, and a fetch with nothing new returns nothing.
func TestIMAPBackfillPagesAndIncrementalFetch(t *testing.T) {
	server := newFakeIMAPServer(t)
	source := server.source()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	server.deliver("Too old", now.AddDate(0, 0, -45))
	for i := range 30 {
		server.deliver(fmt.Sprintf("Recent %02d", i), now.AddDate(0, 0, -20).Add(time.Duration(i)*time.Hour))
	}
	token := verifyIMAP(t, source, server.credentials("Dave@imap.test"))

	first := fetchIMAP(t, source, token, "", now)
	if len(first.Messages) != 25 || !first.More {
		t.Fatalf("first page = %d messages, more %v; want 25 and more", len(first.Messages), first.More)
	}
	message := first.Messages[0]
	if message.ExternalID != "7001:2" || message.Subject != "Recent 00" || message.FromAddress != "carol@sender.test" ||
		message.FromName != "Carol" || message.RFCMessageID != "Recent-00@sender.test" ||
		!strings.Contains(message.TextBody, "Body of Recent 00") || !message.ReceivedAt.Equal(now.AddDate(0, 0, -20)) {
		t.Fatalf("first message = %+v", message)
	}
	second := fetchIMAP(t, source, token, first.Cursor, now)
	if len(second.Messages) != 5 || second.More || second.Messages[4].Subject != "Recent 29" {
		t.Fatalf("second page = %d messages, more %v", len(second.Messages), second.More)
	}
	if idle := fetchIMAP(t, source, token, second.Cursor, now); len(idle.Messages) != 0 || idle.More {
		t.Fatalf("idle fetch = %+v; want nothing", idle)
	}

	server.deliver("Fresh", now)
	fresh := fetchIMAP(t, source, token, second.Cursor, now)
	if len(fresh.Messages) != 1 || fresh.Messages[0].Subject != "Fresh" || fresh.Messages[0].ExternalID != "7001:32" {
		t.Fatalf("incremental fetch = %+v", fresh.Messages)
	}
	var cursor imapCursor
	if err := json.Unmarshal([]byte(fresh.Cursor), &cursor); err != nil || cursor != (imapCursor{UIDValidity: 7001, LastUID: 32}) {
		t.Fatalf("cursor = %s, err %v", fresh.Cursor, err)
	}
	if again := fetchIMAP(t, source, token, `{"uidvalidity":7001,"last_uid":31}`, now); len(again.Messages) != 1 {
		t.Fatalf("refetch after UID 31 = %d messages, want 1", len(again.Messages))
	}

	commands, _, _ := server.served()
	for _, command := range commands {
		if strings.Contains(command, "SELECT") || strings.Contains(command, "STORE") || strings.Contains(command, " BODY[]") {
			t.Fatalf("the source changed the mailbox: %q", command)
		}
	}
}

// TestIMAPEmptyWindowStartsAtNewMail: a backfill window without mail moves the cursor to the
// newest UID, so older mail outside the window is never read later.
func TestIMAPEmptyWindowStartsAtNewMail(t *testing.T) {
	server := newFakeIMAPServer(t)
	source := server.source()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	server.deliver("Ancient", now.AddDate(-1, 0, 0))
	token := verifyIMAP(t, source, server.credentials("dave@imap.test"))
	empty := fetchIMAP(t, source, token, "", now)
	if len(empty.Messages) != 0 || empty.More {
		t.Fatalf("empty window = %+v", empty)
	}
	server.deliver("New", now)
	next := fetchIMAP(t, source, token, empty.Cursor, now)
	if len(next.Messages) != 1 || next.Messages[0].Subject != "New" {
		t.Fatalf("after an empty window = %+v", next.Messages)
	}
}

func TestIMAPUIDValidityChangeExpiresCursor(t *testing.T) {
	server := newFakeIMAPServer(t)
	source := server.source()
	now := time.Now()
	server.deliver("One", now)
	token := verifyIMAP(t, source, server.credentials("dave@imap.test"))
	first := fetchIMAP(t, source, token, "", now)
	server.renumber()
	if _, err := source.Fetch(context.Background(), ExternalMailFetchRequest{
		Token: token, Cursor: first.Cursor, BackfillDays: 30, Now: now,
	}); !errors.Is(err, ErrExternalMailCursorExpired) {
		t.Fatalf("fetch after a UIDVALIDITY change = %v, want cursor expired", err)
	}
	if _, err := source.Fetch(context.Background(), ExternalMailFetchRequest{
		Token: token, Cursor: "not json", Now: now,
	}); !errors.Is(err, ErrExternalMailCursorExpired) {
		t.Fatalf("fetch with an unreadable cursor = %v, want cursor expired", err)
	}
	restarted := fetchIMAP(t, source, token, "", now)
	if len(restarted.Messages) != 1 || restarted.Messages[0].ExternalID != "7002:2" {
		t.Fatalf("restarted backfill = %+v", restarted.Messages)
	}
}

// TestIMAPSkipsOversizedMessage: a message above the inbound limit is never downloaded, and the
// cursor moves past it.
func TestIMAPSkipsOversizedMessage(t *testing.T) {
	server := newFakeIMAPServer(t)
	source := server.source()
	now := time.Now()
	server.deliver("Before", now)
	huge := []byte("From: carol@sender.test\r\nSubject: Huge\r\n\r\n" + strings.Repeat("x", MaxInboundBytes) + "\r\n")
	hugeUID := server.deliverRaw(huge, now)
	server.deliver("After", now)
	token := verifyIMAP(t, source, server.credentials("dave@imap.test"))

	fetched := fetchIMAP(t, source, token, "", now)
	if len(fetched.Messages) != 2 || fetched.Messages[0].Subject != "Before" || fetched.Messages[1].Subject != "After" {
		t.Fatalf("fetched = %+v; want the two small messages", fetched.Messages)
	}
	if _, bodies, _ := server.served(); slices.Contains(bodies, hugeUID) {
		t.Fatal("the oversized message was downloaded")
	}
	if again := fetchIMAP(t, source, token, fetched.Cursor, now); len(again.Messages) != 0 {
		t.Fatalf("the oversized message came back: %+v", again.Messages)
	}
}

// TestIMAPLiteralBodyAboveLimitIsDiscarded: a server that reports a small size but sends a larger
// body does not get it stored.
func TestIMAPLiteralBodyAboveLimitIsDiscarded(t *testing.T) {
	session := &imapSession{reader: bufio.NewReaderSize(strings.NewReader(
		fmt.Sprintf("{%d}\r\n%s", MaxInboundBytes+1, strings.Repeat("y", MaxInboundBytes+1))), 1024)}
	value, err := session.readValue(0)
	if err != nil || !value.oversized || value.text != nil {
		t.Fatalf("oversized literal = oversized %v, %d bytes, err %v", value.oversized, len(value.text), err)
	}
}

// TestIMAPCredentialsAreQuotedOrSentAsLiterals: quotes and backslashes are escaped, non-ASCII
// values go as literals, and a value with a line break never reaches the server.
func TestIMAPCredentialsAreQuotedOrSentAsLiterals(t *testing.T) {
	server := newFakeIMAPServer(t)
	source := server.source()
	quoted := server.credentials(`we"ird\user@imap.test`)
	quoted.AccountAddress = "weird@imap.test"
	if _, account, err := source.VerifyPassword(context.Background(), quoted); err != nil || account.Address != "weird@imap.test" {
		t.Fatalf("quoted login = %q, %v", account.Address, err)
	}
	verifyIMAP(t, source, server.credentials("dävid@imap.test"))
	commands, _, _ := server.served()
	if !slices.ContainsFunc(commands, func(c string) bool { return strings.Contains(c, `"we\"ird\\user@imap.test"`) }) {
		t.Fatalf("no escaped quoted LOGIN in %q", commands)
	}
	if !slices.ContainsFunc(commands, func(c string) bool { return strings.Contains(c, "LOGIN {16}\r\ndävid@imap.test") }) {
		t.Fatalf("no literal LOGIN in %q", commands)
	}

	_, _, before := server.served()
	for _, username := range []string{"dave@imap.test\r\nZ9 DELETE INBOX", "dave@imap.test\n", "{5}\r\nhello"} {
		credentials := server.credentials(username)
		if _, _, err := source.VerifyPassword(context.Background(), credentials); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
			t.Fatalf("username %q = %v, want invalid", username, err)
		}
	}
	credentials := server.credentials("dave@imap.test")
	credentials.Password = "pass\r\nZ9 LOGOUT"
	if _, _, err := source.VerifyPassword(context.Background(), credentials); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
		t.Fatalf("password with CRLF = %v, want invalid", err)
	}
	if _, _, after := server.served(); after != before {
		t.Fatalf("an injection attempt reached the server (%d connections, was %d)", after, before)
	}
}

type fixedResolver map[string][]netip.Addr

func (r fixedResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if addresses, ok := r[host]; ok {
		return addresses, nil
	}
	return nil, errors.New("no such host")
}

// TestIMAPRefusesPrivateAddressesAndOtherPorts: production policy never connects to the
// instance's own network, to a port other than 993, or to a server whose certificate does not
// verify.
func TestIMAPRefusesPrivateAddressesAndOtherPorts(t *testing.T) {
	server := newFakeIMAPServer(t)
	production := newIMAPSource()
	production.resolver = fixedResolver{
		"imap.internal.test": {netip.MustParseAddr("10.1.2.3")},
		"mixed.test":         {netip.MustParseAddr("203.0.113.9"), netip.MustParseAddr("192.168.1.1")},
		"metadata.test":      {netip.MustParseAddr("169.254.169.254")},
		"mapped.test":        {netip.MustParseAddr("::ffff:127.0.0.1")},
	}
	cases := []ExternalMailPasswordCredentials{
		{Host: "127.0.0.1", Port: imapPort},
		{Host: "10.0.0.8", Port: imapPort},
		{Host: "::1", Port: imapPort},
		{Host: "0.0.0.0", Port: imapPort},
		{Host: "imap.internal.test", Port: imapPort},
		{Host: "mixed.test", Port: imapPort},
		{Host: "metadata.test", Port: imapPort},
		{Host: "mapped.test", Port: imapPort},
		{Host: "unknown.test", Port: imapPort},
		{Host: "imap.example.com", Port: 143},
	}
	for _, credentials := range cases {
		credentials.Username, credentials.Password = "dave@imap.test", fakeIMAPPassword
		if _, _, err := production.VerifyPassword(context.Background(), credentials); !errors.Is(err, ErrExternalMailServerUnreachable) {
			t.Fatalf("%s:%d = %v, want unreachable", credentials.Host, credentials.Port, err)
		}
	}
	// Even with loopback allowed, the self-signed certificate is not trusted by default.
	untrusted := newIMAPSource()
	untrusted.policy = imapNetworkPolicy{AllowPrivate: true, AllowAnyPort: true}
	if _, _, err := untrusted.VerifyPassword(context.Background(), server.credentials("dave@imap.test")); !errors.Is(err, ErrExternalMailServerUnreachable) {
		t.Fatalf("untrusted certificate = %v, want unreachable", err)
	}
	// A closed port is unreachable, not an authorization failure.
	closed := server.source()
	credentials := server.credentials("dave@imap.test")
	credentials.Port = closedPort(t)
	if _, _, err := closed.VerifyPassword(context.Background(), credentials); !errors.Is(err, ErrExternalMailServerUnreachable) {
		t.Fatalf("closed port = %v, want unreachable", err)
	}
	if _, _, connections := server.served(); connections != 1 {
		t.Fatalf("the server saw %d connections, want only the untrusted handshake", connections)
	}
}

func TestIMAPNetworkPolicyAddresses(t *testing.T) {
	var production imapNetworkPolicy
	for address, want := range map[string]bool{
		"203.0.113.9": true, "2001:4860:4860::8888": true, "8.8.8.8": true,
		"127.0.0.1": false, "10.0.0.1": false, "172.16.0.1": false, "192.168.0.1": false, "100.64.0.1": false,
		"169.254.169.254": false, "0.0.0.0": false, "::": false, "::1": false, "fe80::1": false, "fd00::1": false,
		"224.0.0.1": false, "::ffff:10.0.0.1": false,
	} {
		if got := production.allowedAddress(netip.MustParseAddr(address)); got != want {
			t.Errorf("allowedAddress(%s) = %v, want %v", address, got, want)
		}
	}
}

func TestIMAPConnectorRegistration(t *testing.T) {
	registry := newExternalMailRegistry(config.ExternalMailConfig{Providers: []string{"imap"}})
	connector, ok := registry.lookup(ExternalMailProviderIMAP)
	if !ok || connector.AuthKind != ExternalMailAuthPassword || !connector.Capabilities.Read {
		t.Fatalf("imap connector = %+v, registered %v", connector, ok)
	}
	if connector.Source.AuthURL(ExternalMailAuthRequest{State: "s"}) != "" {
		t.Fatal("imap offers a sign-in page")
	}
	if _, _, err := connector.Source.Exchange(context.Background(), ExternalMailCodeExchange{Code: "c"}); !errors.Is(err, ErrExternalMailUnavailable) {
		t.Fatalf("Exchange = %v", err)
	}
	if _, ok := newExternalMailRegistry(config.ExternalMailConfig{Providers: []string{"gmail"}}).lookup(ExternalMailProviderIMAP); ok {
		t.Fatal("imap registered without being enabled")
	}
}

func closedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}
