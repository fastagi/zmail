package operatorcommands

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/console"
)

const (
	// defaultExternalMailWorkerBatch is the most due connections one pass fetches.
	defaultExternalMailWorkerBatch = 20
	// defaultExternalMailWorkerPoll is the wait between passes that found nothing due; each
	// connection is polled at most once per two minutes regardless.
	defaultExternalMailWorkerPoll = 30 * time.Second
	maxExternalMailWorkerPoll     = 10 * time.Minute
	// defaultExternalMailPushWorkerPoll is the wait between idle passes while push subscriptions are
	// on: a notification makes its connection due at the start of the next pass, so this bounds the
	// delay from a notification to its sync.
	defaultExternalMailPushWorkerPoll = 2 * time.Second
)

var errExternalMailRuntimeUnavailable = errors.New("external mail runtime is unavailable")

// MailExternalSyncCommand copies new mail from bound external accounts into their read-only
// external mailboxes.
type MailExternalSyncCommand struct {
	output *console.Output
	// loadConfig reads the configuration; tests replace it.
	loadConfig func() (*config.Config, error)
}

func NewMailExternalSyncCommand() *MailExternalSyncCommand {
	return &MailExternalSyncCommand{output: console.NewOutput(), loadConfig: config.Load}
}

func (c *MailExternalSyncCommand) Name() string { return "mail:external:sync" }
func (c *MailExternalSyncCommand) Description() string {
	return "Sync bound external mailboxes (EXTERNAL_MAIL_PROVIDERS); exits at once while no provider is enabled"
}
func (c *MailExternalSyncCommand) Usage() string {
	return "mail:external:sync [--batch=20] [--poll=30s] [--once]"
}

// externalMailWorker adapts the sync worker to the shared worker loop.
type externalMailWorker struct {
	worker domain.ExternalMailSyncWorker
}

func (w externalMailWorker) DispatchWebhooks(ctx context.Context, limit int) (int, error) {
	return w.worker.SyncExternalMail(ctx, limit)
}

func parseExternalMailWorkerArgs(args []string) (webhookWorkerConfig, error) {
	return parseWorkerArgs("external mail worker", args,
		webhookWorkerConfig{Batch: defaultExternalMailWorkerBatch, Poll: defaultExternalMailWorkerPoll}, maxExternalMailWorkerPoll)
}

// externalMailWorkerPoll is the idle wait: the --poll the operator gave, or, without one, a short
// wait while push subscriptions are on, so a notification is synced within seconds.
func externalMailWorkerPoll(args []string, parsed time.Duration, external config.ExternalMailConfig) time.Duration {
	for _, arg := range args {
		if arg == "--poll" || strings.HasPrefix(arg, "--poll=") {
			return parsed
		}
	}
	if external.PushActive() {
		return defaultExternalMailPushWorkerPoll
	}
	return parsed
}

func (c *MailExternalSyncCommand) Run(args []string) error {
	workerConfig, err := parseExternalMailWorkerArgs(args)
	if err != nil {
		return err
	}
	cfg, err := c.loadConfig()
	if err != nil {
		return err
	}
	workerConfig.Poll = externalMailWorkerPoll(args, workerConfig.Poll, cfg.ExternalMail)
	if !cfg.ExternalMail.Enabled() {
		slog.Info("external_mail.worker_disabled", "reason", "EXTERNAL_MAIL_PROVIDERS is empty")
		c.output.Info("External mail is off (EXTERNAL_MAIL_PROVIDERS is empty); nothing to sync")
		return nil
	}
	application, cfg, err := initMailboxApplication()
	if err != nil {
		return err
	}
	if application.ExternalMail == nil {
		return errExternalMailRuntimeUnavailable
	}
	tracerProvider := bootstrap.InitTracing(cfg, application.DB)
	defer bootstrap.ShutdownTracing(tracerProvider)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("external_mail.worker_started", "batch", workerConfig.Batch, "poll", workerConfig.Poll.String(),
		"source", cfg.ExternalMail.Source())
	processed, err := runWebhookWorker(ctx, externalMailWorker{worker: application.ExternalMail}, workerConfig)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	c.output.Success("External mail worker stopped after handling %d connections", processed)
	return nil
}
