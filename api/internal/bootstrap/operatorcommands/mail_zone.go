package operatorcommands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/infra/console"
)

const (
	// defaultMailZoneWorkerBatch is the most pending zones one pass checks.
	defaultMailZoneWorkerBatch = 100
	// defaultMailZoneWorkerPoll is the wait between passes. Registrar delegations take hours to
	// days, and every pass makes a provider call per pending zone.
	defaultMailZoneWorkerPoll = 10 * time.Minute
	maxMailZoneWorkerPoll     = time.Hour
	// mailZoneSetupDrain bounds the wait for running domain setups when the worker exits. One
	// setup is bounded by two minutes; the next pass picks up a setup that is cut off.
	mailZoneSetupDrain = 2 * time.Minute
)

var errMailZoneRuntimeUnavailable = errors.New("mail zone runtime is unavailable")

// MailZoneWorkCommand activates and expires pending managed zones.
type MailZoneWorkCommand struct {
	output *console.Output
}

func NewMailZoneWorkCommand() *MailZoneWorkCommand {
	return &MailZoneWorkCommand{output: console.NewOutput()}
}

func (c *MailZoneWorkCommand) Name() string { return "mail:zone:work" }
func (c *MailZoneWorkCommand) Description() string {
	return "Activate managed mail zones the registrar now delegates and remove expired pending ones"
}
func (c *MailZoneWorkCommand) Usage() string {
	return "mail:zone:work [--batch=100] [--poll=10m] [--once]"
}

// mailZoneWorker adapts the zone checker to the shared worker loop. It always reports an empty
// batch: the loop runs again at once after a full batch, and the checker would then check the
// same zones again instead of waiting for the poll.
type mailZoneWorker struct {
	checker mailZonePoller
}

// mailZonePoller is the part of the zone runtime the worker needs.
type mailZonePoller interface {
	CheckPendingZones(ctx context.Context, limit int) (int, error)
	WaitForSetups(ctx context.Context) error
}

func (w mailZoneWorker) DispatchWebhooks(ctx context.Context, limit int) (int, error) {
	checked, err := w.checker.CheckPendingZones(ctx, limit)
	if err == nil {
		slog.InfoContext(ctx, "mail_zone.pass_finished", "checked", checked)
	}
	return 0, err
}

func parseMailZoneWorkerArgs(args []string) (webhookWorkerConfig, error) {
	return parseWorkerArgs("mail zone worker", args,
		webhookWorkerConfig{Batch: defaultMailZoneWorkerBatch, Poll: defaultMailZoneWorkerPoll}, maxMailZoneWorkerPoll)
}

func (c *MailZoneWorkCommand) Run(args []string) error {
	workerConfig, err := parseMailZoneWorkerArgs(args)
	if err != nil {
		return err
	}
	application, cfg, err := initMailboxApplication()
	if err != nil {
		return err
	}
	if application.MailZones == nil {
		return errMailZoneRuntimeUnavailable
	}
	tracerProvider := bootstrap.InitTracing(cfg, application.DB)
	defer bootstrap.ShutdownTracing(tracerProvider)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("mail_zone.worker_started", "batch", workerConfig.Batch, "poll", workerConfig.Poll.String())
	if err := runMailZoneWorker(ctx, application.MailZones, workerConfig); err != nil {
		return err
	}
	c.output.Success("Mail zone worker stopped")
	return nil
}

// runMailZoneWorker runs passes until ctx ends, or once, and then waits, for at most
// mailZoneSetupDrain, for the domain setups the passes started: they run in the background and
// would otherwise end with the process.
func runMailZoneWorker(ctx context.Context, checker mailZonePoller, workerConfig webhookWorkerConfig) error {
	_, err := runWebhookWorker(ctx, mailZoneWorker{checker: checker}, workerConfig)
	drainContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailZoneSetupDrain)
	defer cancel()
	if drainErr := checker.WaitForSetups(drainContext); drainErr != nil {
		slog.WarnContext(ctx, "mail_zone.setups_not_finished", "error", drainErr.Error())
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// mailZoneReleaser is the part of the zone runtime the release command needs.
type mailZoneReleaser interface {
	ReleasePendingZone(ctx context.Context, apex string, force bool) error
}

// MailZoneReleaseCommand frees an apex that a pending managed zone holds, such as one an
// organization claimed without ever delegating it.
type MailZoneReleaseCommand struct {
	output *console.Output
}

func NewMailZoneReleaseCommand() *MailZoneReleaseCommand {
	return &MailZoneReleaseCommand{output: console.NewOutput()}
}

func (c *MailZoneReleaseCommand) Name() string { return "mail:zone:release" }
func (c *MailZoneReleaseCommand) Description() string {
	return "Remove a pending managed mail zone from Cloudflare and Zmail; --force also removes its pending domains"
}
func (c *MailZoneReleaseCommand) Usage() string {
	return "mail:zone:release [--force] <apex>"
}

func parseMailZoneReleaseArgs(args []string) (string, bool, error) {
	zone, force := "", false
	for _, arg := range args {
		switch {
		case arg == "--force":
			force = true
		case strings.HasPrefix(arg, "-"):
			return "", false, fmt.Errorf("unknown mail zone release argument %q", arg)
		case zone != "":
			return "", false, errors.New("mail:zone:release takes exactly one zone")
		default:
			zone = arg
		}
	}
	if zone == "" {
		return "", false, errors.New("mail:zone:release needs the apex of the zone, such as example.com")
	}
	return zone, force, nil
}

// runMailZoneRelease releases the zone and records who-did-what in the log; the service logs the
// zone it removed.
func runMailZoneRelease(ctx context.Context, releaser mailZoneReleaser, zone string, force bool) error {
	slog.InfoContext(ctx, "mail_zone.release_requested", "zone", zone, "force", force)
	if err := releaser.ReleasePendingZone(ctx, zone, force); err != nil {
		return fmt.Errorf("release mail zone %s: %w", zone, err)
	}
	return nil
}

func (c *MailZoneReleaseCommand) Run(args []string) error {
	zone, force, err := parseMailZoneReleaseArgs(args)
	if err != nil {
		return err
	}
	application, _, err := initMailboxApplication()
	if err != nil {
		return err
	}
	if application.MailZones == nil {
		return errMailZoneRuntimeUnavailable
	}
	if err := runMailZoneRelease(context.Background(), application.MailZones, zone, force); err != nil {
		return err
	}
	c.output.Success("Released mail zone %s", zone)
	return nil
}
