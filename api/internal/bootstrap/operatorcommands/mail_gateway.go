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
	"syscall"
	"time"

	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/console"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/imapgateway"
)

// mailGatewayShutdownGrace is how long a stopping gateway waits for sessions to end after BYE.
const mailGatewayShutdownGrace = 10 * time.Second

var errMailGatewayRuntimeUnavailable = errors.New("mail gateway runtime is unavailable")

// MailGatewayCommand serves mailboxes to mail apps over implicit-TLS IMAP until SIGINT or SIGTERM.
type MailGatewayCommand struct {
	output     *console.Output
	loadConfig func() (*config.Config, error)
}

func NewMailGatewayCommand() *MailGatewayCommand {
	return &MailGatewayCommand{output: console.NewOutput(), loadConfig: config.Load}
}

func (c *MailGatewayCommand) Name() string { return "mail:gateway" }
func (c *MailGatewayCommand) Description() string {
	return "Serve mailboxes to mail apps over IMAP with mail client passwords (read-only in this release)"
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
		slog.Info("imap_gateway.disabled", "reason", "MAIL_GATEWAY_IMAP_ADDR is empty")
		c.output.Info("The mail gateway is off (MAIL_GATEWAY_IMAP_ADDR is empty); nothing to serve")
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
	return runMailGateway(ctx, cfg.MailGateway.Resolved(), application.MailClients, limits, slog.Default())
}

// runMailGateway listens, sweeps, and serves health until ctx ends, then says goodbye to every
// session and stops.
func runMailGateway(ctx context.Context, cfg config.MailGatewayConfig, store domain.MailClientStore, limits *ratelimit.Stores, logger *slog.Logger) error {
	certificates, err := imapgateway.NewCertificateReloader(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.IMAPAddr)
	if err != nil {
		return fmt.Errorf("mail gateway listen: %w", err)
	}
	healthListener, err := net.Listen("tcp", cfg.HealthAddr)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("mail gateway health listen: %w", err)
	}
	server := imapgateway.NewServer(store, imapgateway.Config{
		MaxConnections: cfg.MaxConnections, MaxPerUser: cfg.MaxConnectionsPerUser, MaxPerIP: cfg.MaxConnectionsPerIP,
		Limits: limits,
	}, logger)
	health := &http.Server{Handler: server.HealthHandler(), ReadHeaderTimeout: 5 * time.Second}

	errs := make(chan error, 2)
	go func() { errs <- server.Serve(tls.NewListener(listener, certificates.TLSConfig())) }()
	go func() {
		if serveErr := health.Serve(healthListener); !errors.Is(serveErr, http.ErrServerClosed) {
			errs <- serveErr
		}
	}()
	sweepCtx, stopSweep := context.WithCancel(ctx)
	defer stopSweep()
	go server.RunSweeper(sweepCtx, cfg.RevalidateInterval)
	logger.Info("imap_gateway.started", "imap_addr", listener.Addr().String(), "health_addr", healthListener.Addr().String(),
		"revalidate_interval", cfg.RevalidateInterval.String())

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errs:
	}
	stopSweep()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), mailGatewayShutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("imap_gateway.shutdown_failed", "error", err)
	}
	if err := health.Shutdown(shutdownCtx); err != nil {
		logger.Warn("imap_gateway.health_shutdown_failed", "error", err)
	}
	logger.Info("imap_gateway.stopped")
	return serveErr
}
