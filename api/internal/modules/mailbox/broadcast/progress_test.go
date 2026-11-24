package broadcast

import (
	"errors"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }

func TestEstimateProgressNoPendingOrFitsInRoom(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

	t.Run("zero pending returns empty estimate", func(t *testing.T) {
		est, err := EstimateProgress(ProgressInput{Start: start, Pending: 0, Limit: 100})
		if err != nil || est.Finish != nil || est.LimitPauses || est.ResumesAt != nil {
			t.Fatalf("unexpected estimate: %+v, err=%v", est, err)
		}
	})

	t.Run("fits in room with unknown rate returns empty estimate", func(t *testing.T) {
		est, err := EstimateProgress(ProgressInput{Start: start, Pending: 20, Limit: 100, Used: 50})
		if err != nil || est.Finish != nil || est.LimitPauses || est.ResumesAt != nil {
			t.Fatalf("unexpected estimate: %+v, err=%v", est, err)
		}
	})

	t.Run("fits in room with known rate estimates finish without pause", func(t *testing.T) {
		est, err := EstimateProgress(ProgressInput{
			Start:   start,
			Pending: 30,
			Rate:    floatPtr(10), // 30 / 10 = 3 minutes
			Limit:   100,
			Used:    50,
		})
		if err != nil {
			t.Fatalf("EstimateProgress failed: %v", err)
		}
		want := start.Add(3 * time.Minute)
		if est.LimitPauses || est.ResumesAt != nil || est.Finish == nil || !est.Finish.Equal(want) {
			t.Fatalf("got %+v, want Finish=%v LimitPauses=false", est, want)
		}
	})
}

func TestEstimateProgressLimitPauses(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

	t.Run("extra recipients fit within old slots", func(t *testing.T) {
		// Limit 100, Used 80 -> room = 20, oldSlots = 80. Pending 50 -> extra = 30 <= 80.
		firstFreed := start.Add(2 * time.Hour)
		lastNeededFreed := start.Add(5 * time.Hour)
		est, err := EstimateProgress(ProgressInput{
			Start:   start,
			Pending: 50,
			Rate:    floatPtr(10),
			Limit:   100,
			Used:    80,
			SlotFreesAt: func(n int64) (*time.Time, error) {
				switch n {
				case 0:
					return &firstFreed, nil
				case 29:
					return &lastNeededFreed, nil
				default:
					t.Fatalf("unexpected slot index %d", n)
					return nil, nil
				}
			},
		})
		if err != nil {
			t.Fatalf("EstimateProgress failed: %v", err)
		}
		if !est.LimitPauses || est.ResumesAt == nil || !est.ResumesAt.Equal(firstFreed) {
			t.Fatalf("expected LimitPauses=true ResumesAt=%v, got %+v", firstFreed, est)
		}
		if est.Finish == nil || !est.Finish.Equal(lastNeededFreed) {
			t.Fatalf("expected Finish=%v, got %+v", lastNeededFreed, est.Finish)
		}
	})

	t.Run("extra recipients span multiple days with overshoot", func(t *testing.T) {
		// Limit 100, Used 105 -> room = 0, overshoot = 5, oldSlots = 100.
		// Pending 250 -> extra = 250, beyond = 150 -> days = ceil(150/100) = 2.
		firstFreed := start.Add(1 * time.Hour)
		lastOldFreed := start.Add(4 * time.Hour)
		est, err := EstimateProgress(ProgressInput{
			Start:   start,
			Pending: 250,
			Rate:    floatPtr(50), // 50/min -> 50 remaining on day 2 = 1 minute
			Limit:   100,
			Used:    105,
			SlotFreesAt: func(n int64) (*time.Time, error) {
				switch n {
				case 5: // overshoot
					return &firstFreed, nil
				case 104: // Used - 1
					return &lastOldFreed, nil
				default:
					t.Fatalf("unexpected slot index %d", n)
					return nil, nil
				}
			},
		})
		if err != nil {
			t.Fatalf("EstimateProgress failed: %v", err)
		}
		// base = latest(start+24h, lastOldFreed) = start+24h
		// slot = base + (2-1)*24h + sendTime(150-100) = start + 48h + 1m
		wantFinish := start.Add(48*time.Hour + time.Minute)
		if !est.LimitPauses || est.ResumesAt == nil || !est.ResumesAt.Equal(firstFreed) {
			t.Fatalf("expected ResumesAt=%v, got %+v", firstFreed, est)
		}
		if est.Finish == nil || !est.Finish.Equal(wantFinish) {
			t.Fatalf("expected Finish=%v, got %v", wantFinish, est.Finish)
		}
	})

	t.Run("SlotFreesAt error propagates", func(t *testing.T) {
		wantErr := errors.New("db error")
		_, err := EstimateProgress(ProgressInput{
			Start:   start,
			Pending: 20,
			Limit:   10,
			Used:    10,
			SlotFreesAt: func(int64) (*time.Time, error) {
				return nil, wantErr
			},
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected %v, got %v", wantErr, err)
		}
	})
}

func TestMeasuredRate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 10, 0, 0, time.UTC)

	if rate := MeasuredRate(0, nil, now); rate != nil {
		t.Fatalf("expected nil rate for zero finished, got %v", *rate)
	}

	// Started 5 minutes ago -> 50 / 5 = 10/min.
	fiveMinAgo := now.Add(-5 * time.Minute)
	if rate := MeasuredRate(50, &fiveMinAgo, now); rate == nil || *rate != 10 {
		t.Fatalf("expected rate 10, got %v", rate)
	}

	// Started 10 seconds ago -> clamped to at least 1 minute -> 12 / 1 = 12/min.
	tenSecAgo := now.Add(-10 * time.Second)
	if rate := MeasuredRate(12, &tenSecAgo, now); rate == nil || *rate != 12 {
		t.Fatalf("expected rate 12, got %v", rate)
	}

	// Started earlier than RateWindow (10m) -> measured over full RateWindow -> 100 / 10 = 10/min.
	hourAgo := now.Add(-time.Hour)
	if rate := MeasuredRate(100, &hourAgo, now); rate == nil || *rate != 10 {
		t.Fatalf("expected rate 10, got %v", rate)
	}
}
