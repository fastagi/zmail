package mailbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
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
	// imapBodyBatch is how many bodies one UID FETCH command reads.
	imapBodyBatch = 5
	// imapBatchBytes bounds the announced sizes of the bodies one UID FETCH command reads; a
	// single message up to the inbound limit always fits.
	imapBatchBytes = 6 << 20
	// imapPageBytes bounds the announced sizes of the bodies one fetch reads; a page above it is
	// cut short and continues in the next fetch.
	imapPageBytes = 40 << 20
	// imapCommandBudget bounds every byte the server sends in answer to one command, lines and
	// literals, kept or discarded.
	imapCommandBudget = 8 << 20
	// imapSessionBudget bounds every byte one connection receives.
	imapSessionBudget = 48 << 20
	// imapMaxMessageBytes skips messages larger than the inbound limit.
	imapMaxMessageBytes = MaxInboundBytes
	// imapMaxLineBytes bounds one response line, a long UID SEARCH result included.
	imapMaxLineBytes = 4 << 20
	// imapMaxSmallLiteral bounds a literal outside a message body.
	imapMaxSmallLiteral = 64 << 10
	imapDialTimeout     = 10 * time.Second
	imapCommandTimeout  = 15 * time.Second
	// imapOverallTimeout bounds one fetch or one verification, dial and every command included,
	// so a slow or hostile server cannot hold the sync worker.
	imapOverallTimeout = 45 * time.Second
	// imapMaxCredentialLength bounds each credential field.
	imapMaxCredentialLength = 320
)

var (
	// ErrExternalMailServerUnreachable means the mail server could not be reached over TLS: the
	// host does not resolve, is refused as a private address, does not answer, or presents a
	// certificate that does not verify.
	ErrExternalMailServerUnreachable = errors.New("external mail server unreachable")
	// errIMAPBudget means the server sent more than a command or a connection may receive.
	errIMAPBudget = errors.New("imap: response exceeds its byte budget")
)

func init() {
	registerExternalMailConnector(func(cfg config.ExternalMailConfig) (ExternalMailConnector, bool) {
		if !cfg.Provides(config.ExternalMailProviderIMAP) {
			return ExternalMailConnector{}, false
		}
		return ExternalMailConnector{
			ID: ExternalMailProviderIMAP, AuthKind: ExternalMailAuthPassword,
			Capabilities: ExternalMailCapabilities{Read: true, Send: true}, Source: newIMAPSource(),
		}, true
	})
}

// ExternalMailPasswordCredentials are what a member types in to bind a password connector.
type ExternalMailPasswordCredentials struct {
	Host     string
	Port     int
	Username string
	Password string
	// SMTPHost, when set, is the account's outgoing server: the same login is verified there too, and
	// the connected mailbox then sends through it. SMTPPort is 465 (implicit TLS, the default) or 587
	// (STARTTLS).
	SMTPHost string
	SMTPPort int
}

// ExternalMailPasswordVerifier is implemented by sources bound with a password: it signs in once
// and returns the grant to store and the account it reads. The account is derived from the
// credentials by the source, never taken from the member.
type ExternalMailPasswordVerifier interface {
	VerifyPassword(ctx context.Context, credentials ExternalMailPasswordCredentials) (ExternalMailToken, ExternalMailAccount, error)
}

// ExternalMailGrantComparer is implemented by sources whose grant names the server it reads. A
// rebind with a grant for another server starts the connection's backfill over, since the old
// cursor means nothing there.
type ExternalMailGrantComparer interface {
	SameMailbox(previous, next ExternalMailToken) bool
}

// imapCredentials is the sealed secret of an IMAP connection.
type imapCredentials struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	// SMTPHost and SMTPPort name the outgoing server verified at binding; empty when the account
	// only syncs.
	SMTPHost string `json:"smtp_host,omitempty"`
	SMTPPort int    `json:"smtp_port,omitempty"`
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

// imapSource reads INBOX of an IMAP account over implicit TLS. It is read-only: INBOX is opened
// with EXAMINE and messages are fetched with BODY.PEEK, so no flag changes.
type imapSource struct {
	policy         imapNetworkPolicy
	resolver       imapResolver
	dialTimeout    time.Duration
	commandTimeout time.Duration
	overallTimeout time.Duration
	pageSize       int
	// commandBudget and sessionBudget override the byte budgets in tests.
	commandBudget int64
	sessionBudget int64
}

