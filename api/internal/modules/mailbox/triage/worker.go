package triage

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

const (
	maxAttempts      = 3
	unavailableDelay = 30 * time.Minute
	// CallTimeout bounds one AI triage model call.
	CallTimeout = 30 * time.Second
	failureCode = "model.failed"
)

var retryDelays = []time.Duration{time.Minute, 5 * time.Minute}

// Worker claims pending triage rows and classifies them with Model.
type Worker struct {
	store       domain.MailTriageRepository
	findMessage func(ctx context.Context, mailboxID, messageID uint) (*domain.MailMessage, error)
	errNotFound error
	model       Model
	cfg         config.AITriageConfig
	now         func() time.Time
	masker      SecretMasker
}

var _ domain.MailTriageWorker = (*Worker)(nil)

// NewWorker builds a Worker with narrow dependencies.
func NewWorker(
	store domain.MailTriageRepository,
	findMessage func(ctx context.Context, mailboxID, messageID uint) (*domain.MailMessage, error),
	errNotFound error,
	model Model,
	cfg config.AITriageConfig,
	now func() time.Time,
	masker SecretMasker,
) *Worker {
	return &Worker{
		store:       store,
		findMessage: findMessage,
		errNotFound: errNotFound,
		model:       model,
		cfg:         cfg,
		now:         now,
		masker:      masker,
	}
}

// DispatchTriage runs one worker pass: it claims up to limit due pending rows and decides each.
func (w *Worker) DispatchTriage(ctx context.Context, limit int) (int, error) {
	if w.store == nil || limit <= 0 {
		return 0, nil
	}
	now := w.now().UTC()
	claims, err := w.store.ClaimTriage(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return len(claims), err
		}
		if err := w.triageOne(ctx, claim, now); err != nil {
			return len(claims), err
		}
	}
	return len(claims), nil
}

func (w *Worker) triageOne(ctx context.Context, claim domain.MailTriageClaim, now time.Time) error {
	row := claim.Triage
	switch {
	case !claim.OrganizationOn || !claim.MailboxOn || (claim.MailboxPersonal && claim.MailboxOwnerLeft):
		row.Status, row.NextAttemptAt = domain.MailTriageSkipped, now
		return w.saveTriage(ctx, row, "skipped")
	case !w.cfg.Available():
		row.NextAttemptAt = now.Add(unavailableDelay)
		return w.saveTriage(ctx, row, "deferred")
	}

	limit := min(claim.OrganizationCap, w.cfg.DailyCap)
	reserved, err := w.store.ReserveTriageCall(ctx, claim.OrganizationID, limit, now)
	if err != nil {
		return err
	}
	if !reserved {
		row.NextAttemptAt = nextUTCDay(now)
		return w.saveTriage(ctx, row, "cap_reached")
	}

	message, err := w.findMessage(ctx, row.MailboxID, row.MessageID)
	if errors.Is(err, w.errNotFound) {
		row.Status, row.NextAttemptAt = domain.MailTriageSkipped, now
		return w.saveTriage(ctx, row, "skipped")
	}
	if err != nil {
		return err
	}
	examples, err := w.store.RecentTriageExamples(ctx, row.MailboxID, ExampleLimit)
	if err != nil {
		return err
	}
	callContext, cancel := context.WithTimeout(ctx, CallTimeout)
	output, err := w.model.Classify(callContext, BuildInput(message, claim.MailboxRoleName, examples, w.masker))
	cancel()
	switch {
	case errors.Is(err, ErrUnavailable):
		row.NextAttemptAt = now.Add(unavailableDelay)
		return w.saveTriage(ctx, row, "deferred")
	case err != nil:
		row.Attempts++
		row.LastErrorCode = failureCode
		if row.Attempts >= maxAttempts {
			row.Status, row.Category, row.Reason, row.NextAttemptAt = domain.MailTriageFailed, domain.MailTriageReview, "", now
			return w.saveTriage(ctx, row, "failed")
		}
		row.NextAttemptAt = now.Add(retryDelays[row.Attempts-1])
		return w.saveTriage(ctx, row, "retry")
	}
	row.Attempts++
	row.Category, row.Reason, row.DueAt = ValidateOutput(output, now, w.masker)
	row.Status, row.Model, row.LastErrorCode, row.NextAttemptAt =
		domain.MailTriageDone, w.cfg.ModelLabel(), "", now
	return w.saveTriage(ctx, row, "classified")
}

func (w *Worker) saveTriage(ctx context.Context, row domain.MailTriage, outcome string) error {
	if err := w.store.SaveTriage(ctx, row); err != nil {
		return err
	}
	slog.InfoContext(ctx, "mail_triage.decided",
		slog.Uint64("message_id", uint64(row.MessageID)),
		slog.Uint64("mailbox_id", uint64(row.MailboxID)),
		slog.String("outcome", outcome),
		slog.String("category", string(row.Category)),
		slog.Int("attempts", row.Attempts),
	)
	return nil
}

func nextUTCDay(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}
