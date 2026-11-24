package operatorcommands

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type scalingBenchWorker struct {
	remaining   atomic.Int64
	processed   atomic.Int64
	totalJobs   int64
	simulatedIO time.Duration
	cancel      context.CancelFunc
	mu          sync.Mutex
	latencies   []time.Duration
}

func (w *scalingBenchWorker) DispatchWebhooks(ctx context.Context, _ int) (int, error) {
	if ctx.Err() != nil {
		return 0, nil
	}
	if w.remaining.Add(-1) < 0 {
		w.cancel()
		return 0, nil
	}
	start := time.Now()
	time.Sleep(w.simulatedIO)
	lat := time.Since(start)
	w.mu.Lock()
	w.latencies = append(w.latencies, lat)
	w.mu.Unlock()
	if w.processed.Add(1) >= w.totalJobs {
		w.cancel()
	}
	return 1, nil
}

// TestMailWorkerConcurrencyScalingAndLaneIsolation measures throughput and P95 latency across
// concurrency levels (1, 4, 8, 16) under simulated provider I/O latency with the race detector on.
func TestMailWorkerConcurrencyScalingAndLaneIsolation(t *testing.T) {
	const totalJobs = 64
	const simulatedIO = 5 * time.Millisecond

	runLevel := func(concurrency int) (elapsed time.Duration, throughput float64, p95 time.Duration, processed int) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		w := &scalingBenchWorker{
			totalJobs:   totalJobs,
			simulatedIO: simulatedIO,
			cancel:      cancel,
			latencies:   make([]time.Duration, 0, totalJobs),
		}
		w.remaining.Store(totalJobs)

		start := time.Now()
		cfg := webhookWorkerConfig{Batch: 1, Poll: time.Millisecond, Once: false}
		var err error
		if concurrency <= 1 {
			processed, err = runWebhookWorker(ctx, w, cfg)
		} else {
			processed, err = runConcurrentMailWorker(ctx, w, cfg, concurrency)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("concurrency=%d failed: %v", concurrency, err)
		}
		elapsed = time.Since(start)
		throughput = float64(totalJobs) / elapsed.Seconds()
		slices.Sort(w.latencies)
		if len(w.latencies) > 0 {
			idx := int(float64(len(w.latencies)-1) * 0.95)
			p95 = w.latencies[idx]
		}
		return elapsed, throughput, p95, processed
	}

	e1, tp1, p95_1, n1 := runLevel(1)
	e4, tp4, p95_4, n4 := runLevel(4)
	e8, tp8, p95_8, n8 := runLevel(8)
	e16, tp16, p95_16, n16 := runLevel(16)

	t.Logf("CONCURRENCY=1  processed=%d elapsed=%v throughput=%.1f msg/s p95=%v", n1, e1.Round(time.Millisecond), tp1, p95_1.Round(time.Microsecond))
	t.Logf("CONCURRENCY=4  processed=%d elapsed=%v throughput=%.1f msg/s p95=%v (%.2fx)", n4, e4.Round(time.Millisecond), tp4, p95_4.Round(time.Microsecond), tp4/tp1)
	t.Logf("CONCURRENCY=8  processed=%d elapsed=%v throughput=%.1f msg/s p95=%v (%.2fx)", n8, e8.Round(time.Millisecond), tp8, p95_8.Round(time.Microsecond), tp8/tp1)
	t.Logf("CONCURRENCY=16 processed=%d elapsed=%v throughput=%.1f msg/s p95=%v (%.2fx)", n16, e16.Round(time.Millisecond), tp16, p95_16.Round(time.Microsecond), tp16/tp1)

	if n1 != totalJobs || n4 != totalJobs || n8 != totalJobs || n16 != totalJobs {
		t.Fatalf("unexpected processed counts: %d %d %d %d (want %d)", n1, n4, n8, n16, totalJobs)
	}
	if tp8 <= tp1*3 {
		t.Fatalf("expected >=3x throughput speedup at concurrency=8, got %.2fx (tp1=%.1f tp8=%.1f)", tp8/tp1, tp1, tp8)
	}
}
