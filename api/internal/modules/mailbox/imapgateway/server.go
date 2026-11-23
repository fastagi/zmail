// Package imapgateway serves Zmail mailboxes to mail apps over IMAP. It is the only package that
// imports go-imap; it reads and changes mail only through domain.MailClientStore, which checks the
// member's access on every call.
package imapgateway

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// Defaults of Config (spec §6.4 and the P1a plan).
const (
	DefaultMaxConnections   = 1000
	DefaultMaxPerUser       = 20
	DefaultMaxPerIP         = 100
	DefaultFailureDelay     = time.Second
	DefaultIdlePoll         = 30 * time.Second
	DefaultFetchBatch       = 500
	DefaultSourceCacheBytes = 64 << 20
	// commandTimeout bounds the store calls of one command.
	commandTimeout = 30 * time.Second
)

// Config tunes the gateway; zero fields take the defaults.
type Config struct {
	MaxConnections int
	MaxPerUser     int
	MaxPerIP       int
	// FailureDelay is the fixed wait before answering a refused sign-in.
	FailureDelay time.Duration
	// IdlePoll is how often an IDLE command looks for new and removed mail.
	IdlePoll time.Duration
	// FetchBatch is how many messages one store read of a FETCH covers.
	FetchBatch       int
	SourceCacheBytes int64
	// Limits backs the sign-in rate limits; nil keeps them in this process.
	Limits *ratelimit.Stores
	Now    func() time.Time
}

func (c Config) withDefaults() Config {
	if c.MaxConnections <= 0 {
		c.MaxConnections = DefaultMaxConnections
	}
	if c.MaxPerUser <= 0 {
		c.MaxPerUser = DefaultMaxPerUser
	}
	if c.MaxPerIP <= 0 {
		c.MaxPerIP = DefaultMaxPerIP
	}
	if c.FailureDelay < 0 {
		c.FailureDelay = 0
	} else if c.FailureDelay == 0 {
		c.FailureDelay = DefaultFailureDelay
	}
	if c.IdlePoll <= 0 {
		c.IdlePoll = DefaultIdlePoll
	}
	if c.FetchBatch <= 0 {
		c.FetchBatch = DefaultFetchBatch
	}
	if c.SourceCacheBytes <= 0 {
		c.SourceCacheBytes = DefaultSourceCacheBytes
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// capabilities the gateway advertises. IMAP4rev2 is left out on purpose: it includes MOVE, which
// P1a refuses. beta.9 adds IDLE, ENABLE, UNSELECT, UTF8=ACCEPT, SASL-IR, LITERAL-, and AUTH=PLAIN
// on its own; ID is not implemented by beta.9.
var capabilities = imap.CapSet{
	imap.CapIMAP4rev1:  {},
	imap.CapNamespace:  {},
	imap.CapUIDPlus:    {},
	imap.CapSpecialUse: {},
	imap.CapListStatus: {},
}

// Server is the IMAP gateway.
type Server struct {
	store    domain.MailClientStore
	cfg      Config
	logger   *slog.Logger
	limiter  *authLimiter
	cache    *sourceCache
	registry *sessionRegistry
	imap     *imapserver.Server
	nextID   atomic.Uint64

	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	closing bool
}

// NewServer builds a gateway over store. It never logs subjects, addresses, bodies, passwords, or
// usernames, and go-imap's raw protocol trace stays off.
func NewServer(store domain.MailClientStore, cfg Config, logger *slog.Logger) *Server {
	cfg = cfg.withDefaults()
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		store: store, cfg: cfg, logger: logger,
		limiter:  newAuthLimiter(cfg.Limits, cfg.Now),
		cache:    newSourceCache(cfg.SourceCacheBytes),
		registry: newSessionRegistry(cfg.MaxConnections, cfg.MaxPerUser, cfg.MaxPerIP),
		ctx:      ctx, cancel: cancel,
	}
	s.imap = imapserver.New(&imapserver.Options{
		NewSession:  s.newSession,
		Caps:        capabilities,
		Logger:      protocolLogger{logger: logger},
		DebugWriter: nil,
	})
	return s
}

// Serve accepts mail apps on ln, which must hand out *tls.Conn (tls.NewListener): go-imap only
// accepts a sign-in over TLS. It returns when ln closes or Close is called.
func (s *Server) Serve(ln net.Listener) error {
	err := s.imap.Serve(ln)
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// Close ends every session at once and stops accepting.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	s.mu.Unlock()
	s.cancel()
	return s.imap.Close()
}

// Shutdown says goodbye to every session, waits up to the context for them to end, and closes.
func (s *Server) Shutdown(ctx context.Context) error {
	for _, live := range s.registry.snapshot() {
		live.bye("Server shutting down")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for s.registry.count() > 0 {
		select {
		case <-ctx.Done():
			return s.Close()
		case <-ticker.C:
		}
	}
	return s.Close()
}

// Connections is the number of live sessions.
func (s *Server) Connections() int { return s.registry.count() }

func (s *Server) newSession(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
	ip := remoteIP(conn.NetConn().RemoteAddr())
	sess := &session{server: s, conn: conn, ip: ip, id: s.nextID.Add(1)}
	if err := s.registry.open(sess); err != nil {
		recordConnectionRefused()
		return nil, nil, &imap.Error{Type: imap.StatusResponseTypeBye, Code: imap.ResponseCodeUnavailable, Text: err.Error()}
	}
	recordConnections(s.registry.count())
	return sess, nil, nil
}

// commandContext bounds the store calls of one command and ends with the server.
func (s *Server) commandContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.ctx, commandTimeout)
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

// protocolLogger receives go-imap's own error lines. Their arguments can quote client input, so
// only the fixed format string is logged.
type protocolLogger struct {
	logger *slog.Logger
}

func (l protocolLogger) Printf(format string, _ ...any) {
	l.logger.Warn("imap_gateway.protocol_error", "format", format)
}