var (
	_ ExternalMailSource           = (*imapSource)(nil)
	_ ExternalMailPasswordVerifier = (*imapSource)(nil)
	_ ExternalMailGrantComparer    = (*imapSource)(nil)
)

func newIMAPSource() *imapSource {
	return &imapSource{
		resolver: net.DefaultResolver, dialTimeout: imapDialTimeout, commandTimeout: imapCommandTimeout,
		overallTimeout: imapOverallTimeout, pageSize: imapPageSize,
		commandBudget: imapCommandBudget, sessionBudget: imapSessionBudget,
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

// SameMailbox reports whether two grants read the same server. A grant that does not decode
// never matches.
func (*imapSource) SameMailbox(previous, next ExternalMailToken) bool {
	var before, after imapCredentials
	if json.Unmarshal([]byte(previous.RefreshToken), &before) != nil || json.Unmarshal([]byte(next.RefreshToken), &after) != nil {
		return false
	}
	return before.Host != "" && strings.EqualFold(before.Host, after.Host)
}

// imapAccountIdentity is the account a binding claims, derived only from what the server
// accepted: the username when it is an address, otherwise the username at the host.
func imapAccountIdentity(credentials imapCredentials) string {
	username := strings.ToLower(strings.TrimSpace(credentials.Username))
	if strings.Contains(username, "@") {
		return username
	}
	return username + "@" + credentials.Host
}

// VerifyPassword signs in and opens INBOX once, then returns the sealed-to-be credentials and
// the account identity.
func (s *imapSource) VerifyPassword(ctx context.Context, input ExternalMailPasswordCredentials) (ExternalMailToken, ExternalMailAccount, error) {
	credentials := imapCredentials{
		Host: strings.ToLower(strings.TrimSpace(input.Host)), Port: input.Port,
		Username: input.Username, Password: input.Password,
	}
	if credentials.Port == 0 {
		credentials.Port = imapPort
	}
	if smtpHost := strings.ToLower(strings.TrimSpace(input.SMTPHost)); smtpHost != "" {
		credentials.SMTPHost, credentials.SMTPPort = smtpHost, input.SMTPPort
		if credentials.SMTPPort == 0 {
			credentials.SMTPPort = smtpImplicitTLSPort
		}
		if err := credentials.validateSMTP(); err != nil {
			return ExternalMailToken{}, ExternalMailAccount{}, err
		}
	}
	if err := credentials.validate(); err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	identity := imapAccountIdentity(credentials)
	if len(identity) > imapMaxCredentialLength {
		return ExternalMailToken{}, ExternalMailAccount{}, ErrExternalMailAuthorizationInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.overallTimeout)
	defer cancel()
	session, err := s.open(ctx, credentials)
	if err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	defer session.close()
	if _, err = session.selectInbox(); err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	if credentials.SMTPHost != "" {
		if smtpErr := s.verifySMTP(ctx, credentials); smtpErr != nil {
			return ExternalMailToken{}, ExternalMailAccount{}, smtpErr
		}
	}
	sealed, err := json.Marshal(credentials)
	if err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	return ExternalMailToken{RefreshToken: string(sealed)}, ExternalMailAccount{Address: identity}, nil
}

// Fetch reads one page of INBOX after the cursor within one overall deadline. An empty cursor
// starts the backfill at the request's window; a changed UIDVALIDITY expires the cursor.
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
	ctx, cancel := context.WithTimeout(ctx, s.overallTimeout)
	defer cancel()
	reader := &imapPageReader{source: s, ctx: ctx, credentials: credentials}
	inbox, err := reader.connect()
	if err != nil {
		return ExternalMailFetch{}, err
	}
	defer reader.close()
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
		uids, err = reader.session.search("SINCE " + now.UTC().AddDate(0, 0, -days).Format("2-Jan-2006"))
	case inbox.uidNext == 0 || cursor.LastUID+1 < inbox.uidNext:
		uids, err = reader.session.search(fmt.Sprintf("UID %d:*", cursor.LastUID+1))
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
	messages, processed := reader.readPage(page)
	if len(page) > 0 && processed == 0 {
		// Not one message could be read before the deadline: try again at the next interval.
		return ExternalMailFetch{}, fmt.Errorf("imap: no message of the page could be read: %w", ctx.Err())
	}
	next := imapCursor{UIDValidity: inbox.uidValidity, LastUID: max(cursor.LastUID, processed)}
	more := len(uids) > 0 && next.LastUID < uids[len(uids)-1]
	if !more && inbox.uidNext > 0 && inbox.uidNext-1 > next.LastUID {
		// Everything in the window was read: later fetches start at mail newer than this one.
		next.LastUID = inbox.uidNext - 1
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return ExternalMailFetch{}, err
	}
	return ExternalMailFetch{Messages: messages, Cursor: string(encoded), More: more}, nil
}

// imapExternalID is stable across a UIDVALIDITY change: a message with a Message-ID is known by
// its hash, so a resync after the change stores no second copy. Only a message without one falls
// back to its UID.
func imapExternalID(messageID string, uidValidity, uid uint32) string {
	if messageID = strings.ToLower(strings.TrimSpace(messageID)); messageID != "" {
		sum := sha256.Sum256([]byte(messageID))
		return "m:" + hex.EncodeToString(sum[:])[:40]
	}
	return fmt.Sprintf("u:%d:%d", uidValidity, uid)
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

// imapRefusedPrefixes are special-purpose ranges a public mail server never uses: shared and
// benchmarking space, IETF protocol assignments, the reserved class E, and IPv6 forms that
// embed or translate an IPv4 address the IPv4 checks would otherwise judge.
var imapRefusedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
}

// allowedAddress refuses addresses inside the instance's own network.
func (p imapNetworkPolicy) allowedAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if p.AllowPrivate {
		return addr.IsValid()
	}
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsMulticast() ||
		addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range imapRefusedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// dial resolves the host, refuses any private address it resolves to, and connects over TLS to
// the address it checked, so a second resolution cannot point elsewhere.
func (s *imapSource) dial(ctx context.Context, credentials imapCredentials) (*tls.Conn, error) {
	if credentials.Port != imapPort && !s.policy.AllowAnyPort {
		return nil, fmt.Errorf("%w: only port %d (IMAP over TLS) is allowed", ErrExternalMailServerUnreachable, imapPort)
	}
	conn, err := s.dialServer(ctx, credentials.Host, credentials.Port, true)
	if err != nil {
		return nil, err
	}
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		closeQuietly(conn)
		return nil, ErrExternalMailServerUnreachable
	}
	return tlsConn, nil
}

