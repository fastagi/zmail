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
	"log/slog"
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
	// loginReply replaces the OK to a correct LOGIN, such as "NO [UNAVAILABLE] later".
	loginReply string
	// stallLogin never answers LOGIN, as a tarpit does.
	stallLogin bool
	// examineReply replaces the OK to EXAMINE.
	examineReply string
	// failBody answers NO to any body FETCH that includes the UID; stallBody never answers it;
	// silent leaves the UID out of every FETCH answer.
	failBody, stallBody, silent map[uint32]bool
	// unsolicited is sent before the answer to every body FETCH: a response about a UID nobody
	// asked for.
	unsolicited string
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
		DNSNames:    []string{"imap.test", "other.imap.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
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
		failBody: map[uint32]bool{}, stallBody: map[uint32]bool{}, silent: map[uint32]bool{},
	}
	t.Cleanup(func() { _ = listener.Close() })
	go server.serve()
	return server
}

// source is an IMAP source that trusts the server and may reach it on loopback.
func (f *fakeIMAPServer) source() *imapSource {
	source := newIMAPSource()
	source.policy = imapNetworkPolicy{AllowPrivate: true, AllowAnyPort: true, RootCAs: f.roots}
	source.dialTimeout, source.commandTimeout, source.overallTimeout = 5*time.Second, 5*time.Second, 20*time.Second
	loopback := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	source.resolver = fixedResolver{"imap.test": loopback, "other.imap.test": loopback}
	return source
}

func (f *fakeIMAPServer) credentials(username string) ExternalMailPasswordCredentials {
	return ExternalMailPasswordCredentials{Host: "127.0.0.1", Port: f.port, Username: username, Password: fakeIMAPPassword}
}

