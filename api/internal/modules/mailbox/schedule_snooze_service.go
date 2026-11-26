package mailbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// minScheduleLead is the earliest a scheduled send may be set ahead of now.
	minScheduleLead = time.Minute
	// maxScheduleAhead bounds how far ahead a single email may be scheduled.
	maxScheduleAhead = 365 * 24 * time.Hour
	// maxSnoozeAhead bounds how far ahead an inbox message may be snoozed.
	maxSnoozeAhead = 365 * 24 * time.Hour
)

var _ domain.MailSnoozeWaker = (*service)(nil)

// validateScheduledAt verifies that a non-nil schedule time is at least 1 minute in the future
// and at most 365 days ahead, returning its normalized UTC copy.
func validateScheduledAt(now time.Time, scheduledAt *time.Time) (*time.Time, error) {
	if scheduledAt == nil {
		return nil, nil
	}
	at := scheduledAt.UTC()
	if at.Before(now.Add(minScheduleLead)) || at.After(now.Add(maxScheduleAhead)) {
		return nil, fmt.Errorf("%w: scheduled_at", ErrMailMessageInvalid)
	}
	return &at, nil
}

// queueForSchedule stores an outbound message as queued with reason "scheduled" without calling
// the mail transport yet; the mail worker dispatches it once scheduled_at is reached.
func (s *service) queueForSchedule(
	ctx context.Context,
	mailbox *domain.Mailbox,
	message *domain.MailMessage,
) (*domain.MailMessage, bool, error) {
	message.Status = domain.MailStatusQueued
	message.QueuedReason = domain.MailQueuedForSchedule
	message.SendAttemptedAt = nil
	fingerprint := message.IdempotencyFingerprint
	created, err := s.repo.CreateMessage(ctx, message)
	if err != nil {
		return nil, false, err
	}
	if !created {
		replayed, replayErr := replayOutbound(mailbox, message, fingerprint)
		return replayed, false, replayErr
	}
	// The stored copy keeps the attachments for the worker; the response carries metadata only.
	for i := range message.Attachments {
		message.Attachments[i].Content = nil
	}
	return message, true, nil
}

// WakeDueSnoozedMessages clears snoozed_until and marks unread every inbox message whose snooze
// deadline has elapsed at now.
func (s *service) WakeDueSnoozedMessages(ctx context.Context, now time.Time) (int, error) {
	if s.snooze == nil {
		return 0, nil
	}
	woken, err := s.snooze.WakeDueSnoozedMessages(ctx, now.UTC())
	if err != nil {
		return 0, err
	}
	if woken > 0 {
		slog.InfoContext(ctx, "mail_snooze.woken", slog.Int("count", woken))
	}
	return woken, nil
}

// wakeSnoozedOpportunistically runs a cheap partial-index wake before inbox reads so messages
// whose snooze window just elapsed appear immediately as unread.
func (s *service) wakeSnoozedOpportunistically(ctx context.Context) {
	if s.snooze == nil {
		return
	}
	if _, err := s.WakeDueSnoozedMessages(ctx, s.now().UTC()); err != nil {
		slog.WarnContext(ctx, "mail_snooze.opportunistic_wake_failed", slog.String("error", err.Error()))
	}
}