// dialServer resolves host, refuses any private address it resolves to, and connects to the
// address it checked, so a second resolution cannot point elsewhere. With implicitTLS the
// connection is a verified TLS client; otherwise it is plain TCP for a later STARTTLS.
func (s *imapSource) dialServer(ctx context.Context, host string, port int, implicitTLS bool) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, s.dialTimeout)
	defer cancel()
	var addresses []netip.Addr
	if literal, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{literal}
	} else {
		resolved, err := s.resolver.LookupNetIP(ctx, "ip", host)
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
		raw, err := dialer.DialContext(ctx, "tcp", netip.AddrPortFrom(addr.Unmap(), uint16(port)).String())
		if err != nil {
			lastErr = err
			continue
		}
		if !implicitTLS {
			return raw, nil
		}
		conn := tls.Client(raw, s.tlsConfig(host))
		if err := conn.HandshakeContext(ctx); err != nil {
			closeQuietly(raw)
			lastErr = err
			continue
		}
		return conn, nil
	}
	return nil, fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, lastErr)
}

// imapPageReader reads one page, reconnecting when a command fails so one bad message cannot
// block the mailbox.
type imapPageReader struct {
	source      *imapSource
	ctx         context.Context
	credentials imapCredentials
	session     *imapSession
	uidValidity uint32
	// broken means the session's state is unknown after a failed command.
	broken bool
}

