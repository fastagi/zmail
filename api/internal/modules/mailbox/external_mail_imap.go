package mailbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/mail"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// ExternalMailProviderIMAP is the generic IMAP connector: any provider that offers IMAP over TLS
// and app passwords (iCloud, Yahoo, Fastmail, QQ, 163, a self-hosted server).
const ExternalMailProviderIMAP domain.ExternalMailProvider = "imap"

const (
	// imapPort is the implicit-TLS IMAP port; plaintext IMAP and STARTTLS are refused.
	imapPort = 993
	// imapPageSize is how many messages one fetch returns.
	imapPageSize = 25
	// imapBodyBatch is how many bodies one UID FETCH command reads, bounding the memory of a page.
	imapBodyBatch = 5
	// imapMaxMessageBytes skips messages larger than the inbound limit.
	imapMaxMessageBytes = MaxInboundBytes
	// imapMaxLineBytes bounds one response line, a long UID SEARCH result included.
	imapMaxLineBytes = 4 << 20
	// imapMaxSmallLiteral bounds a literal outside a message body.
	imapMaxSmallLiteral = 64 << 10
	imapDialTimeout     = 15 * time.Second
	imapCommandTimeout  = 60 * time.Second
	// imapMaxCredentialLength bounds each credential field.
	imapMaxCredentialLength = 320
)

var (
	// ErrExternalMailServerUnreachable means the mail server could not be reached over TLS: the
	// host does not resolve, is refused as a private address, does not answer, or presents a
	// certificate that does not verify.
	ErrExternalMailServerUnreachable = errors.New("external mail server unreachable")
)

func init() {
	registerExternalMailConnector(func(cfg config.ExternalMailConfig) (ExternalMailConnector, bool) {
		if !cfg.Provides(config.ExternalMailProviderIMAP) {
			return ExternalMailConnector{}, false
		}
		return ExternalMailConnector{
			ID: ExternalMailProviderIMAP, AuthKind: ExternalMailAuthPassword,
			Capabilities: ExternalMailCapabilities{Read: true}, Source: newIMAPSource(),
		}, true
	})
}

// ExternalMailPasswordCredentials are what a member types in to bind a password connector.
type ExternalMailPasswordCredentials struct {
	Host     string
	Port     int
	Username string
	Password string
	// AccountAddress is the bound address; empty takes the username when it is an address.
	AccountAddress string
}

// ExternalMailPasswordVerifier is implemented by sources bound with a password: it signs in once
// and returns the grant to store and the account it reads.
type ExternalMailPasswordVerifier interface {
	VerifyPassword(ctx context.Context, credentials ExternalMailPasswordCredentials) (ExternalMailToken, ExternalMailAccount, error)
}

// imapCredentials is the sealed secret of an IMAP connection.
type imapCredentials struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// imapCursor is the position in INBOX.
type imapCursor struct {
	UIDValidity uint32 `json:"uidvalidity"`
	LastUID     uint32 `json:"last_uid"`
}

// imapNetworkPolicy decides which servers the source may reach. The zero value is production:
// port 993 on public addresses, certificates verified against the system roots.
type imapNetworkPolicy struct {
	// AllowPrivate lets tests reach loopback and private addresses.
	AllowPrivate bool
	// AllowAnyPort lets tests use the port of an in-process server.
	AllowAnyPort bool
	// RootCAs replaces the system roots, so tests trust a self-signed server.
	RootCAs *x509.CertPool
}

// imapResolver resolves a host name; net.DefaultResolver in production.
type imapResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// imapSource reads INBOX of an IMAP account over implicit TLS. It is read-only: messages are
// fetched with BODY.PEEK so their \Seen flag stays as it was.
type imapSource struct {
	policy         imapNetworkPolicy
	resolver       imapResolver
	dialTimeout    time.Duration
	commandTimeout time.Duration
	pageSize       int
}

var (
	_ ExternalMailSource           = (*imapSource)(nil)
	_ ExternalMailPasswordVerifier = (*imapSource)(nil)
)