// configure changes the server's behavior under its lock.
func (f *fakeIMAPServer) configure(change func(f *fakeIMAPServer)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// deliver appends a message received at date.
func (f *fakeIMAPServer) deliver(subject string, date time.Time) {
	raw := fmt.Sprintf("From: Carol <carol@sender.test>\r\nTo: dave@imap.test\r\nSubject: %s\r\n"+
		"Message-ID: <%s@sender.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBody of %s\r\n",
		subject, strings.ReplaceAll(subject, " ", "-"), subject)
	f.deliverRaw([]byte(raw), date)
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
			f.mu.Lock()
			stall, reply := f.stallLogin, f.loginReply
			f.mu.Unlock()
			if stall {
				_, _ = io.Copy(io.Discard, reader)
				return
			}
			if args[3] != fakeIMAPPassword {
				write("%s NO [AUTHENTICATIONFAILED] Invalid credentials", tag)
				continue
			}
			if reply != "" {
				write("%s %s", tag, reply)
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
			reply := f.examineReply
			f.mu.Unlock()
			if reply == "" {
				reply = "OK [READ-ONLY] EXAMINE completed"
			}
			write("%s %s", tag, reply)
		case command == "UID" && len(args) >= 4 && strings.EqualFold(args[2], "SEARCH"):
			write("* SEARCH%s", f.search(args[3:]))
			write("%s OK SEARCH completed", tag)
		case command == "UID" && len(args) >= 5 && strings.EqualFold(args[2], "FETCH"):
			switch f.fetch(conn, args[3], strings.Join(args[4:], " ")) {
			case fetchStall:
				_, _ = io.Copy(io.Discard, reader)
				return
			case fetchFail:
				write("%s NO [SERVERBUG] cannot fetch", tag)
			default:
				write("%s OK FETCH completed", tag)
			}
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

type fetchOutcome int

const (
	fetchOK fetchOutcome = iota
	fetchFail
	fetchStall
)

func (f *fakeIMAPServer) fetch(conn net.Conn, set, items string) fetchOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	wantBody := strings.Contains(strings.ToUpper(items), "BODY.PEEK[]")
	if wantBody {
		for _, part := range strings.Split(set, ",") {
			uid, _ := strconv.Atoi(part)
			if f.stallBody[uint32(uid)] {
				return fetchStall
			}
			if f.failBody[uint32(uid)] {
				return fetchFail
			}
		}
		if f.unsolicited != "" {
			_, _ = io.WriteString(conn, f.unsolicited)
		}
	}
	for _, part := range strings.Split(set, ",") {
		uid, _ := strconv.Atoi(part)
		for sequence, message := range f.messages {
			if int(message.uid) != uid || f.silent[message.uid] {
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
	return fetchOK
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
	if message.ExternalID != imapExternalID("Recent-00@sender.test", 7001, 2) || message.Subject != "Recent 00" || message.FromAddress != "carol@sender.test" ||
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
	if len(fresh.Messages) != 1 || fresh.Messages[0].Subject != "Fresh" || fresh.Messages[0].ExternalID != imapExternalID("Fresh@sender.test", 0, 0) {
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
	// The message keeps its id across the change, so the resync stores no second copy.
	if len(restarted.Messages) != 1 || restarted.Messages[0].ExternalID != first.Messages[0].ExternalID {
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
	value, err := session.readValue(0, imapMaxMessageBytes)
	if err != nil || !value.oversized || value.text != nil {
		t.Fatalf("oversized literal = oversized %v, %d bytes, err %v", value.oversized, len(value.text), err)
	}
}

// TestIMAPCredentialsAreQuotedOrSentAsLiterals: quotes and backslashes are escaped, non-ASCII
// values go as literals, and a value with a line break never reaches the server.
func TestIMAPCredentialsAreQuotedOrSentAsLiterals(t *testing.T) {
	server := newFakeIMAPServer(t)
	source := server.source()
	quoted := server.credentials(`We"ird\user@imap.test`)
	if _, account, err := source.VerifyPassword(context.Background(), quoted); err != nil || account.Address != `we"ird\user@imap.test` {
		t.Fatalf("quoted login = %q, %v", account.Address, err)
	}
	verifyIMAP(t, source, server.credentials("dävid@imap.test"))
	commands, _, _ := server.served()
	if !slices.ContainsFunc(commands, func(c string) bool { return strings.Contains(c, `"We\"ird\\user@imap.test"`) }) {
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
		"224.0.0.1": false, "::ffff:10.0.0.1": false, "ff02::1": false,
		"64:ff9b::a00:1": false, "64:ff9b:1::1": false, "2002:a00:1::1": false, "::10.0.0.1": false,
		"240.0.0.1": false, "255.255.255.255": false, "198.18.0.1": false, "198.19.255.1": false,
		"192.0.0.8": false, "100.127.0.1": false, "100.128.0.1": true,
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

// TestIMAPIdentityIsDerivedFromTheLogin: the account is the username when it is an address,
// otherwise the username at the host; the member never names it.
func TestIMAPIdentityIsDerivedFromTheLogin(t *testing.T) {
	server := newFakeIMAPServer(t)
	source := server.source()
	if _, account, err := source.VerifyPassword(context.Background(), server.credentials("Dave")); err != nil ||
		account.Address != "dave@127.0.0.1" {
		t.Fatalf("bare username = %q, %v; want dave@127.0.0.1", account.Address, err)
	}
	named := server.credentials("Dave")
	named.Host = "IMAP.test"
	token, account, err := source.VerifyPassword(context.Background(), named)
	if err != nil || account.Address != "dave@imap.test" {
		t.Fatalf("bare username at a named host = %q, %v", account.Address, err)
	}
	var sealed imapCredentials
	if err = json.Unmarshal([]byte(token.RefreshToken), &sealed); err != nil || sealed.Host != "imap.test" {
		t.Fatalf("sealed host = %q, %v; want the lowercased host", sealed.Host, err)
	}
	other := named
	other.Host = "other.imap.test"
	otherToken, _, err := source.VerifyPassword(context.Background(), other)
	if err != nil {
		t.Fatalf("other host: %v", err)
	}
	if !source.SameMailbox(token, token) || source.SameMailbox(token, otherToken) ||
		source.SameMailbox(ExternalMailToken{RefreshToken: "garbage"}, token) {
		t.Fatal("SameMailbox does not compare the sealed hosts")
	}
}

// TestIMAPTransientRefusalsAreNotLostAuthorization: only a plain NO, or one naming the
// credentials, means the password is wrong; everything else is retried.
func TestIMAPTransientRefusalsAreNotLostAuthorization(t *testing.T) {
	for reply, auth := range map[string]bool{
		"NO [AUTHENTICATIONFAILED] bad": true, "NO [AUTHORIZATIONFAILED] no": true, "NO [EXPIRED] old": true,
		"NO wrong password": true, "NO [UNAVAILABLE] later": false, "NO [LIMIT] too many": false,
		"NO [INUSE] busy": false, "NO [SERVERBUG] oops": false, "NO [CONTACTADMIN] call": false,
		"BAD [AUTHENTICATIONFAILED] syntax": false,
	} {
		server := newFakeIMAPServer(t)
		server.configure(func(f *fakeIMAPServer) { f.loginReply = reply })
		_, _, err := server.source().VerifyPassword(context.Background(), server.credentials("dave@imap.test"))
		if err == nil || errors.Is(err, ErrExternalMailAuth) != auth {
			t.Fatalf("LOGIN answered %q = %v; want auth failure %v", reply, err, auth)
		}
	}
	server := newFakeIMAPServer(t)
	token := verifyIMAP(t, server.source(), server.credentials("dave@imap.test"))
	server.configure(func(f *fakeIMAPServer) { f.examineReply = "NO [UNAVAILABLE] INBOX is being rebuilt" })
	if _, err := server.source().Fetch(context.Background(), ExternalMailFetchRequest{Token: token, Now: time.Now()}); err == nil ||
		errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("EXAMINE refused = %v; want a retryable error", err)
	}
}

// TestIMAPOverallDeadline: a server that never answers holds a verification or a fetch no longer
// than the overall deadline, even when each command could wait longer.
func TestIMAPOverallDeadline(t *testing.T) {
	server := newFakeIMAPServer(t)
	token := verifyIMAP(t, server.source(), server.credentials("dave@imap.test"))
	server.configure(func(f *fakeIMAPServer) { f.stallLogin = true })
	source := server.source()
	source.commandTimeout, source.overallTimeout = 30*time.Second, 300*time.Millisecond
	started := time.Now()
	if _, _, err := source.VerifyPassword(context.Background(), server.credentials("dave@imap.test")); err == nil {
		t.Fatal("a tarpit verified")
	}
	if _, err := source.Fetch(context.Background(), ExternalMailFetchRequest{Token: token, Now: time.Now()}); err == nil {
		t.Fatal("a tarpit fetched")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("two calls to a tarpit took %v", elapsed)
	}
}

// captureLogs records the default logger's output for the test.
func captureLogs(t *testing.T) *strings.Builder {
	t.Helper()
	var output strings.Builder
	var mu sync.Mutex
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(lockedWriter{&mu, &output}, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// TestIMAPFailingMessageDoesNotBlockTheMailbox: a batch that fails or stalls is retried one UID
// at a time; the UID that still fails is skipped with a log line and the cursor moves past it, as
// it does past a UID the server never answers for.
func TestIMAPFailingMessageDoesNotBlockTheMailbox(t *testing.T) {
	logs := captureLogs(t)
	server := newFakeIMAPServer(t)
	now := time.Now()
	for i := range 8 {
		server.deliver(fmt.Sprintf("Message %d", i), now)
	}
	server.configure(func(f *fakeIMAPServer) {
		f.failBody[2], f.stallBody[4], f.silent[6] = true, true, true
	})
	source := server.source()
	source.commandTimeout = 500 * time.Millisecond
	token := verifyIMAP(t, source, server.credentials("dave@imap.test"))

	fetched := fetchIMAP(t, source, token, "", now)
	var subjects []string
	for _, message := range fetched.Messages {
		subjects = append(subjects, message.Subject)
	}
	want := []string{"Message 0", "Message 2", "Message 4", "Message 6", "Message 7"}
	if !slices.Equal(subjects, want) {
		t.Fatalf("subjects = %v; want %v", subjects, want)
	}
	var cursor imapCursor
	if err := json.Unmarshal([]byte(fetched.Cursor), &cursor); err != nil || cursor.LastUID != 8 || fetched.More {
		t.Fatalf("cursor = %s, more %v; want past every UID", fetched.Cursor, fetched.More)
	}
	text := logs.String()
	for _, line := range []string{"uid=2 reason=fetch_failed", "uid=4 reason=fetch_failed", "uid=6 reason=no_response"} {
		if !strings.Contains(text, "external_mail.imap_message_skipped") || !strings.Contains(text, line) {
			t.Fatalf("logs lack %q:\n%s", line, text)
		}
	}
	if strings.Contains(text, "Message 1") || strings.Contains(text, "carol@sender.test") {
		t.Fatalf("skip logs carry message content:\n%s", text)
	}
}

// TestIMAPUnsolicitedFetchIsDropped: a FETCH response about a UID nobody asked for is not
// returned, and its body is not kept.
func TestIMAPUnsolicitedFetchIsDropped(t *testing.T) {
	server := newFakeIMAPServer(t)
	now := time.Now()
	server.deliver("Asked", now)
	planted := "From: x@evil.test\r\nSubject: Planted\r\n\r\nplanted\r\n"
	server.configure(func(f *fakeIMAPServer) {
		f.unsolicited = fmt.Sprintf("* 99 FETCH (UID 999 RFC822.SIZE %d BODY[] {%d}\r\n%s)\r\n", len(planted), len(planted), planted)
	})
	source := server.source()
	token := verifyIMAP(t, source, server.credentials("dave@imap.test"))
	fetched := fetchIMAP(t, source, token, "", now)
	if len(fetched.Messages) != 1 || fetched.Messages[0].Subject != "Asked" {
		t.Fatalf("fetched = %+v; want only the requested message", fetched.Messages)
	}
}

// scriptedSession is a session over an in-memory pipe whose server side writes script.
func scriptedSession(t *testing.T, script string) *imapSession {
	const commandBudget = 64 << 10
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go func() {
		reader := bufio.NewReader(server)
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		_, _ = io.WriteString(server, script)
		_, _ = io.Copy(io.Discard, reader)
	}()
	budget := &imapBudgetReader{conn: client, command: commandBudget, session: 4 * commandBudget}
	return &imapSession{
		conn: client, reader: bufio.NewReaderSize(budget, 32<<10), budget: budget,
		commandBudget: commandBudget, timeout: 5 * time.Second,
	}
}

// TestIMAPByteBudgets: a command whose answer exceeds its budget (64 KiB here) fails, discarded
// bytes included, and a literal larger than the remaining budget is refused before it is allocated.
func TestIMAPByteBudgets(t *testing.T) {
	const budget = 64 << 10
	flood := scriptedSession(t, fmt.Sprintf("* 9 FETCH (UID 99 BODY[] {%d}\r\n%s)\r\nZ1 OK done\r\n",
		budget*4, strings.Repeat("x", budget*4)))
	if _, err := flood.fetch([]uint32{1}, "(UID BODY.PEEK[])"); !errors.Is(err, errIMAPBudget) {
		t.Fatalf("an unsolicited flood = %v; want the budget refusal", err)
	}
	announced := scriptedSession(t, "* 1 FETCH (UID 1 BODY[] {4000000}\r\n")
	if _, err := announced.fetch([]uint32{1}, "(UID BODY.PEEK[])"); !errors.Is(err, errIMAPBudget) {
		t.Fatalf("a literal above the budget = %v; want the budget refusal", err)
	}
	lines := scriptedSession(t, strings.Repeat("* OK "+strings.Repeat("y", 1000)+"\r\n", 200)+"Z1 OK done\r\n")
	if _, err := lines.search("ALL"); !errors.Is(err, errIMAPBudget) {
		t.Fatalf("a flood of lines = %v; want the budget refusal", err)
	}
	fits := scriptedSession(t, "* 1 FETCH (UID 1 RFC822.SIZE 5 BODY[] {5}\r\nhello)\r\nZ1 OK done\r\n")
	read, err := fits.fetch([]uint32{1}, "(UID RFC822.SIZE BODY.PEEK[])")
	if err != nil || string(read[1].body) != "hello" {
		t.Fatalf("a small answer = %+v, %v", read, err)
	}
}

func TestIMAPExternalIDIsStable(t *testing.T) {
	if imapExternalID("<A@B>", 1, 2) == imapExternalID("", 1, 2) || imapExternalID("Id@Host", 1, 2) != imapExternalID("id@host", 9, 9) {
		t.Fatal("the Message-ID id depends on the UID or ignores case")
	}
	if id := imapExternalID("x@y", 1, 2); !strings.HasPrefix(id, "m:") || len(id) != 42 {
		t.Fatalf("id = %q", id)
	}
	if id := imapExternalID("", 7, 9); id != "u:7:9" {
		t.Fatalf("fallback id = %q", id)
	}
}
