// Package smtpgateway lets mail apps send from Zmail mailboxes over SMTP submission: implicit TLS
// (port 465) and STARTTLS (port 587), signed in with a mail client password. It is the only
// package that imports go-smtp; it signs in and sends only through domain.MailSubmissionStore,
// which checks the member's access on every call.
package smtpgateway

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/mailclientauth"
)

// Defaults of Config, the same caps as the IMAP gateway (spec §6.4).
const (
	DefaultMaxConnections = 1000
	DefaultMaxPerUser     = 20
	DefaultMaxPerIP       = 100
	DefaultFailureDelay   = time.Second
	// DefaultAuthTimeout is how long a connection may stay without signing in, counted from the
	// TCP accept; STARTTLS does not restart it.
	DefaultAuthTimeout = 60 * time.Second
	// DefaultIdleTimeout closes a connection that sends no line for this long.
	DefaultIdleTimeout = 5 * time.Minute
	// writeTimeout bounds one reply.
	writeTimeout = time.Minute
	// storeTimeout bounds one store call; a send waits for the provider.
	storeTimeout = 90 * time.Second
	// maxRcptCommands bounds the RCPT commands of one message, repeats included.
	maxRcptCommands = 200
)

// Config tunes the gateway; zero fields take the defaults.
type Config struct {
	// Domain is the host name the greeting names, such as smtp.zmail.im.
	Domain         string
	MaxConnections int
	MaxPerUser     int
	MaxPerIP       int
	// FailureDelay is the fixed wait before answering a refused sign-in.
	FailureDelay time.Duration
	AuthTimeout  time.Duration
	IdleTimeout  time.Duration
	// Limits backs the sign-in rate limits; nil keeps them in this process.
	Limits *ratelimit.Stores
	// Limiter, when set, is the sign-in limiter shared with the IMAP gateway; Limits is then unused.
	Limiter *mailclientauth.Limiter
	Now     func() time.Time
}

func (c Config) withDefaults() Config {
	if c.Domain == "" {
		c.Domain = "localhost"
	}
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
	if c.AuthTimeout <= 0 {
		c.AuthTimeout = DefaultAuthTimeout
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Limiter == nil {
		c.Limiter = mailclientauth.NewLimiter(c.Limits, c.Now)
	}
	return c
}

// Server is the SMTP submission gateway.
type Server struct {
	store    domain.MailSubmissionStore
	cfg      Config
	logger   *slog.Logger
	tls      *tls.Config
	registry *registry
	smtp     *smtp.Server

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	closing bool
}

// NewServer builds a gateway over store that offers tlsConfig on both listeners. It never logs
// subjects, addresses, bodies, passwords, or usernames, and go-smtp's protocol trace stays off.
func NewServer(store domain.MailSubmissionStore, tlsConfig *tls.Config, cfg Config, logger *slog.Logger) *Server {
	cfg = cfg.withDefaults()
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		store: store, cfg: cfg, logger: logger, tls: tlsConfig,
		registry: newRegistry(cfg.MaxConnections, cfg.MaxPerUser, cfg.MaxPerIP),
		ctx:      ctx, cancel: cancel,
	}
	server := smtp.NewServer(backend{server: s})
	server.Domain = cfg.Domain
	server.TLSConfig = tlsConfig
	server.MaxMessageBytes = domain.MailClientMaxSubmissionBytes
	server.MaxLineLength = 2000
	// Recipients are counted without repeats in the session, against domain.MaxMailRecipients.
	server.MaxRecipients = 0
	// AUTH is neither offered nor accepted before TLS.
	server.AllowInsecureAuth = false
	server.ReadTimeout = cfg.IdleTimeout
	server.WriteTimeout = writeTimeout
	server.Debug = nil
	server.ErrorLog = protocolLogger{logger: logger}
	s.smtp = server
	return s
}

// ServeImplicitTLS serves mail apps that start TLS at once (port 465) on ln, a plain TCP listener.
// It returns when ln closes or the server stops.
func (s *Server) ServeImplicitTLS(ln net.Listener) error {
	return s.serve(tls.NewListener(&listener{Listener: ln, server: s, implicitTLS: true}, s.tls))
}

// ServeSubmission serves mail apps that upgrade with STARTTLS (port 587) on ln, a plain TCP
// listener. It returns when ln closes or the server stops.
func (s *Server) ServeSubmission(ln net.Listener) error {
	return s.serve(&listener{Listener: ln, server: s})
}

func (s *Server) serve(ln net.Listener) error {
	err := s.smtp.Serve(ln)
	if errors.Is(err, net.ErrClosed) || errors.Is(err, smtp.ErrServerClosed) {
		return nil
	}
	return err
}

// Close ends every connection at once and stops accepting.
func (s *Server) Close() error {
	if !s.markClosing() {
		return nil
	}
	s.cancel()
	if err := s.smtp.Close(); err != nil && !errors.Is(err, smtp.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

// Shutdown stops accepting, lets the messages in flight finish until ctx ends, and closes.
func (s *Server) Shutdown(ctx context.Context) error {
	if !s.markClosing() {
		return nil
	}
	// Idle connections end now; a connection sending a message ends after its reply.
	s.registry.expireIdle()
	err := s.smtp.Shutdown(ctx)
	s.cancel()
	if err != nil && !errors.Is(err, smtp.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		closeErr := s.smtp.Close()
		if closeErr != nil && !errors.Is(closeErr, smtp.ErrServerClosed) {
			return closeErr
		}
	}
	return nil
}

func (s *Server) markClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.closing = true
	return true
}

// Closing reports whether the gateway is stopping.
func (s *Server) Closing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

// Connections is the number of live connections.
func (s *Server) Connections() int { return s.registry.count() }

// storeContext bounds one store call and ends with the server.
func (s *Server) storeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.ctx, storeTimeout)
}

// protocolLogger receives go-smtp's own error lines. Their arguments carry the client's address
// and can quote its input, so only the fixed format string is logged.
type protocolLogger struct {
	logger *slog.Logger
}

func (l protocolLogger) Printf(format string, _ ...any) {
	l.logger.Warn("smtp_gateway.protocol_error", "format", format)
}

func (l protocolLogger) Println(_ ...any) {
	l.logger.Warn("smtp_gateway.protocol_error")
}