func newIMAPSource() *imapSource {
	return &imapSource{
		resolver: net.DefaultResolver, dialTimeout: imapDialTimeout, commandTimeout: imapCommandTimeout,
		pageSize: imapPageSize,
	}
}

// AuthURL is empty: IMAP binds with a password, never through a sign-in redirect.
func (*imapSource) AuthURL(ExternalMailAuthRequest) string { return "" }

// Exchange is unused: there is no authorization code.
func (*imapSource) Exchange(context.Context, ExternalMailCodeExchange) (ExternalMailToken, ExternalMailAccount, error) {
	return ExternalMailToken{}, ExternalMailAccount{}, ErrExternalMailUnavailable
}

// Revoke has nothing to hand back: the member revokes the app password at the provider.
func (*imapSource) Revoke(context.Context, ExternalMailToken) error { return nil }

// VerifyPassword signs in and opens INBOX once, then returns the sealed-to-be credentials.
func (s *imapSource) VerifyPassword(ctx context.Context, input ExternalMailPasswordCredentials) (ExternalMailToken, ExternalMailAccount, error) {
	credentials := imapCredentials{
		Host: strings.ToLower(strings.TrimSpace(input.Host)), Port: input.Port,
		Username: input.Username, Password: input.Password,
	}
	if credentials.Port == 0 {
		credentials.Port = imapPort
	}
	if err := credentials.validate(); err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	address := strings.TrimSpace(input.AccountAddress)
	if address == "" {
		address = strings.TrimSpace(input.Username)
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Name != "" || parsed.Address != address {
		return ExternalMailToken{}, ExternalMailAccount{}, ErrExternalMailAuthorizationInvalid
	}
	session, err := s.open(ctx, credentials)
	if err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	defer session.close()
	if _, err = session.selectInbox(); err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	sealed, err := json.Marshal(credentials)
	if err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	return ExternalMailToken{RefreshToken: string(sealed)}, ExternalMailAccount{Address: strings.ToLower(address)}, nil
}

// Fetch reads one page of INBOX after the cursor. An empty cursor starts the backfill at the
// request's window; a changed UIDVALIDITY expires the cursor.
func (s *imapSource) Fetch(ctx context.Context, request ExternalMailFetchRequest) (ExternalMailFetch, error) {
	var credentials imapCredentials
	if err := json.Unmarshal([]byte(request.Token.RefreshToken), &credentials); err != nil || credentials.validate() != nil {
		return ExternalMailFetch{}, fmt.Errorf("%w: unreadable IMAP credentials", ErrExternalMailAuth)
	}
	var cursor imapCursor
	if request.Cursor != "" {
		if err := json.Unmarshal([]byte(request.Cursor), &cursor); err != nil || cursor.UIDValidity == 0 {
			return ExternalMailFetch{}, ErrExternalMailCursorExpired
		}
	}
	session, err := s.open(ctx, credentials)
	if err != nil {
		return ExternalMailFetch{}, err
	}
	defer session.close()
	inbox, err := session.selectInbox()
	if err != nil {
		return ExternalMailFetch{}, err
	}
	if request.Cursor != "" && cursor.UIDValidity != inbox.uidValidity {
		return ExternalMailFetch{}, ErrExternalMailCursorExpired
	}

	var uids []uint32
	switch {
	case request.Cursor == "":
		days := request.BackfillDays
		if days <= 0 {
			days = domain.DefaultExternalMailBackfillDays
		}
		now := request.Now
		if now.IsZero() {
			now = time.Now()
		}
		uids, err = session.search("SINCE " + now.UTC().AddDate(0, 0, -days).Format("2-Jan-2006"))
	case inbox.uidNext == 0 || cursor.LastUID+1 < inbox.uidNext:
		uids, err = session.search(fmt.Sprintf("UID %d:*", cursor.LastUID+1))
	}
	if err != nil {
		return ExternalMailFetch{}, err
	}
	// "n:*" matches the highest UID even below n, so anything at or below the cursor is dropped.
	uids = slices.DeleteFunc(uids, func(uid uint32) bool { return uid <= cursor.LastUID })
	slices.Sort(uids)
	uids = slices.Compact(uids)

	pageSize := s.pageSize
	if pageSize <= 0 {
		pageSize = imapPageSize
	}
	page := uids[:min(len(uids), pageSize)]
	next := imapCursor{UIDValidity: inbox.uidValidity, LastUID: cursor.LastUID}
	if len(page) > 0 {
		next.LastUID = page[len(page)-1]
	}
	more := len(page) < len(uids)
	if !more && inbox.uidNext > 0 && inbox.uidNext-1 > next.LastUID {
		// Everything in the window was read: later fetches start at mail newer than this one.
		next.LastUID = inbox.uidNext - 1
	}
	messages, err := session.fetchMessages(ctx, page)
	if err != nil {
		return ExternalMailFetch{}, err
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return ExternalMailFetch{}, err
	}
	for i := range messages {
		messages[i].ExternalID = fmt.Sprintf("%d:%s", inbox.uidValidity, messages[i].ExternalID)
	}
	return ExternalMailFetch{Messages: messages, Cursor: string(encoded), More: more}, nil
}

// validate refuses credentials that cannot be sent safely: control characters would let a value
// end the command and start another.
func (c imapCredentials) validate() error {
	if c.Host == "" || len(c.Host) > 253 || c.Port < 1 || c.Port > 65535 {
		return ErrExternalMailAuthorizationInvalid
	}
	for _, value := range []string{c.Host, c.Username, c.Password} {
		if value == "" || len(value) > imapMaxCredentialLength {
			return ErrExternalMailAuthorizationInvalid
		}
		for _, r := range value {
			if r < 0x20 || r == 0x7f {
				return ErrExternalMailAuthorizationInvalid
			}
		}
	}
	if strings.ContainsAny(c.Host, " /\\@[]") {
		return ErrExternalMailAuthorizationInvalid
	}
	return nil
}

// allowedAddress refuses addresses inside the instance's own network.
func (p imapNetworkPolicy) allowedAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if p.AllowPrivate {
		return addr.IsValid()
	}
	sharedSpace := netip.MustParsePrefix("100.64.0.0/10")
	thisNetwork := netip.MustParsePrefix("0.0.0.0/8")
	return addr.IsValid() && addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() &&
		!addr.IsLinkLocalUnicast() && !addr.IsUnspecified() && !sharedSpace.Contains(addr) && !thisNetwork.Contains(addr)
}

