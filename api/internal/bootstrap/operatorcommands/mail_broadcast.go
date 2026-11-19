package operatorcommands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/fastagi/zmail/api/internal/app"
	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/console"
	"github.com/fastagi/zmail/api/internal/wiring"
)

var errMailBroadcastRuntimeUnavailable = errors.New("mail broadcast runtime is unavailable")

// MailBroadcastWorkCommand sends the recipients of running broadcasts and the direct sends that
// waited for room in the send limit.
type MailBroadcastWorkCommand struct {
	output *console.Output
}

func NewMailBroadcastWorkCommand() *MailBroadcastWorkCommand {
	return &MailBroadcastWorkCommand{output: console.NewOutput()}
}

func (c *MailBroadcastWorkCommand) Name() string { return "mail:broadcast:work" }
func (c *MailBroadcastWorkCommand) Description() string {
	return "Send queued mail and the recipients of scheduled and running mail broadcasts"
}
func (c *MailBroadcastWorkCommand) Usage() string {
	return "mail:broadcast:work [--batch=25] [--poll=2s] [--once]"
}

// mailBroadcastWorker adapts the dispatcher to the shared worker loop.
type mailBroadcastWorker struct {
	dispatcher domain.MailBroadcastDispatcher
}

// DispatchWebhooks runs one tick: messages that waited for the send limit go first, then
// broadcast recipients with the rest of the batch.
func (w mailBroadcastWorker) DispatchWebhooks(ctx context.Context, limit int) (int, error) {
	queued, err := w.dispatcher.DispatchQueuedSends(ctx, limit)
	if err != nil || queued >= limit {
		return queued, err
	}
	sent, err := w.dispatcher.DispatchBroadcasts(ctx, limit-queued)
	return queued + sent, err
}

func (c *MailBroadcastWorkCommand) Run(args []string) error {
	// The flags and the loop are those of webhook:work: batches, polling, and backoff on errors.
	workerConfig, err := parseWebhookWorkerArgs(args)
	if err != nil {
		return err
	}
	application, cfg, err := initMailboxApplication()
	if err != nil {
		return err
	}
	if application.MailBroadcasts == nil {
		return errMailBroadcastRuntimeUnavailable
	}
	tracerProvider := bootstrap.InitTracing(cfg, application.DB)
	defer bootstrap.ShutdownTracing(tracerProvider)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("mail_broadcast.worker_started", "batch", workerConfig.Batch, "poll", workerConfig.Poll.String())
	processed, err := runWebhookWorker(ctx, mailBroadcastWorker{dispatcher: application.MailBroadcasts}, workerConfig)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	c.output.Success("Mail broadcast worker stopped after sending %d messages", processed)
	return nil
}

func initMailboxApplication() (*app.Application, *config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if !cfg.Starters.Selected(config.StarterMailbox) {
		return nil, nil, fmt.Errorf("mailbox starter is not selected in OPTIONAL_STARTERS")
	}
	if loggerErr := bootstrap.InitLogger(cfg); loggerErr != nil {
		return nil, nil, loggerErr
	}
	application, err := wiring.InitApplicationWithConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize mailbox runtime: %w", err)
	}
	return application, cfg, nil
}
