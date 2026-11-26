package operatorcommands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fastagi/zmail/api/internal/app"
	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/console"
	"github.com/fastagi/zmail/api/internal/wiring"
)

var errMailBroadcastRuntimeUnavailable = errors.New("mail broadcast runtime is unavailable")

const (
	defaultMailWorkerConcurrency = 1
	maxMailWorkerConcurrency     = 32
)

// mailWorkerLanes selects which mail queues a worker process drains.
// The zero value enables every lane so existing callers stay backwards-compatible.
type mailWorkerLanes struct {
	configured    bool
	Transactional bool
	Personal      bool
	Broadcast     bool
	Purge         bool
}

func allMailWorkerLanes() mailWorkerLanes {
	return mailWorkerLanes{
		configured:    true,
		Transactional: true,
		Personal:      true,
		Broadcast:     true,
		Purge:         true,
	}
}

func (l mailWorkerLanes) resolved() mailWorkerLanes {
	if !l.configured {
		return allMailWorkerLanes()
	}
	return l
}

func (l mailWorkerLanes) String() string {
	r := l.resolved()
	if r.Transactional && r.Personal && r.Broadcast && r.Purge {
		return "all"
	}
	var names []string
	if r.Transactional {
		names = append(names, "transactional")
	}
	if r.Personal {
		names = append(names, "personal")
	}
	if r.Broadcast {
		names = append(names, "broadcast")
	}
	if r.Purge {
		names = append(names, "purge")
	}
	return strings.Join(names, ",")
}

func parseMailWorkerLanes(raw string) (mailWorkerLanes, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" || trimmed == "all" {
		return allMailWorkerLanes(), nil
	}
	lanes := mailWorkerLanes{configured: true}
	for _, part := range strings.Split(trimmed, ",") {
		switch strings.TrimSpace(part) {
		case "transactional":
			lanes.Transactional = true
		case "personal", "queued":
			lanes.Personal = true
		case "broadcast", "broadcasts":
			lanes.Broadcast = true
		case "purge":
			lanes.Purge = true
		default:
			return mailWorkerLanes{}, fmt.Errorf("unknown --lanes value %q (expected all or comma-separated transactional,personal,broadcast,purge)", part)
		}
	}
	if !lanes.Transactional && !lanes.Personal && !lanes.Broadcast && !lanes.Purge {
		return mailWorkerLanes{}, fmt.Errorf("--lanes must enable at least one lane")
	}
	return lanes, nil
}

type mailBroadcastWorkerArgs struct {
	worker      webhookWorkerConfig
	lanes       mailWorkerLanes
	concurrency int
}

func parseMailBroadcastWorkerArgs(args []string) (mailBroadcastWorkerArgs, error) {
	result := mailBroadcastWorkerArgs{
		lanes:       allMailWorkerLanes(),
		concurrency: defaultMailWorkerConcurrency,
	}
	forwarded := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--lanes" && i+1 < len(args):
			i++
			parsed, err := parseMailWorkerLanes(args[i])
			if err != nil {
				return result, err
			}
			result.lanes = parsed
		case strings.HasPrefix(arg, "--lanes="):
			parsed, err := parseMailWorkerLanes(strings.TrimPrefix(arg, "--lanes="))
			if err != nil {
				return result, err
			}
			result.lanes = parsed
		case arg == "--concurrency" && i+1 < len(args):
			i++
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return result, fmt.Errorf("invalid --concurrency value %q: %w", args[i], err)
			}
			result.concurrency = value
		case strings.HasPrefix(arg, "--concurrency="):
			value, err := strconv.Atoi(strings.TrimPrefix(arg, "--concurrency="))
			if err != nil {
				return result, fmt.Errorf("invalid --concurrency value: %w", err)
			}
			result.concurrency = value
		default:
			forwarded = append(forwarded, arg)
		}
	}
	if result.concurrency < 1 || result.concurrency > maxMailWorkerConcurrency {
		return result, fmt.Errorf("--concurrency must be between 1 and %d", maxMailWorkerConcurrency)
	}
	workerCfg, err := parseWebhookWorkerArgs(forwarded)
	if err != nil {
		return result, err
	}
	result.worker = workerCfg
	return result, nil
}

// MailBroadcastWorkCommand sends transactional mail, the recipients of running broadcasts, and the
// direct sends that waited for room in the send limit.
type MailBroadcastWorkCommand struct {
	output *console.Output
}

func NewMailBroadcastWorkCommand() *MailBroadcastWorkCommand {
	return &MailBroadcastWorkCommand{output: console.NewOutput()}
}