// dial resolves the host, refuses any private address it resolves to, and connects over TLS to
// the address it checked, so a second resolution cannot point elsewhere.
func (s *imapSource) dial(ctx context.Context, credentials imapCredentials) (*tls.Conn, error) {
	if credentials.Port != imapPort && !s.policy.AllowAnyPort {
		return nil, fmt.Errorf("%w: only port %d (IMAP over TLS) is allowed", ErrExternalMailServerUnreachable, imapPort)
	}
	ctx, cancel := context.WithTimeout(ctx, s.dialTimeout)
	defer cancel()
	var addresses []netip.Addr
	if literal, err := netip.ParseAddr(credentials.Host); err == nil {
		addresses = []netip.Addr{literal}
	} else {
		resolved, err := s.resolver.LookupNetIP(ctx, "ip", credentials.Host)
		if err != nil || len(resolved) == 0 {
			return nil, fmt.Errorf("%w: host does not resolve", ErrExternalMailServerUnreachable)
		}
		addresses = resolved
	}
	for _, addr := range addresses {
		if !s.policy.allowedAddress(addr) {
			return nil, fmt.Errorf("%w: host resolves to a private address", ErrExternalMailServerUnreachable)
		}
	}
	dialer := &net.Dialer{Timeout: s.dialTimeout}
	var lastErr error
	for _, addr := range addresses {
		raw, err := dialer.DialContext(ctx, "tcp", netip.AddrPortFrom(addr.Unmap(), uint16(credentials.Port)).String())
		if err != nil {
			lastErr = err
			continue
		}
		conn := tls.Client(raw, &tls.Config{
			ServerName: credentials.Host, MinVersion: tls.VersionTLS12, RootCAs: s.policy.RootCAs,
		})
		if err := conn.HandshakeContext(ctx); err != nil {
			closeQuietly(raw)
			lastErr = err
			continue
		}
		return conn, nil
	}
	return nil, fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, lastErr)
}