// connect opens a session and INBOX. A reconnect that finds another UIDVALIDITY expires the
// cursor.
func (r *imapPageReader) connect() (imapInbox, error) {
	r.close()
	session, err := r.source.open(r.ctx, r.credentials)
	if err != nil {
		return imapInbox{}, err
	}
	inbox, err := session.selectInbox()
	if err != nil {
		session.close()
		return imapInbox{}, err
	}
	if r.uidValidity != 0 && inbox.uidValidity != r.uidValidity {
		session.close()
		return imapInbox{}, ErrExternalMailCursorExpired
	}
	r.session, r.uidValidity, r.broken = session, inbox.uidValidity, false
	return inbox, nil
}

func (r *imapPageReader) close() {
	if r.session != nil {
		r.session.close()
		r.session = nil
	}
}

// skipped logs a message that will not be stored; the cursor still moves past it.
func (r *imapPageReader) skipped(uid uint32, reason string, attributes ...any) {
	slog.InfoContext(r.ctx, "external_mail.imap_message_skipped",
		append([]any{slog.Uint64("uid", uint64(uid)), slog.String("reason", reason)}, attributes...)...)
}

// readPage reads the UIDs in order and returns the messages and the last UID it is done with,
// read or skipped. Sizes come first, so a message above the inbound limit is never downloaded;
// bodies follow in batches bounded by count and announced size. A failed batch is retried one
// UID at a time; a UID that still fails is skipped. The page stops early at the deadline or when
// its bodies would exceed the page budget.
func (r *imapPageReader) readPage(page []uint32) ([]ExternalMessage, uint32) {
	if len(page) == 0 {
		return nil, 0
	}
	sizes, err := r.session.fetch(page, "(UID RFC822.SIZE)")
	if err != nil {
		r.broken = true
		messages, processed, _ := r.oneByOne(page, nil, 0)
		return messages, processed
	}
	var messages []ExternalMessage
	var processed uint32
	var batch []uint32
	var batchBytes, pageBytes int64
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		read, err := r.session.fetch(batch, "(UID RFC822.SIZE INTERNALDATE BODY.PEEK[])")
		if err != nil {
			r.broken = true
			var stopped bool
			messages, processed, stopped = r.oneByOne(batch, messages, processed)
			batch, batchBytes = nil, 0
			return !stopped
		}
		for _, uid := range batch {
			messages = r.collect(messages, uid, read)
			processed = uid
		}
		batch, batchBytes = nil, 0
		return true
	}
	for _, uid := range page {
		fetched, answered := sizes[uid]
		switch {
		case !answered:
			if !flush() {
				return messages, processed
			}
			r.skipped(uid, "no_response")
			processed = uid
			continue
		case fetched.size > imapMaxMessageBytes:
			if !flush() {
				return messages, processed
			}
			r.skipped(uid, "oversized", slog.Int64("size", fetched.size))
			processed = uid
			continue
		case pageBytes+fetched.size > imapPageBytes && (processed != 0 || len(batch) > 0):
			// The rest of the page continues in the next fetch.
			flush()
			return messages, processed
		}
		if len(batch) == imapBodyBatch || batchBytes+fetched.size > imapBatchBytes {
			if !flush() {
				return messages, processed
			}
		}
		batch = append(batch, uid)
		batchBytes += fetched.size
		pageBytes += fetched.size
	}
	flush()
	return messages, processed
}

// oneByOne reads each UID with its own commands, reconnecting after any failure. A UID that
// fails is skipped; the deadline ends the loop, skipping the UID it interrupted, and stopped
// reports that the UIDs were not all done.
func (r *imapPageReader) oneByOne(uids []uint32, messages []ExternalMessage, processed uint32) ([]ExternalMessage, uint32, bool) {
	for _, uid := range uids {
		if r.ctx.Err() != nil {
			return messages, processed, true
		}
		if r.broken {
			if _, err := r.connect(); err != nil {
				if r.ctx.Err() != nil || errors.Is(err, ErrExternalMailCursorExpired) {
					return messages, processed, true
				}
				r.broken = true
				r.skipped(uid, "reconnect_failed")
				processed = uid
				continue
			}
		}
		read, err := r.session.fetch([]uint32{uid}, "(UID RFC822.SIZE)")
		if err == nil {
			if fetched, answered := read[uid]; answered && fetched.size <= imapMaxMessageBytes {
				read, err = r.session.fetch([]uint32{uid}, "(UID RFC822.SIZE INTERNALDATE BODY.PEEK[])")
			}
		}
		if err != nil {
			r.broken = true
			reason := "fetch_failed"
			if r.ctx.Err() != nil {
				reason = "deadline"
			}
			r.skipped(uid, reason)
			processed = uid
			continue
		}
		messages = r.collect(messages, uid, read)
		processed = uid
	}
	return messages, processed, false
}

