package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// triageMaxAttempts is how many failed model calls end a row as failed (review).
	triageMaxAttempts = 3
	// triageUnavailableDelay is the wait when the model reports it is unavailable; it uses no
	// attempt.
	triageUnavailableDelay = 30 * time.Minute
	// triageCallTimeout bounds one model call.
	triageCallTimeout = 30 * time.Second
	// triageFailureCode is the stored code of a failed model call; the provider's error text is
	// never stored.
	triageFailureCode = "model.failed"
)

// triageRetryDelays are the waits after the 1st and 2nd failed model call.
var triageRetryDelays = []time.Duration{time.Minute, 5 * time.Minute}

var _ domain.MailTriageWorker = (*service)(nil)

// DispatchTriage runs one worker pass: it claims up to limit due pending rows and decides each.
// It returns how many rows it claimed.
func (s *service) DispatchTriage(ctx context.Context, limit int) (int, error) {
	if s.triage == nil || limit <= 0 {
		return 0, nil
	}
	now := s.now().UTC()
	claims, err := s.triage.ClaimTriage(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return len(claims), err
		}
		if err := s.triageOne(ctx, claim, now); err != nil {
			return len(claims), err
		}
	}
	return len(claims), nil
}

// triageOne decides one claimed row: skipped when a switch is now off, deferred when no model is
// available or the day's cap is used, otherwise classified.
func (s *service) triageOne(ctx context.Context, claim domain.MailTriageClaim, now time.Time) error {
	triage := claim.Triage
	switch {
	case !claim.OrganizationOn || !claim.MailboxOn || (claim.MailboxPersonal && claim.MailboxOwnerLeft):
		triage.Status, triage.NextAttemptAt = domain.MailTriageSkipped, now
		return s.saveTriage(ctx, triage, "skipped")
	case !s.settings.Triage.Available():
		triage.NextAttemptAt = now.Add(triageUnavailableDelay)
		return s.saveTriage(ctx, triage, "deferred")
	}

	limit := min(claim.OrganizationCap, s.settings.Triage.DailyCap)
	reserved, err := s.triage.ReserveTriageCall(ctx, claim.OrganizationID, limit, now)
	if err != nil {
		return err
	}
	if !reserved {
		triage.NextAttemptAt = nextUTCDay(now)
		return s.saveTriage(ctx, triage, "cap_reached")
	}

	message, err := s.repo.FindMessage(ctx, triage.MailboxID, triage.MessageID)
	if errors.Is(err, ErrMailMessageNotFound) {
		triage.Status, triage.NextAttemptAt = domain.MailTriageSkipped, now
		return s.saveTriage(ctx, triage, "skipped")
	}
	if err != nil {
		return err
	}
	examples, err := s.triage.RecentTriageExamples(ctx, triage.MailboxID, triageExampleLimit)
	if err != nil {
		return err
	}
	callContext, cancel := context.WithTimeout(ctx, triageCallTimeout)
	output, err := s.triageModel.Classify(callContext, buildTriageInput(message, claim.MailboxRoleName, examples))
	cancel()
	switch {
	case errors.Is(err, ErrTriageUnavailable):
		triage.NextAttemptAt = now.Add(triageUnavailableDelay)
		return s.saveTriage(ctx, triage, "deferred")
	case err != nil:
		triage.Attempts++
		triage.LastErrorCode = triageFailureCode
		if triage.Attempts >= triageMaxAttempts {
			triage.Status, triage.Category, triage.Reason, triage.NextAttemptAt = domain.MailTriageFailed, domain.MailTriageReview, "", now
			return s.saveTriage(ctx, triage, "failed")
		}
		triage.NextAttemptAt = now.Add(triageRetryDelays[triage.Attempts-1])
		return s.saveTriage(ctx, triage, "retry")
	}
	triage.Attempts++
	triage.Category, triage.Reason, triage.DueAt = validateTriageOutput(output, now)
	triage.Status, triage.Model, triage.LastErrorCode, triage.NextAttemptAt =
		domain.MailTriageDone, s.settings.Triage.ModelLabel(), "", now
	return s.saveTriage(ctx, triage, "classified")
}

// saveTriage stores the outcome and logs it without any message content.
func (s *service) saveTriage(ctx context.Context, triage domain.MailTriage, outcome string) error {
	if err := s.triage.SaveTriage(ctx, triage); err != nil {
		return err
	}
	slog.InfoContext(ctx, "mail_triage.decided",
		slog.Uint64("message_id", uint64(triage.MessageID)),
		slog.Uint64("mailbox_id", uint64(triage.MailboxID)),
		slog.String("outcome", outcome),
		slog.String("category", string(triage.Category)),
		slog.Int("attempts", triage.Attempts),
	)
	return nil
}

// nextUTCDay is the next UTC midnight after now.
func nextUTCDay(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}