// imapSession is one signed-in connection.
type imapSession struct {
	conn    *tls.Conn
	reader  *bufio.Reader
	timeout time.Duration
	tag     int
	stop    func() bool
}

// open connects, reads the greeting, and signs in. A NO to LOGIN is ErrExternalMailAuth.
func (s *imapSource) open(ctx context.Context, credentials imapCredentials) (*imapSession, error) {
	conn, err := s.dial(ctx, credentials)
	if err != nil {
		return nil, err
	}
	session := &imapSession{conn: conn, reader: bufio.NewReaderSize(conn, 32<<10), timeout: s.commandTimeout}
	// A canceled context closes the connection, which ends any read in progress.
	session.stop = context.AfterFunc(ctx, func() { closeQuietly(conn) })
	greeting, err := session.readGreeting()
	if err != nil {
		session.close()
		return nil, fmt.Errorf("%w: no greeting: %v", ErrExternalMailServerUnreachable, err)
	}
	if !strings.HasPrefix(greeting, "* OK") {
		session.close()
		return nil, fmt.Errorf("%w: server refused the connection", ErrExternalMailServerUnreachable)
	}
	if err := session.login(credentials.Username, credentials.Password); err != nil {
		session.close()
		return nil, err
	}
	return session, nil
}

// readGreeting reads the server's first line within one command timeout.
func (s *imapSession) readGreeting() (string, error) {
	if err := s.conn.SetDeadline(time.Now().Add(s.timeout)); err != nil {
		return "", err
	}
	return s.readLine()
}

// close says goodbye on a best-effort basis and closes the connection.
func (s *imapSession) close() {
	if s.stop != nil {
		s.stop()
	}
	s.timeout = 2 * time.Second
	if err := s.send("LOGOUT"); err == nil {
		if _, err := s.finish(imapHandlers{}); err != nil {
			slog.Debug("external_mail.imap_logout_failed", slog.String("error", err.Error()))
		}
	}
	closeQuietly(s.conn)
}

// closeQuietly closes a connection whose close error changes nothing for the caller.
func closeQuietly(closer io.Closer) {
	if err := closer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		slog.Debug("external_mail.imap_close_failed", slog.String("error", err.Error()))
	}
}

// send writes a command under the next tag, which finish then waits for.
func (s *imapSession) send(command string) error {
	s.tag++
	if err := s.conn.SetDeadline(time.Now().Add(s.timeout)); err != nil {
		return err
	}
	_, err := io.WriteString(s.conn, "Z"+strconv.Itoa(s.tag)+" "+command+"\r\n")
	return err
}

// skip consumes n bytes the reader already peeked at.
func (s *imapSession) skip(n int) error {
	_, err := s.reader.Discard(n)
	return err
}

// imapStatus is the tagged completion of a command.
type imapStatus struct {
	ok   bool
	text string
}

// imapHandlers receive the untagged responses of a command: FETCH responses parsed, any other
// response as its line after "* ".
type imapHandlers struct {
	line  func(line string) error
	fetch func(fetched imapFetched)
}

