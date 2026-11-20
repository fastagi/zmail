package operatorcommands

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/console"
)

const (
	// defaultMailTriageWorkerBatch is the most pending messages one pass classifies.
	defaultMailTriageWorkerBatch = 20
	// defaultMailTriageWorkerPoll is the wait between passes that found nothing due.
	defaultMailTriageWorkerPoll = 5 * time.Second
	maxMailTriageWorkerPoll     = 10 * time.Minute
)

var errMailTriageRuntimeUnavailable = errors.New("mail triage runtime is unavailable")

// MailTriageWorkCommand classifies inbound mail queued for AI triage.
type MailTriageWorkCommand struct {
	output *console.Output
}

func NewMailTriageWorkCommand() *MailTriageWorkCommand {
	return &MailTriageWorkCommand{output: console.NewOutput()}
}

func (c *MailTriageWorkCommand) Name() string { return "mail:triage:work" }
func (c *MailTriageWorkCommand) Description() string {
	return "Classify inbound mail queued for AI triage with the configured model, within each organization's daily cap"
}
func (c *MailTriageWorkCommand) Usage() string {
	return "mail:triage:work [--batch=20] [--poll=5s] [--once]"
}

// mailTriageWorker adapts the triage worker to the shared worker loop.
type mailTriageWorker struct {
	worker domain.MailTriageWorker
}

func (w mailTriageWorker) DispatchWebhooks(ctx context.Context, limit int) (int, error) {
	return w.worker.DispatchTriage(ctx, limit)
}

func parseMailTriageWorkerArgs(args []string) (webhookWorkerConfig, error) {
	return parseWorkerArgs("mail triage worker", args,
		webhookWorkerConfig{Batch: defaultMailTriageWorkerBatch, Poll: defaultMailTriageWorkerPoll}, maxMailTriageWorkerPoll)
}

func (c *MailTriageWorkCommand) Run(args []string) error {
	workerConfig, err := parseMailTriageWorkerArgs(args)
	if err != nil {
		return err
	}
	application, cfg, err := initMailboxApplication()
	if err != nil {
		return err
	}
	if application.MailTriage == nil {
		return errMailTriageRuntimeUnavailable
	}
	tracerProvider := bootstrap.InitTracing(cfg, application.DB)
	defer bootstrap.ShutdownTracing(tracerProvider)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("mail_triage.worker_started", "batch", workerConfig.Batch, "poll", workerConfig.Poll.String(),
		"provider", cfg.AITriage.Provider)
	processed, err := runWebhookWorker(ctx, mailTriageWorker{worker: application.MailTriage}, workerConfig)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	c.output.Success("Mail triage worker stopped after handling %d messages", processed)
	return nil
}
