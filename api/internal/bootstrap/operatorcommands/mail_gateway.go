package operatorcommands

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/console"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/imapgateway"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/mailclientauth"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/smtpgateway"
)

// mailGatewayShutdownGrace is how long a stopping gateway waits for sessions to end after BYE.
const mailGatewayShutdownGrace = 10 * time.Second

var errMailGatewayRuntimeUnavailable = errors.New("mail gateway runtime is unavailable")

// MailGatewayCommand serves mailboxes to mail apps over implicit-TLS IMAP and accepts the mail they
// send over SMTP submission until SIGINT or SIGTERM.
type MailGatewayCommand struct {
	output     *console.Output
	loadConfig func() (*config.Config, error)
}

func NewMailGatewayCommand() *MailGatewayCommand {
	return &MailGatewayCommand{output: console.NewOutput(), loadConfig: config.Load}
}

func (c *MailGatewayCommand) Name() string { return "mail:gateway" }
func (c *MailGatewayCommand) Description() string {
	return "Serve mailboxes to mail apps over IMAP (read-only) and SMTP submission with mail client passwords"
}
func (c *MailGatewayCommand) Usage() string { return "mail:gateway" }

func (c *MailGatewayCommand) Run(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("mail:gateway takes no arguments; configure it with MAIL_GATEWAY_* variables")
	}
	cfg, err := c.loadConfig()
	if err != nil {
		return err
	}
	if !cfg.MailGateway.Enabled() {
		slog.Info("mail_gateway.disabled", "reason", "no MAIL_GATEWAY_*_ADDR is set")
		c.output.Info("The mail gateway is off (MAIL_GATEWAY_IMAP_ADDR, MAIL_GATEWAY_SMTPS_ADDR, and MAIL_GATEWAY_SUBMISSION_ADDR are empty); nothing to serve")
		return nil
	}
	application, cfg, err := initMailboxApplication()
	if err != nil {
		return err
	}
	if application.MailClients == nil {
		return errMailGatewayRuntimeUnavailable
	}
	limits, err := ratelimit.ProvideStores(cfg, application.DB)
	if err != nil {
		return fmt.Errorf("mail gateway rate limits: %w", err)
	}
	tracerProvider := bootstrap.InitTracing(cfg, application.DB)
	defer bootstrap.ShutdownTracing(tracerProvider)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runMailGateway(ctx, mailGatewayRuntime{
		cfg: cfg.MailGateway.Resolved(), smtpDomain: cfg.Mailbox.Resolved(cfg.IsProduction()).ClientSMTPHost,
		clients: application.MailClients, submissions: application.MailSubmissions, limits: limits, logger: slog.Default(),
	})
}

// mailGatewayRuntime is what one gateway process serves with.
type mailGatewayRuntime struct {
	cfg config.MailGatewayConfig
	// smtpDomain is the host name the SMTP greeting names.
	smtpDomain  string
	clients     domain.MailClientStore
	submissions domain.MailSubmissionStore
	limits      *ratelimit.Stores
	logger      *slog.Logger
}