// finish reads responses until the tagged completion of the last command.
func (s *imapSession) finish(handlers imapHandlers) (imapStatus, error) {
	tag := "Z" + strconv.Itoa(s.tag)
	for {
		peek, err := s.reader.Peek(2)
		if err != nil {
			return imapStatus{}, err
		}
		if string(peek) == "* " {
			if err = s.skip(2); err != nil {
				return imapStatus{}, err
			}
			if err = s.readUntagged(handlers); err != nil {
				return imapStatus{}, err
			}
			continue
		}
		line, err := s.readLine()
		if err != nil {
			return imapStatus{}, err
		}
		rest, tagged := strings.CutPrefix(line, tag+" ")
		if !tagged {
			continue
		}
		status, text, _ := strings.Cut(rest, " ")
		switch strings.ToUpper(status) {
		case "OK":
			return imapStatus{ok: true, text: text}, nil
		case "NO", "BAD":
			return imapStatus{text: text}, nil
		}
		return imapStatus{}, fmt.Errorf("imap: unexpected completion %q", status)
	}
}

// readUntagged reads one untagged response after its "* ".
func (s *imapSession) readUntagged(handlers imapHandlers) error {
	first, err := s.readWord()
	if err != nil {
		return err
	}
	second := ""
	if next, peekErr := s.reader.Peek(1); peekErr == nil && next[0] == ' ' {
		if err = s.skip(1); err != nil {
			return err
		}
		if second, err = s.readWord(); err != nil {
			return err
		}
	}
	if _, numeric := parseUID(first); numeric && strings.EqualFold(second, "FETCH") {
		fetched, fetchErr := s.readFetch()
		if fetchErr != nil {
			return fetchErr
		}
		if handlers.fetch != nil {
			handlers.fetch(fetched)
		}
		return nil
	}
	rest, err := s.readLine()
	if err != nil {
		return err
	}
	if handlers.line != nil {
		return handlers.line(strings.TrimSpace(first + " " + second + rest))
	}
	return nil
}

// readWord reads up to the next space or line end, without consuming it.
func (s *imapSession) readWord() (string, error) {
	var word []byte
	for len(word) < 1024 {
		next, err := s.reader.Peek(1)
		if err != nil {
			return "", err
		}
		if next[0] == ' ' || next[0] == '\r' || next[0] == '\n' {
			break
		}
		word = append(word, next[0])
		if err := s.skip(1); err != nil {
			return "", err
		}
	}
	return string(word), nil
}

func parseUID(value string) (uint32, bool) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	return uint32(parsed), err == nil
}

// readLine reads one response line without its CRLF. A line that announces a literal has the
// literal inlined when it is small and dropped otherwise; lines outside FETCH never carry mail.
func (s *imapSession) readLine() (string, error) {
	var line []byte
	for {
		part, err := s.readRawLine(imapMaxLineBytes - len(line))
		if err != nil {
			return "", err
		}
		line = append(line, part...)
		size, ok := trailingLiteral(part)
		if !ok {
			return string(line), nil
		}
		if size > imapMaxSmallLiteral {
			if _, err := io.CopyN(io.Discard, s.reader, size); err != nil {
				return "", err
			}
			continue
		}
		literal := make([]byte, size)
		if _, err := io.ReadFull(s.reader, literal); err != nil {
			return "", err
		}
		line = append(line, literal...)
	}
}

// readRawLine reads up to CRLF, refusing lines longer than limit.
func (s *imapSession) readRawLine(limit int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := s.reader.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > limit {
			return nil, errors.New("imap: response line too long")
		}
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
	return bytes.TrimRight(line, "\r\n"), nil
}

// trailingLiteral reports the size of a "{n}" at the end of a line.
func trailingLiteral(line []byte) (int64, bool) {
	if !bytes.HasSuffix(line, []byte("}")) {
		return 0, false
	}
	open := bytes.LastIndexByte(line, '{')
	if open < 0 {
		return 0, false
	}
	size, err := strconv.ParseInt(strings.TrimSuffix(string(line[open+1:len(line)-1]), "+"), 10, 64)
	if err != nil || size < 0 {
		return 0, false
	}
	return size, true
}

