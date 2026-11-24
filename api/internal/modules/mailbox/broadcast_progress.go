package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/broadcast"
)

// broadcastRateWindow is how far back the send rate of a broadcast is measured.
const broadcastRateWindow = broadcast.RateWindow

// progressInput is what estimateProgress needs, all of it from data the API holds.
type progressInput struct {
	start       time.Time
	pending     int64
	rate        *float64
	limit       int64
	used        int64
	slotFreesAt func(n int64) (*time.Time, error)
}

type progressEstimate struct {
	finish      *time.Time
	limitPauses bool
	resumesAt   *time.Time
}

func estimateProgress(in progressInput) (progressEstimate, error) {
	estimate, err := broadcast.EstimateProgress(broadcast.ProgressInput{
		Start:       in.start,
		Pending:     in.pending,
		Rate:        in.rate,
		Limit:       in.limit,
		Used:        in.used,
		SlotFreesAt: in.slotFreesAt,
	})
	if err != nil {
		return progressEstimate{}, err
	}
	return progressEstimate{
		finish:      estimate.Finish,
		limitPauses: estimate.LimitPauses,
		resumesAt:   estimate.ResumesAt,
	}, nil
}

func measuredRate(finished int64, startedAt *time.Time, now time.Time) *float64 {
	return broadcast.MeasuredRate(finished, startedAt, now)
}

// BroadcastProgress estimates the send rate and finish of a scheduled, sending, or paused
// broadcast; nil for any other. A paused broadcast that waits for a person or the health check
// has no estimate; a test wave waiting out its wait is estimated from its resume time.
func (s *service) BroadcastProgress(ctx context.Context, mailBroadcast *domain.MailBroadcast) (*domain.MailBroadcastProgress, error) {
	now := s.now().UTC()
	progress := &domain.MailBroadcastProgress{RateWindow: broadcastRateWindow, ComputedAt: now}
	start := now
	switch mailBroadcast.Status {
	case domain.MailBroadcastSending:
	case domain.MailBroadcastScheduled:
		if mailBroadcast.ScheduledAt != nil && mailBroadcast.ScheduledAt.After(now) {
			start = *mailBroadcast.ScheduledAt
		}
	case domain.MailBroadcastPaused:
		if mailBroadcast.StopReason != stopReasonTestWave {
			return progress, nil
		}
		if mailBroadcast.ResumeAt != nil && mailBroadcast.ResumeAt.After(now) {
			start = *mailBroadcast.ResumeAt
		}
	default:
		return nil, nil
	}
	if mailBroadcast.Status == domain.MailBroadcastSending {
		finished, err := s.broadcasts.CountRecipientsFinishedSince(ctx, mailBroadcast.ID, now.Add(-broadcastRateWindow))
		if err != nil {
			return nil, err
		}
		progress.RatePerMinute = measuredRate(finished, mailBroadcast.StartedAt, now)
	}
	limit, err := s.organizationLaneLimit(ctx, mailBroadcast.OrganizationID, domain.SendLaneBroadcast)
	if err != nil {
		return nil, err
	}
	since := now.Add(-sendLimitWindow)
	used, err := s.repo.CountOutboundSince(ctx, mailBroadcast.OrganizationID, domain.SendLaneBroadcast, since)
	if err != nil {
		return nil, err
	}
	estimate, err := estimateProgress(progressInput{
		start:   start,
		pending: int64(mailBroadcast.Counts.Pending),
		rate:    progress.RatePerMinute,
		limit:   int64(limit),
		used:    used,
		slotFreesAt: func(n int64) (*time.Time, error) {
			at, lookupErr := s.broadcasts.NthBroadcastAttemptSince(ctx, mailBroadcast.OrganizationID, since, n)
			if lookupErr != nil || at == nil {
				return nil, lookupErr
			}
			freed := at.Add(sendLimitWindow)
			return &freed, nil
		},
	})
	if err != nil {
		return nil, err
	}
	progress.EstimatedFinishAt = estimate.finish
	progress.SendLimitPauses = estimate.limitPauses
	progress.SendLimitResumesAt = estimate.resumesAt
	return progress, nil
}