// collect parses the body of one requested UID, or logs why it is skipped.
func (r *imapPageReader) collect(messages []ExternalMessage, uid uint32, read map[uint32]imapFetched) []ExternalMessage {
	fetched, answered := read[uid]
	switch {
	case !answered:
		r.skipped(uid, "no_response")
		return messages
	case fetched.oversized || fetched.size > imapMaxMessageBytes:
		r.skipped(uid, "oversized", slog.Int64("size", fetched.size))
		return messages
	case fetched.body == nil:
		r.skipped(uid, "no_body")
		return messages
	}
	parsed, err := parseMail(fetched.body)
	if err != nil {
		r.skipped(uid, "unparseable")
		return messages
	}
	return append(messages, ExternalMessage{
		ExternalID:  imapExternalID(parsed.MessageID, r.uidValidity, uid),
		FromAddress: parsed.FromAddress, FromName: parsed.FromName,
		To: parsed.To, Cc: parsed.Cc, Subject: parsed.Subject, TextBody: parsed.TextBody, HTMLBody: parsed.HTMLBody,
		RFCMessageID: parsed.MessageID, InReplyTo: parsed.InReplyTo, References: parsed.References,
		ReceivedAt: fetched.internalDate,
	})
}

// imapBudgetReader counts every byte read from the connection against the budget of the current
// command and of the whole connection.
type imapBudgetReader struct {
	conn    net.Conn
	command int64
	session int64
}

func (b *imapBudgetReader) Read(p []byte) (int, error) {
	limit := min(b.command, b.session)
	if limit <= 0 {
		return 0, errIMAPBudget
	}
	if int64(len(p)) > limit {
		p = p[:limit]
	}
	n, err := b.conn.Read(p)
	b.command -= int64(n)
	b.session -= int64(n)
	return n, err
}

// imapSession is one signed-in connection.
type imapSession struct {
	conn          net.Conn
	reader        *bufio.Reader
	budget        *imapBudgetReader
	commandBudget int64
	timeout       time.Duration
	// deadline is the overall deadline no command may outlive.
	deadline time.Time
	tag      int
	stop     func() bool
}

// open connects, reads the greeting, and signs in.
func (s *imapSource) open(ctx context.Context, credentials imapCredentials) (*imapSession, error) {
	conn, err := s.dial(ctx, credentials)
	if err != nil {
		return nil, err
	}
	budget := &imapBudgetReader{conn: conn, command: s.commandBudget, session: s.sessionBudget}
	session := &imapSession{
		conn: conn, reader: bufio.NewReaderSize(budget, 32<<10), budget: budget,
		commandBudget: s.commandBudget, timeout: s.commandTimeout,
	}
	session.deadline, _ = ctx.Deadline()
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
	if err := session.authenticateAndIdentify(credentials.Username, credentials.Password); err != nil {
		session.close()
		return nil, err
	}
	return session, nil
}

// arm starts a command: a fresh byte budget and a deadline that never outlives the overall one.
func (s *imapSession) arm() error {
	s.budget.command = s.commandBudget
	deadline := time.Now().Add(s.timeout)
	if !s.deadline.IsZero() && s.deadline.Before(deadline) {
		deadline = s.deadline
	}
	return s.conn.SetDeadline(deadline)
}