// login signs in. Printable ASCII values go as quoted strings; any other value goes as a literal
// the server must invite. Control characters were refused before.
func (s *imapSession) login(username, password string) error {
	s.tag++
	tag := "Z" + strconv.Itoa(s.tag)
	if err := s.conn.SetDeadline(time.Now().Add(s.timeout)); err != nil {
		return fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
	}
	parts := []string{tag + " LOGIN "}
	for i, value := range []string{username, password} {
		if i > 0 {
			parts[len(parts)-1] += " "
		}
		if quotable(value) {
			parts[len(parts)-1] += quoteIMAP(value)
			continue
		}
		parts[len(parts)-1] += "{" + strconv.Itoa(len(value)) + "}\r\n"
		parts = append(parts, value)
	}
	parts[len(parts)-1] += "\r\n"
	for i, part := range parts {
		if i > 0 {
			// The server invites each literal with a continuation, or refuses the command.
			line, err := s.readLine()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
			}
			if !strings.HasPrefix(line, "+") {
				return ErrExternalMailAuth
			}
		}
		if _, err := io.WriteString(s.conn, part); err != nil {
			return fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
		}
	}
	status, err := s.finish(imapHandlers{})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
	}
	if !status.ok {
		return ErrExternalMailAuth
	}
	return nil
}

