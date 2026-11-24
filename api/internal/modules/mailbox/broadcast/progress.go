package broadcast

import "time"

// ProgressInput is what EstimateProgress needs, all of it from data the API holds.
type ProgressInput struct {
	// Start is when sending can go on: now, or the scheduled or resume time ahead.
	Start   time.Time
	Pending int64
	// Rate is recipients per minute; nil when unknown.
	Rate *float64
	// Limit and Used are the organization's broadcast lane limit and its attempts in the window.
	Limit int64
	Used  int64
	// SlotFreesAt is when the n-th (0-based) oldest attempt in the window leaves it, or nil when
	// there are fewer attempts.
	SlotFreesAt func(n int64) (*time.Time, error)
}

// ProgressEstimate is the estimated completion and send-limit pause state of a broadcast.
type ProgressEstimate struct {
	Finish      *time.Time
	LimitPauses bool
	ResumesAt   *time.Time
}

// EstimateProgress tells when a broadcast should finish and whether the 24-hour lane limit holds it
// on the way. It assumes the worker keeps up with the measured rate and that no other broadcast of
// the organization uses the lane meanwhile, so it is an estimate, not a promise.
//
// While the lane has room for every pending recipient, the finish is start + pending / rate.
// Otherwise sending pauses once the room is used, resumes when the oldest attempt leaves the
// window, and the k-th recipient over the room goes when the k-th oldest attempt leaves it. Past
// the attempts in the window today, each further day adds one limit's worth.
func EstimateProgress(in ProgressInput) (ProgressEstimate, error) {
	if in.Pending <= 0 {
		return ProgressEstimate{}, nil
	}
	known := in.Rate != nil && *in.Rate > 0
	sendTime := func(count int64) time.Duration {
		if !known {
			return 0
		}
		return time.Duration(float64(count) / *in.Rate * float64(time.Minute))
	}
	room := max(0, in.Limit-in.Used)
	// Attempts over the limit (requests in flight may overshoot) must leave before any room opens.
	overshoot := max(0, in.Used-in.Limit)
	if in.Pending <= room {
		if !known {
			return ProgressEstimate{}, nil
		}
		finish := in.Start.Add(sendTime(in.Pending))
		return ProgressEstimate{Finish: &finish}, nil
	}

	pauseAt := in.Start.Add(sendTime(room))
	resumesAt := in.Start.Add(SendLimitWindow)
	first, err := in.SlotFreesAt(overshoot)
	if err != nil {
		return ProgressEstimate{}, err
	}
	if first != nil {
		resumesAt = *first
	}
	resumesAt = latest(resumesAt, pauseAt)

	extra := in.Pending - room
	oldSlots := in.Used - overshoot
	var slot time.Time
	if extra <= oldSlots {
		freed, err := in.SlotFreesAt(overshoot + extra - 1)
		if err != nil {
			return ProgressEstimate{}, err
		}
		slot = resumesAt
		if freed != nil {
			slot = latest(*freed, resumesAt)
		}
	} else {
		beyond := extra - oldSlots
		days := (beyond + in.Limit - 1) / max(1, in.Limit)
		// The broadcast's own first sends of today leave the window a day after it starts.
		base := in.Start.Add(SendLimitWindow)
		if last, err := in.SlotFreesAt(in.Used - 1); err != nil {
			return ProgressEstimate{}, err
		} else if last != nil {
			base = latest(base, *last)
		}
		slot = base.Add(time.Duration(days-1) * SendLimitWindow).Add(sendTime(beyond - (days-1)*in.Limit))
	}
	finish := latest(in.Start.Add(sendTime(in.Pending)), slot)
	return ProgressEstimate{Finish: &finish, LimitPauses: true, ResumesAt: &resumesAt}, nil
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// MeasuredRate is recipients per minute from the finished count of the rate window, measured
// over the part of the window since the broadcast started and at least one minute; nil when none
// finished.
func MeasuredRate(finished int64, startedAt *time.Time, now time.Time) *float64 {
	if finished <= 0 {
		return nil
	}
	from := now.Add(-RateWindow)
	if startedAt != nil && startedAt.After(from) {
		from = *startedAt
	}
	minutes := max(now.Sub(from).Minutes(), 1)
	rate := float64(finished) / minutes
	return &rate
}