// runMailGateway listens on every configured address, sweeps IMAP sessions, and serves health
// until ctx ends, then says goodbye to every session and stops. IMAP and SMTP share one
// certificate, one sign-in limiter, and one health address.
func runMailGateway(ctx context.Context, rt mailGatewayRuntime) error {
	cfg, logger := rt.cfg, rt.logger
	certificates, err := imapgateway.NewCertificateReloader(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return err
	}
	var listeners []net.Listener
	closeListeners := func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}
	listen := func(addr, what string) (net.Listener, error) {
		if addr == "" {
			return nil, nil
		}
		listener, listenErr := net.Listen("tcp", addr)
		if listenErr != nil {
			closeListeners()
			return nil, fmt.Errorf("mail gateway %s listen: %w", what, listenErr)
		}
		listeners = append(listeners, listener)
		return listener, nil
	}
	imapListener, err := listen(cfg.IMAPAddr, "IMAP")
	if err != nil {
		return err
	}
	smtpsListener, err := listen(cfg.SMTPSAddr, "SMTPS")
	if err != nil {
		return err
	}
	submissionListener, err := listen(cfg.SubmissionAddr, "submission")
	if err != nil {
		return err
	}
	if (smtpsListener != nil || submissionListener != nil) && rt.submissions == nil {
		closeListeners()
		return errMailGatewayRuntimeUnavailable
	}
	healthListener, err := listen(cfg.HealthAddr, "health")
	if err != nil {
		return err
	}

	limiter := mailclientauth.NewLimiter(rt.limits, nil)
	var stopping atomic.Bool
	health := &http.Server{Handler: mailGatewayHealth(&stopping, logger), ReadHeaderTimeout: 5 * time.Second}
	errs := make(chan error, 4)
	go func() {
		if serveErr := health.Serve(healthListener); !errors.Is(serveErr, http.ErrServerClosed) {
			errs <- serveErr
		}
	}()

	var imapServer *imapgateway.Server
	sweepCtx, stopSweep := context.WithCancel(ctx)
	defer stopSweep()
	if imapListener != nil {
		imapServer = imapgateway.NewServer(rt.clients, imapgateway.Config{
			MaxConnections: cfg.MaxConnections, MaxPerUser: cfg.MaxConnectionsPerUser, MaxPerIP: cfg.MaxConnectionsPerIP,
			Limiter: limiter,
		}, logger)
		go func() { errs <- imapServer.Serve(tls.NewListener(imapListener, certificates.TLSConfig())) }()
		go imapServer.RunSweeper(sweepCtx, cfg.RevalidateInterval)
	}
	var smtpServer *smtpgateway.Server
	if smtpsListener != nil || submissionListener != nil {
		smtpServer = smtpgateway.NewServer(rt.submissions, certificates.TLSConfig(), smtpgateway.Config{
			Domain:         rt.smtpDomain,
			MaxConnections: cfg.MaxConnections, MaxPerUser: cfg.MaxConnectionsPerUser, MaxPerIP: cfg.MaxConnectionsPerIP,
			Limiter: limiter,
		}, logger)
		if smtpsListener != nil {
			go func() { errs <- smtpServer.ServeImplicitTLS(smtpsListener) }()
		}
		if submissionListener != nil {
			go func() { errs <- smtpServer.ServeSubmission(submissionListener) }()
		}
	}
	logger.Info("mail_gateway.started", "imap_addr", addrOf(imapListener), "smtps_addr", addrOf(smtpsListener),
		"submission_addr", addrOf(submissionListener), "health_addr", healthListener.Addr().String(),
		"revalidate_interval", cfg.RevalidateInterval.String())

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errs:
	}
	stopping.Store(true)
	stopSweep()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), mailGatewayShutdownGrace)
	defer cancel()
	var stopped sync.WaitGroup
	if imapServer != nil {
		stopped.Go(func() {
			if err := imapServer.Shutdown(shutdownCtx); err != nil {
				logger.Warn("imap_gateway.shutdown_failed", "error", err)
			}
		})
	}
	if smtpServer != nil {
		stopped.Go(func() {
			if err := smtpServer.Shutdown(shutdownCtx); err != nil {
				logger.Warn("smtp_gateway.shutdown_failed", "error", err)
			}
		})
	}
	stopped.Wait()
	if err := health.Shutdown(shutdownCtx); err != nil {
		logger.Warn("mail_gateway.health_shutdown_failed", "error", err)
	}
	logger.Info("mail_gateway.stopped")
	return serveErr
}

// mailGatewayHealth serves /health, which answers 503 once the gateway is stopping, and the
// process's Prometheus /metrics. Bind it to a private address.
func mailGatewayHealth(stopping *atomic.Bool, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status, body := http.StatusOK, `{"status":"ok"}`
		if stopping.Load() {
			status, body = http.StatusServiceUnavailable, `{"status":"stopping"}`
		}
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			logger.Debug("mail_gateway.health_write_failed", "error", err)
		}
	})
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

func addrOf(listener net.Listener) string {
	if listener == nil {
		return ""
	}
	return listener.Addr().String()
}