func (c *MailBroadcastWorkCommand) Name() string { return "mail:broadcast:work" }
func (c *MailBroadcastWorkCommand) Description() string {
	return "Send transactional and queued mail and the recipients of scheduled and running mail broadcasts, and purge expired trash, transactional mail, and agent mail events and audit"
}
func (c *MailBroadcastWorkCommand) Usage() string {
	return "mail:broadcast:work [--batch=25] [--poll=2s] [--lanes=all] [--concurrency=1] [--once]"
}

// mailBroadcastWorker adapts the dispatcher to the shared worker loop.
type mailBroadcastWorker struct {
	dispatcher domain.MailBroadcastDispatcher
	// agents prunes agent feed events and audit rows past retention; nil skips the step.
	agents domain.MailAgentEventPruner
	lanes  mailWorkerLanes
}

// DispatchWebhooks runs one tick across enabled lanes in strict priority order: transactional mail
// first, then personal queued sends, broadcast recipients, and finally retention purges.
func (w mailBroadcastWorker) DispatchWebhooks(ctx context.Context, limit int) (int, error) {
	lanes := w.lanes.resolved()
	steps := make([]func(context.Context, int) (int, error), 0, 8)
	if lanes.Transactional {
		steps = append(steps, w.dispatcher.DispatchTransactional)
	}
	if lanes.Personal {
		steps = append(steps, w.dispatcher.DispatchQueuedSends)
		if waker, ok := w.dispatcher.(domain.MailSnoozeWaker); ok {
			steps = append(steps, func(ctx context.Context, _ int) (int, error) {
				return waker.WakeDueSnoozedMessages(ctx, time.Now().UTC())
			})
		}
	}
	if lanes.Broadcast {
		steps = append(steps, w.dispatcher.DispatchBroadcasts)
	}
	if lanes.Purge {
		steps = append(steps,
			func(ctx context.Context, limit int) (int, error) {
				return w.dispatcher.PurgeExpiredTrash(ctx, time.Now().UTC(), limit)
			},
			func(ctx context.Context, limit int) (int, error) {
				return w.dispatcher.PurgeExpiredTransactional(ctx, time.Now().UTC(), limit)
			},
			func(ctx context.Context, limit int) (int, error) {
				return w.dispatcher.PurgeRevokedExternalMail(ctx, time.Now().UTC(), limit)
			},
			func(ctx context.Context, limit int) (int, error) {
				if w.agents == nil {
					return 0, nil
				}
				return w.agents.PruneExpiredAgentMail(ctx, time.Now().UTC(), limit)
			},
			func(ctx context.Context, limit int) (int, error) {
				pruner, ok := w.dispatcher.(domain.MailAppPasswordPruner)
				if !ok {
					return 0, nil
				}
				return pruner.PurgeRevokedAppPasswords(ctx, time.Now().UTC(), limit)
			},
		)
	}
	processed := 0
	for _, step := range steps {
		if processed >= limit {
			break
		}
		done, err := step(ctx, limit-processed)
		processed += done
		if err != nil {
			return processed, err
		}
	}
	return processed, nil
}

// runConcurrentMailWorker runs concurrency independent worker loops sharing the same PostgreSQL
// SKIP LOCKED lease tables, draining cleanly when ctx is canceled.
func runConcurrentMailWorker(ctx context.Context, worker webhookWorker, cfg webhookWorkerConfig, concurrency int) (int, error) {
	if concurrency <= 1 {
		return runWebhookWorker(ctx, worker, cfg)
	}
	var total atomic.Int64
	var firstErr error
	var errMu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := runWebhookWorker(ctx, worker, cfg)
			total.Add(int64(n))
			if err != nil && !errors.Is(err, context.Canceled) {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return int(total.Load()), firstErr
	}
	return int(total.Load()), ctx.Err()
}

func (c *MailBroadcastWorkCommand) Run(args []string) error {
	parsed, err := parseMailBroadcastWorkerArgs(args)
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
	slog.Info("mail_broadcast.worker_started",
		"batch", parsed.worker.Batch,
		"poll", parsed.worker.Poll.String(),
		"lanes", parsed.lanes.String(),
		"concurrency", parsed.concurrency,
	)
	worker := mailBroadcastWorker{
		dispatcher: application.MailBroadcasts,
		agents:     application.MailAgentEvents,
		lanes:      parsed.lanes,
	}
	processed, err := runConcurrentMailWorker(ctx, worker, parsed.worker, parsed.concurrency)
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