// readGreeting reads the server's first line within one command timeout.
func (s *imapSession) readGreeting() (string, error) {
	if err := s.arm(); err != nil {
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
	if err := s.arm(); err != nil {
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
	bad  bool
	text string
}

// code is the response code that opens the completion text, such as "AUTHENTICATIONFAILED".
func (s imapStatus) code() string {
	text, ok := strings.CutPrefix(s.text, "[")
	if !ok {
		return ""
	}
	end := strings.IndexAny(text, " ]")
	if end < 0 {
		return ""
	}
	return strings.ToUpper(text[:end])
}

// imapHandlers receive the untagged responses of a command: FETCH responses of the requested
// UIDs parsed, any other response as its line after "* " when line is set. Everything else is
// read and dropped without being kept.
type imapHandlers struct {
	line  func(line string) error
	fetch func(fetched imapFetched)
	// want reports whether a UID was requested; nil wants none.
	want func(uid uint32) bool
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
		if status, tagged, err := parseCompletion(line, tag); tagged {
			return status, err
		}
	}
}

// parseCompletion reads a tagged completion line; tagged is false for any other line.
func parseCompletion(line, tag string) (imapStatus, bool, error) {
	rest, tagged := strings.CutPrefix(line, tag+" ")
	if !tagged {
		return imapStatus{}, false, nil
	}
	status, text, _ := strings.Cut(rest, " ")
	switch strings.ToUpper(status) {
	case "OK":
		return imapStatus{ok: true, text: text}, true, nil
	case "NO":
		return imapStatus{text: text}, true, nil
	case "BAD":
		return imapStatus{bad: true, text: text}, true, nil
	}
	return imapStatus{}, true, fmt.Errorf("imap: unexpected completion %q", status)
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
		want := handlers.want
		if want == nil {
			want = func(uint32) bool { return false }
		}
		fetched, fetchErr := s.readFetch(want, handlers.want != nil)
		if fetchErr != nil {
			return fetchErr
		}
		if handlers.fetch != nil && fetched.uid != 0 && want(fetched.uid) {
			handlers.fetch(fetched)
		}
		return nil
	}
	if handlers.line == nil {
		return s.discardLine()
	}
	rest, err := s.readLine()
	if err != nil {
		return err
	}
	return handlers.line(strings.TrimSpace(first + " " + second + rest))
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

// discardLine drops the rest of a response line and any literal it announces, keeping nothing
// but the end of the last chunk read.
func (s *imapSession) discardLine() error {
	for {
		var tail []byte
		for {
			chunk, err := s.reader.ReadSlice('\n')
			tail = append(tail[:0], chunk[max(0, len(chunk)-24):]...)
			if err == nil {
				break
			}
			if !errors.Is(err, bufio.ErrBufferFull) {
				return err
			}
		}
		size, ok := trailingLiteral(bytes.TrimRight(tail, "\r\n"))
		if !ok {
			return nil
		}
		if _, err := io.CopyN(io.Discard, s.reader, size); err != nil {
			return err
		}
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

// imapTransientCodes are LOGIN refusals that say nothing about the password.
var imapTransientCodes = map[string]bool{
	"UNAVAILABLE": true, "LIMIT": true, "INUSE": true, "SERVERBUG": true, "CONTACTADMIN": true,
}

// loginRefusal turns a LOGIN completion into an error: a NO is a refused password unless its
// response code says the refusal is transient; a BAD is a protocol fault, retryable like one.
func loginRefusal(status imapStatus) error {
	switch {
	case status.ok:
		return nil
	case status.bad:
		return fmt.Errorf("imap: LOGIN rejected as malformed: %s", status.code())
	case imapTransientCodes[status.code()]:
		return fmt.Errorf("imap: LOGIN refused for now [%s]", status.code())
	}
	return ErrExternalMailAuth
}

// login signs in. Printable ASCII values go as quoted strings; any other value goes as a literal
// the server must invite. Control characters were refused before.
func (s *imapSession) login(username, password string) error {
	s.tag++
	tag := "Z" + strconv.Itoa(s.tag)
	if err := s.arm(); err != nil {
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
			// The server invites each literal with a continuation, or completes the command.
			line, err := s.readLine()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
			}
			if !strings.HasPrefix(line, "+") {
				status, tagged, err := parseCompletion(line, tag)
				if err != nil || !tagged {
					return fmt.Errorf("imap: unexpected answer to a LOGIN literal")
				}
				return loginRefusal(status)
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
	return loginRefusal(status)
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

// imapInbox is what EXAMINE reports about INBOX.
type imapInbox struct {
	uidValidity uint32
	uidNext     uint32
}

// selectInbox opens INBOX with EXAMINE, the read-only form of SELECT, so syncing never changes
// the account: no \Recent flag is consumed, and messages are read with BODY.PEEK. A refusal is a
// retryable error, not a lost authorization.
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
		return imapInbox{}, fmt.Errorf("imap: INBOX cannot be opened [%s]", status.code())
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

// fetch runs UID FETCH for the UIDs and returns the responses of those UIDs; responses about
// any other message are read and dropped.
func (s *imapSession) fetch(uids []uint32, items string) (map[uint32]imapFetched, error) {
	set := make([]string, len(uids))
	requested := make(map[uint32]bool, len(uids))
	for i, uid := range uids {
		set[i] = strconv.FormatUint(uint64(uid), 10)
		requested[uid] = true
	}
	if err := s.send("UID FETCH " + strings.Join(set, ",") + " " + items); err != nil {
		return nil, err
	}
	results := map[uint32]imapFetched{}
	status, err := s.finish(imapHandlers{
		want:  func(uid uint32) bool { return requested[uid] },
		fetch: func(fetched imapFetched) { results[fetched.uid] = fetched },
	})
	if err != nil {
		return nil, err
	}
	if !status.ok {
		return nil, fmt.Errorf("imap: UID FETCH refused: %s", status.text)
	}
	return results, nil
}

// readFetch reads the "(...)" of a FETCH response and the line end after it. A body is kept only
// while the command fetches bodies, only up to the message limit, and not for a UID announced
// before it that was not requested; any other body is discarded as it arrives. A body announced
// before its UID is kept within the command's byte budget and dropped afterwards when unwanted.
func (s *imapSession) readFetch(want func(uint32) bool, fetching bool) (imapFetched, error) {
	if err := s.skipSpaces(); err != nil {
		return imapFetched{}, err
	}
	open, err := s.reader.ReadByte()
	if err != nil {
		return imapFetched{}, err
	}
	if open != '(' {
		return imapFetched{}, errors.New("imap: malformed FETCH response")
	}
	var fetched imapFetched
	for items := 0; ; items++ {
		if items > 64 {
			return imapFetched{}, errors.New("imap: FETCH response too long")
		}
		if err = s.skipSpaces(); err != nil {
			return imapFetched{}, err
		}
		next, err := s.reader.Peek(1)
		if err != nil {
			return imapFetched{}, err
		}
		if next[0] == ')' {
			if err = s.skip(1); err != nil {
				return imapFetched{}, err
			}
			break
		}
		key, err := s.readValue(0, imapMaxSmallLiteral)
		if err != nil {
			return imapFetched{}, err
		}
		name := strings.ToUpper(key.atom)
		keep := int64(imapMaxSmallLiteral)
		isBody := strings.HasPrefix(name, "BODY[") || name == "RFC822"
		if isBody {
			keep = 0
			if fetching && (fetched.uid == 0 || want(fetched.uid)) {
				keep = imapMaxMessageBytes
			}
		}
		item, err := s.readValue(0, keep)
		if err != nil {
			return imapFetched{}, err
		}
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
		case isBody:
			fetched.body, fetched.oversized = item.text, item.oversized
		}
	}
	if _, err := s.readRawLine(imapMaxLineBytes); err != nil {
		return imapFetched{}, err
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

// readValue reads one value from the stream. A literal larger than keep is discarded as it
// arrives and marked oversized; no literal is allocated beyond what the command may still
// receive.
func (s *imapSession) readValue(depth int, keep int64) (imapValue, error) {
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
			item, err := s.readValue(depth+1, imapMaxSmallLiteral)
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
		if size > keep {
			if _, err := io.CopyN(io.Discard, s.reader, size); err != nil {
				return imapValue{}, err
			}
			return imapValue{oversized: true}, nil
		}
		if size > s.budget.command+int64(s.reader.Buffered()) {
			return imapValue{}, errIMAPBudget
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
