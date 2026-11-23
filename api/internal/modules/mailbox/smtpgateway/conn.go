package smtpgateway

import (
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/fastagi/zmail/api/internal/modules/mailbox/mailclientauth"
)

// Connection caps (spec §6.4).
var (
	errTooManyConnections = errors.New("too many connections, try again later")
	errTooManyFromAddress = errors.New("too many connections from this address")
	errTooManyForAccount  = errors.New("too many connections for this account")
)

// errExpired ends a connection whose time is up: go-smtp reads it as a timeout, answers
// 421 4.4.2, and closes.
var errExpired net.Error = expiredError{}

type expiredError struct{}

func (expiredError) Error() string   { return "smtp gateway: connection expired" }
func (expiredError) Timeout() bool   { return true }
func (expiredError) Temporary() bool { return true }

// listener admits connections under the caps and tracks each one from its TCP accept.
type listener struct {
	net.Listener
	server      *Server
	implicitTLS bool
}

func (l *listener) Accept() (net.Conn, error) {
	for {
		raw, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		c := &conn{Conn: raw, ip: remoteIP(raw.RemoteAddr())}
		if err := l.server.registry.open(c); err != nil {
			recordConnectionRefused()
			if !l.implicitTLS {
				// A plain connection can read why; an implicit-TLS one expects a handshake.
				// Best effort: the connection is refused and closed either way.
				if deadlineErr := raw.SetWriteDeadline(time.Now().Add(5 * time.Second)); deadlineErr == nil {
					if _, writeErr := raw.Write([]byte("421 4.7.0 " + capitalize(err.Error()) + "\r\n")); writeErr != nil {
						l.server.logger.Debug("smtp_gateway.refusal_write_failed")
					}
				}
			}
			if closeErr := raw.Close(); closeErr != nil {
				l.server.logger.Debug("smtp_gateway.refusal_close_failed")
			}
			continue
		}
		recordConnections(l.server.registry.count())
		c.authTimer = time.AfterFunc(l.server.cfg.AuthTimeout, c.expire)
		c.onClose = func() {
			l.server.registry.close(c)
			recordConnections(l.server.registry.count())
		}
		return c, nil
	}
}

// conn is one mail app connection under the TLS layer, if any. It outlives the go-smtp sessions
// on it: STARTTLS ends one session and starts the next on the same conn.
type conn struct {
	net.Conn
	ip string

	expired      atomic.Bool
	busy         atomic.Bool
	authTimer    *time.Timer
	membershipID atomic.Uint64

	closeOnce sync.Once
	onClose   func()
}

func (c *conn) Read(b []byte) (int, error) {
	if c.expired.Load() {
		return 0, errExpired
	}
	return c.Conn.Read(b)
}

func (c *conn) Close() error {
	c.closeOnce.Do(func() {
		if c.authTimer != nil {
			c.authTimer.Stop()
		}
		if c.onClose != nil {
			c.onClose()
		}
	})
	return c.Conn.Close()
}

// expire makes the next read fail, and wakes a read that waits, so go-smtp says goodbye and
// closes the connection.
func (c *conn) expire() {
	c.expired.Store(true)
	// A connection whose deadline cannot be set is already closing; the flag ends its next read.
	if err := c.SetReadDeadline(time.Now()); err != nil {
		return
	}
}

// signedIn stops the sign-in clock.
func (c *conn) signedIn(membershipID uint) {
	if c.authTimer != nil {
		c.authTimer.Stop()
	}
	c.membershipID.Store(uint64(membershipID))
}

// trackedConn finds the conn under a go-smtp connection, through its TLS layer.
func trackedConn(c *smtp.Conn) *conn {
	raw := c.Conn()
	if tlsConn, ok := raw.(*tls.Conn); ok {
		raw = tlsConn.NetConn()
	}
	if tracked, ok := raw.(*conn); ok {
		return tracked
	}
	return nil
}

// registry counts live connections in total and per IP from the accept, and per membership from
// sign-in.
type registry struct {
	maxTotal, maxPerUser, maxPerIP int

	mu     sync.Mutex
	conns  map[*conn]struct{}
	byIP   map[string]int
	byUser map[uint]int
}

func newRegistry(maxTotal, maxPerUser, maxPerIP int) *registry {
	return &registry{
		maxTotal: maxTotal, maxPerUser: maxPerUser, maxPerIP: maxPerIP,
		conns: map[*conn]struct{}{}, byIP: map[string]int{}, byUser: map[uint]int{},
	}
}

func (r *registry) open(c *conn) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	bucket := mailclientauth.IPBucket(c.ip)
	switch {
	case len(r.conns) >= r.maxTotal:
		return errTooManyConnections
	case r.byIP[bucket] >= r.maxPerIP:
		return errTooManyFromAddress
	}
	r.conns[c] = struct{}{}
	r.byIP[bucket]++
	return nil
}

// signIn counts a signed-in connection against its membership, or answers why not.
func (r *registry) signIn(c *conn, membershipID uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.conns[c]; !ok {
		return errTooManyConnections
	}
	if r.byUser[membershipID] >= r.maxPerUser {
		return errTooManyForAccount
	}
	r.byUser[membershipID]++
	c.signedIn(membershipID)
	return nil
}

func (r *registry) close(c *conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.conns[c]; !ok {
		return
	}
	delete(r.conns, c)
	bucket := mailclientauth.IPBucket(c.ip)
	if r.byIP[bucket]--; r.byIP[bucket] <= 0 {
		delete(r.byIP, bucket)
	}
	if membershipID := uint(c.membershipID.Load()); membershipID != 0 {
		if r.byUser[membershipID]--; r.byUser[membershipID] <= 0 {
			delete(r.byUser, membershipID)
		}
	}
}

func (r *registry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.conns)
}

// expireIdle ends every connection that is not in the middle of sending a message.
func (r *registry) expireIdle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.conns {
		if !c.busy.Load() {
			c.expire()
		}
	}
}

func remoteIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

func capitalize(text string) string {
	if text == "" || text[0] < 'a' || text[0] > 'z' {
		return text
	}
	return string(text[0]-'a'+'A') + text[1:]
}
