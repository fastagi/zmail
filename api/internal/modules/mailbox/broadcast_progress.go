package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// broadcastRateWindow is how far back the send rate of a broadcast is measured.
const broadcastRateWindow = 10 * time.Minute

// progressInput is what estimateProgress needs, all of it from data the API holds.
type progressInput struct {
	// start is when sending can go on: now, or the scheduled or resume time ahead.
	start   time.Time
	pending int64
	// rate is recipients per minute; nil when unknown.
	rate *float64
	// limit and used are the organization's broadcast lane limit and its attempts in the window.
	limit int64
	used  int64
	// slotFreesAt is when the n-th (0-based) oldest attempt in the window leaves it, or nil when
	// there are fewer attempts.
	slotFreesAt func(n int64) (*time.Time, error)
}

type progressEstimate struct {
	finish      *time.Time
	limitPauses bool
	resumesAt   *time.Time
}

// estimateProgress tells when a broadcast should finish and whether the 24-hour lane limit holds it
// on the way. It assumes the worker keeps up with the measured rate and that no other broadcast of
// the organization uses the lane meanwhile, so it is an estimate, not a promise.
//
// While the lane has room for every pending recipient, the finish is start + pending / rate.
// Otherwise sending pauses once the room is used, resumes when the oldest attempt leaves the
// window, and the k-th recipient over the room goes when the k-th oldest attempt leaves it. Past
// the attempts in the window today, each further day adds one limit's worth.
func estimateProgress(in progressInput) (progressEstimate, error) {
	if in.pending <= 0 {
		return progressEstimate{}, nil
	}
	known := in.rate != nil && *in.rate > 0
	sendTime := func(count int64) time.Duration {
		if !known {
			return 0
		}
		return time.Duration(float64(count) / *in.rate * float64(time.Minute))
	}
	room := max(0, in.limit-in.used)
	// Attempts over the limit (requests in flight may overshoot) must leave before any room opens.
	overshoot := max(0, in.used-in.limit)
	if in.pending <= room {
		if !known {
			return progressEstimate{}, nil
		}
		finish := in.start.Add(sendTime(in.pending))
		return progressEstimate{finish: &finish}, nil
	}

	pauseAt := in.start.Add(sendTime(room))
	resumesAt := in.start.Add(sendLimitWindow)
	first, err := in.slotFreesAt(overshoot)
	if err != nil {
		return progressEstimate{}, err
	}
	if first != nil {
		resumesAt = *first
	}
	resumesAt = latest(resumesAt, pauseAt)

	extra := in.pending - room
	oldSlots := in.used - overshoot
	var slot time.Time
	if extra <= oldSlots {
		freed, err := in.slotFreesAt(overshoot + extra - 1)
		if err != nil {
			return progressEstimate{}, err
		}
		slot = resumesAt
		if freed != nil {
			slot = latest(*freed, resumesAt)
		}
	} else {
		beyond := extra - oldSlots
		days := (beyond + in.limit - 1) / max(1, in.limit)
		// The broadcast's own first sends of today leave the window a day after it starts.
		base := in.start.Add(sendLimitWindow)
		if last, err := in.slotFreesAt(in.used - 1); err != nil {
			return progressEstimate{}, err
		} else if last != nil {
			base = latest(base, *last)
		}
		slot = base.Add(time.Duration(days-1) * sendLimitWindow).Add(sendTime(beyond - (days-1)*in.limit))
	}
	finish := latest(in.start.Add(sendTime(in.pending)), slot)
	return progressEstimate{finish: &finish, limitPauses: true, resumesAt: &resumesAt}, nil
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// measuredRate is recipients per minute from the finished count of the rate window, measured
// over the part of the window since the broadcast started and at least one minute; nil when none
// finished.
func measuredRate(finished int64, startedAt *time.Time, now time.Time) *float64 {
	if finished <= 0 {
		return nil
	}
	from := now.Add(-broadcastRateWindow)
	if startedAt != nil && startedAt.After(from) {
		from = *startedAt
	}
	minutes := max(now.Sub(from).Minutes(), 1)
	rate := float64(finished) / minutes
	return &rate
}

// BroadcastProgress estimates the send rate and finish of a scheduled, sending, or paused
// broadcast; nil for any other. A paused broadcast that waits for a person or the health check
// has no estimate; a test wave waiting out its wait is estimated from its resume time.
func (s *service) BroadcastProgress(ctx context.Context, broadcast *domain.MailBroadcast) (*domain.MailBroadcastProgress, error) {
	now := s.now().UTC()
	progress := &domain.MailBroadcastProgress{RateWindow: broadcastRateWindow, ComputedAt: now}
	start := now
	switch broadcast.Status {
	case domain.MailBroadcastSending:
	case domain.MailBroadcastScheduled:
		if broadcast.ScheduledAt != nil && broadcast.ScheduledAt.After(now) {
			start = *broadcast.ScheduledAt
		}
	case domain.MailBroadcastPaused:
		if broadcast.StopReason != stopReasonTestWave {
			return progress, nil
		}
		if broadcast.ResumeAt != nil && broadcast.ResumeAt.After(now) {
			start = *broadcast.ResumeAt
		}
	default:
		return nil, nil
	}
	if broadcast.Status == domain.MailBroadcastSending {
		finished, err := s.broadcasts.CountRecipientsFinishedSince(ctx, broadcast.ID, now.Add(-broadcastRateWindow))
		if err != nil {
			return nil, err
		}
		progress.RatePerMinute = measuredRate(finished, broadcast.StartedAt, now)
	}
	limit, err := s.organizationLaneLimit(ctx, broadcast.OrganizationID, domain.SendLaneBroadcast)
	if err != nil {
		return nil, err
	}
	since := now.Add(-sendLimitWindow)
	used, err := s.repo.CountOutboundSince(ctx, broadcast.OrganizationID, domain.SendLaneBroadcast, since)
	if err != nil {
		return nil, err
	}
	estimate, err := estimateProgress(progressInput{
		start:   start,
		pending: int64(broadcast.Counts.Pending),
		rate:    progress.RatePerMinute,
		limit:   int64(limit),
		used:    used,
		slotFreesAt: func(n int64) (*time.Time, error) {
			at, err := s.broadcasts.NthBroadcastAttemptSince(ctx, broadcast.OrganizationID, since, n)
			if err != nil || at == nil {
				return nil, err
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
