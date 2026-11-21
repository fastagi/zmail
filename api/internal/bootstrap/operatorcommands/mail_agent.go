package operatorcommands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/console"
)

var errMailAgentRuntimeUnavailable = errors.New("mail agent runtime is unavailable")

// MailAgentEventsPruneCommand deletes agent feed events past their retention
// (MAIL_AGENT_EVENT_RETENTION) at once; the mail:broadcast:work pass also prunes them, and the
// audit, in small batches.
type MailAgentEventsPruneCommand struct {
	output *console.Output
}

func NewMailAgentEventsPruneCommand() *MailAgentEventsPruneCommand {
	return &MailAgentEventsPruneCommand{output: console.NewOutput()}
}

func (c *MailAgentEventsPruneCommand) Name() string { return "mail:agent-events:prune" }
func (c *MailAgentEventsPruneCommand) Description() string {
	return "Delete agent mail feed events older than MAIL_AGENT_EVENT_RETENTION (30 days by default)"
}
func (c *MailAgentEventsPruneCommand) Usage() string {
	return "mail:agent-events:prune [--before=<RFC3339>]"
}

// mailAgentPruneBefore is --before when given, otherwise now minus the retention.
func mailAgentPruneBefore(args []string, now time.Time, retention time.Duration) (time.Time, error) {
	value, err := parseOptionalWebhookBefore(args)
	if err != nil {
		return time.Time{}, err
	}
	if value == "" {
		return now.UTC().Add(-retention), nil
	}
	before, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --before value %q: %w", value, err)
	}
	return before.UTC(), nil
}

func (c *MailAgentEventsPruneCommand) Run(args []string) error {
	application, cfg, err := initMailboxApplication()
	if err != nil {
		return err
	}
	before, err := mailAgentPruneBefore(args, time.Now(), cfg.MailAgent.Resolved().EventRetention)
	if err != nil {
		return err
	}
	if application.MailAgentEvents == nil {
		return errMailAgentRuntimeUnavailable
	}
	deleted, err := application.MailAgentEvents.PruneAgentMailEvents(context.Background(), before)
	if err != nil {
		return err
	}
	slog.Info("mail.agent_events_pruned", "before", before.Format(time.RFC3339), "deleted", deleted)
	c.output.Success("Deleted %d agent mail events created before %s", deleted, before.Format(time.RFC3339))
	return nil
}