// quotable reports whether a value fits a quoted string: printable ASCII only.
func quotable(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func quoteIMAP(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// imapInbox is what SELECT reports about INBOX.
type imapInbox struct {
	uidValidity uint32
	uidNext     uint32
}

// selectInbox opens INBOX with EXAMINE, the read-only form of SELECT, so syncing never changes
// the account: no \Recent flag is consumed, and messages are read with BODY.PEEK.
func (s *imapSession) selectInbox() (imapInbox, error) {
	if err := s.send("EXAMINE INBOX"); err != nil {
		return imapInbox{}, err
	}
	var inbox imapInbox
	status, err := s.finish(imapHandlers{line: func(line string) error {
		if value, ok := responseCode(line, "UIDVALIDITY"); ok {
			inbox.uidValidity = value
		}
		if value, ok := responseCode(line, "UIDNEXT"); ok {
			inbox.uidNext = value
		}
		return nil
	}})
	if err != nil {
		return imapInbox{}, err
	}
	if !status.ok {
		return imapInbox{}, fmt.Errorf("%w: INBOX cannot be opened", ErrExternalMailAuth)
	}
	if inbox.uidValidity == 0 {
		return imapInbox{}, errors.New("imap: INBOX reports no UIDVALIDITY")
	}
	return inbox, nil
}

// responseCode reads a numeric response code such as "OK [UIDVALIDITY 3857529045]".
func responseCode(line, name string) (uint32, bool) {
	index := strings.Index(strings.ToUpper(line), "["+name+" ")
	if index < 0 {
		return 0, false
	}
	rest := line[index+len(name)+2:]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(rest[:end]), 10, 32)
	return uint32(value), err == nil
}

// search runs UID SEARCH with the criteria.
func (s *imapSession) search(criteria string) ([]uint32, error) {
	if err := s.send("UID SEARCH " + criteria); err != nil {
		return nil, err
	}
	var uids []uint32
	status, err := s.finish(imapHandlers{line: func(line string) error {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.EqualFold(fields[0], "SEARCH") {
			return nil
		}
		for _, field := range fields[1:] {
			uid, ok := parseUID(field)
			if !ok {
				return fmt.Errorf("imap: malformed SEARCH result %q", field)
			}
			uids = append(uids, uid)
		}
		return nil
	}})
	if err != nil {
		return nil, err
	}
	if !status.ok {
		return nil, fmt.Errorf("imap: UID SEARCH refused: %s", status.text)
	}
	return uids, nil
}

// imapFetched is one FETCH response.
type imapFetched struct {
	uid          uint32
	size         int64
	internalDate time.Time
	body         []byte
	// oversized means the body literal exceeded the limit and was discarded.
	oversized bool
}

// fetchMessages reads the messages of the UIDs in small batches. Sizes come first, so a message
// above the inbound limit is never downloaded; it is skipped like any message that does not
// parse.
func (s *imapSession) fetchMessages(ctx context.Context, uids []uint32) ([]ExternalMessage, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	sizes, err := s.fetch(uids, "(UID RFC822.SIZE)")
	if err != nil {
		return nil, err
	}
	var wanted []uint32
	for _, uid := range uids {
		if fetched, ok := sizes[uid]; ok && fetched.size <= imapMaxMessageBytes {
			wanted = append(wanted, uid)
		} else if ok {
			slog.InfoContext(ctx, "external_mail.imap_message_skipped", slog.Uint64("uid", uint64(uid)),
				slog.Int64("size", fetched.size), slog.String("reason", "oversized"))
		}
	}
	messages := make([]ExternalMessage, 0, len(wanted))
	for start := 0; start < len(wanted); start += imapBodyBatch {
		batch := wanted[start:min(len(wanted), start+imapBodyBatch)]
		bodies, err := s.fetch(batch, "(UID RFC822.SIZE INTERNALDATE BODY.PEEK[])")
		if err != nil {
			return nil, err
		}
		for _, uid := range batch {
			fetched, ok := bodies[uid]
			if !ok || fetched.oversized || fetched.body == nil {
				continue
			}
			parsed, err := parseMail(fetched.body)
			if err != nil {
				slog.InfoContext(ctx, "external_mail.imap_message_skipped", slog.Uint64("uid", uint64(uid)),
					slog.String("reason", "unparseable"))
				continue
			}
			messages = append(messages, ExternalMessage{
				ExternalID: strconv.FormatUint(uint64(uid), 10), FromAddress: parsed.FromAddress, FromName: parsed.FromName,
				To: parsed.To, Cc: parsed.Cc, Subject: parsed.Subject, TextBody: parsed.TextBody, HTMLBody: parsed.HTMLBody,
				RFCMessageID: parsed.MessageID, InReplyTo: parsed.InReplyTo, References: parsed.References,
				ReceivedAt: fetched.internalDate,
			})
		}
	}
	return messages, nil
}

// fetch runs UID FETCH for the UIDs and returns the responses by UID.
func (s *imapSession) fetch(uids []uint32, items string) (map[uint32]imapFetched, error) {
	set := make([]string, len(uids))
	for i, uid := range uids {
		set[i] = strconv.FormatUint(uint64(uid), 10)
	}
	if err := s.send("UID FETCH " + strings.Join(set, ",") + " " + items); err != nil {
		return nil, err
	}
	results := map[uint32]imapFetched{}
	status, err := s.finish(imapHandlers{fetch: func(fetched imapFetched) {
		if fetched.uid != 0 {
			results[fetched.uid] = fetched
		}
	}})
	if err != nil {
		return nil, err
	}
	if !status.ok {
		return nil, fmt.Errorf("imap: UID FETCH refused: %s", status.text)
	}
	return results, nil
}

// readFetch reads the "(...)" of a FETCH response and the line end after it.
func (s *imapSession) readFetch() (imapFetched, error) {
	value, err := s.readValue(0)
	if err != nil {
		return imapFetched{}, err
	}
	if _, err := s.readRawLine(imapMaxLineBytes); err != nil {
		return imapFetched{}, err
	}
	var fetched imapFetched
	for i := 0; i+1 < len(value.list); i += 2 {
		name, item := strings.ToUpper(value.list[i].atom), value.list[i+1]
		switch {
		case name == "UID":
			fetched.uid, _ = parseUID(item.atom)
		case name == "RFC822.SIZE":
			size, err := strconv.ParseInt(item.atom, 10, 64)
			if err != nil || size < 0 {
				// A size that does not parse is treated as too large to download.
				size = math.MaxInt64
			}
			fetched.size = size
		case name == "INTERNALDATE":
			// A date that does not parse stays zero, and the sync time is used instead.
			if date, err := time.Parse("2-Jan-2006 15:04:05 -0700", strings.TrimSpace(string(item.text))); err == nil {
				fetched.internalDate = date
			}
		case strings.HasPrefix(name, "BODY[") || name == "RFC822":
			fetched.body, fetched.oversized = item.text, item.oversized
		}
	}
	return fetched, nil
}

// imapValue is one parsed response value: an atom, a string, or a list.
type imapValue struct {
	atom      string
	text      []byte
	list      []imapValue
	oversized bool
}

// readValue reads one value from the stream. String literals above the message limit are
// discarded and marked oversized.
func (s *imapSession) readValue(depth int) (imapValue, error) {
	if depth > 8 {
		return imapValue{}, errors.New("imap: response nests too deeply")
	}
	if err := s.skipSpaces(); err != nil {
		return imapValue{}, err
	}
	first, err := s.reader.ReadByte()
	if err != nil {
		return imapValue{}, err
	}
	switch first {
	case '(':
		var value imapValue
		for {
			if err := s.skipSpaces(); err != nil {
				return imapValue{}, err
			}
			next, err := s.reader.Peek(1)
			if err != nil {
				return imapValue{}, err
			}
			if next[0] == ')' {
				if err = s.skip(1); err != nil {
					return imapValue{}, err
				}
				if value.list == nil {
					value.list = []imapValue{}
				}
				return value, nil
			}
			if len(value.list) > 64 {
				return imapValue{}, errors.New("imap: response list too long")
			}
			item, err := s.readValue(depth + 1)
			if err != nil {
				return imapValue{}, err
			}
			value.list = append(value.list, item)
		}
	case '"':
		var text []byte
		for {
			b, err := s.reader.ReadByte()
			if err != nil {
				return imapValue{}, err
			}
			switch b {
			case '"':
				return imapValue{text: text}, nil
			case '\\':
				if b, err = s.reader.ReadByte(); err != nil {
					return imapValue{}, err
				}
			case '\r', '\n':
				return imapValue{}, errors.New("imap: line break in a quoted string")
			}
			if len(text) >= imapMaxSmallLiteral {
				return imapValue{}, errors.New("imap: quoted string too long")
			}
			text = append(text, b)
		}
	case '{':
		header, err := s.reader.ReadString('}')
		if err != nil || len(header) > 24 {
			return imapValue{}, errors.New("imap: malformed literal")
		}
		size, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSuffix(header, "}"), "+"), 10, 64)
		if err != nil || size < 0 {
			return imapValue{}, errors.New("imap: malformed literal size")
		}
		if crlf, err := s.readRawLine(2); err != nil || len(crlf) != 0 {
			return imapValue{}, errors.New("imap: malformed literal")
		}
		if size > imapMaxMessageBytes {
			if _, err := io.CopyN(io.Discard, s.reader, size); err != nil {
				return imapValue{}, err
			}
			return imapValue{oversized: true}, nil
		}
		text := make([]byte, size)
		if _, err := io.ReadFull(s.reader, text); err != nil {
			return imapValue{}, err
		}
		return imapValue{text: text}, nil
	default:
		atom := []byte{first}
		brackets := 0
		if first == '[' {
			brackets++
		}
		for {
			next, err := s.reader.Peek(1)
			if err != nil {
				return imapValue{}, err
			}
			b := next[0]
			if b == '\r' || b == '\n' || (brackets == 0 && (b == ' ' || b == '(' || b == ')')) {
				break
			}
			switch b {
			case '[':
				brackets++
			case ']':
				brackets--
			}
			if len(atom) >= 1024 {
				return imapValue{}, errors.New("imap: atom too long")
			}
			atom = append(atom, b)
			if err := s.skip(1); err != nil {
				return imapValue{}, err
			}
		}
		if strings.EqualFold(string(atom), "NIL") {
			return imapValue{}, nil
		}
		return imapValue{atom: string(atom)}, nil
	}
}

func (s *imapSession) skipSpaces() error {
	for {
		next, err := s.reader.Peek(1)
		if err != nil {
			return err
		}
		if next[0] != ' ' {
			return nil
		}
		if err := s.skip(1); err != nil {
			return err
		}
	}
}
