package operatorcommands

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type recordingMailDispatcher struct {
	calls  []string
	queued int
	sent   int
	purged int
	// purgeLimit is the batch the last purge was given.
	purgeLimit    int
	transactional int
	txPurged      int
	// txPurgeLimit is the batch the last transactional purge was given.
	txPurgeLimit int
	// externalPurgeLimit is the batch the last purge of revoked external copies was given.
	externalPurgeLimit int
}

func (d *recordingMailDispatcher) PurgeRevokedExternalMail(_ context.Context, _ time.Time, limit int) (int, error) {
	d.calls = append(d.calls, "purge-external")
	d.externalPurgeLimit = limit
	return 0, nil
}

func (d *recordingMailDispatcher) DispatchTransactional(_ context.Context, limit int) (int, error) {
	d.calls = append(d.calls, "transactional")
	return min(d.transactional, limit), nil
}

func (d *recordingMailDispatcher) PurgeExpiredTransactional(_ context.Context, _ time.Time, limit int) (int, error) {
	d.calls = append(d.calls, "purge-transactional")
	d.txPurgeLimit = limit
	return min(d.txPurged, limit), nil
}

func (d *recordingMailDispatcher) PurgeExpiredTrash(_ context.Context, _ time.Time, limit int) (int, error) {
	d.calls = append(d.calls, "purge")
	d.purgeLimit = limit
	return min(d.purged, limit), nil
}

func (d *recordingMailDispatcher) DispatchQueuedSends(_ context.Context, limit int) (int, error) {
	d.calls = append(d.calls, "queued")
	return min(d.queued, limit), nil
}

func (d *recordingMailDispatcher) DispatchBroadcasts(_ context.Context, limit int) (int, error) {
	d.calls = append(d.calls, "broadcasts")
	return min(d.sent, limit), nil
}

// TestMailWorkerSendsQueuedMailBeforeBroadcasts keeps people's own mail ahead of campaigns: each
// tick sends what waited for the send limit first, then broadcast recipients with the rest of the
// batch.
func TestMailWorkerSendsQueuedMailBeforeBroadcasts(t *testing.T) {
	dispatcher := &recordingMailDispatcher{queued: 3, sent: 50}
	processed, err := mailBroadcastWorker{dispatcher: dispatcher}.DispatchWebhooks(context.Background(), 25)
	if err != nil {
		t.Fatalf("DispatchWebhooks: %v", err)
	}
	if len(dispatcher.calls) != 3 || dispatcher.calls[1] != "queued" || dispatcher.calls[2] != "broadcasts" {
		t.Fatalf("calls = %v; want transactional, queued sends, then broadcasts", dispatcher.calls)
	}
	if processed != 25 {
		t.Fatalf("processed = %d; want 3 queued + 22 broadcast recipients, the batch of 25", processed)
	}
}

// TestMailWorkerPurgesExpiredTrashWithTheRestOfTheBatch purges the trash last, with what the
// sends left of the batch, and skips it when the sends used the whole batch.
func TestMailWorkerPurgesExpiredTrashWithTheRestOfTheBatch(t *testing.T) {
	dispatcher := &recordingMailDispatcher{queued: 3, sent: 4, purged: 100}
	processed, err := mailBroadcastWorker{dispatcher: dispatcher}.DispatchWebhooks(context.Background(), 25)
	if err != nil {
		t.Fatalf("DispatchWebhooks: %v", err)
	}
	if len(dispatcher.calls) != 4 || dispatcher.calls[3] != "purge" || dispatcher.purgeLimit != 18 {
		t.Fatalf("calls = %v, purge limit %d; want transactional, queued, broadcasts, then purge with 18", dispatcher.calls, dispatcher.purgeLimit)
	}
	if processed != 25 {
		t.Fatalf("processed = %d; want 3 + 4 + 18", processed)
	}

	busy := &recordingMailDispatcher{queued: 25, purged: 100}
	if _, err := (mailBroadcastWorker{dispatcher: busy}).DispatchWebhooks(context.Background(), 25); err != nil {
		t.Fatalf("DispatchWebhooks: %v", err)
	}
	if len(busy.calls) != 2 {
		t.Fatalf("calls = %v; want only transactional and queued sends when they fill the batch", busy.calls)
	}
}

// TestMailWorkerDispatchesTransactionalFirst keeps one-time codes ahead of everything else: each
// tick sends transactional mail first, then queued sends, broadcasts, the trash purge, and last the
// transactional purge, each with what the batch has left.
func TestMailWorkerDispatchesTransactionalFirst(t *testing.T) {
	dispatcher := &recordingMailDispatcher{transactional: 2, queued: 3, sent: 4, purged: 5, txPurged: 100}
	processed, err := mailBroadcastWorker{dispatcher: dispatcher}.DispatchWebhooks(context.Background(), 25)
	if err != nil {
		t.Fatalf("DispatchWebhooks: %v", err)
	}
	want := []string{"transactional", "queued", "broadcasts", "purge", "purge-transactional"}
	if len(dispatcher.calls) != len(want) {
		t.Fatalf("calls = %v; want %v", dispatcher.calls, want)
	}
	for i := range want {
		if dispatcher.calls[i] != want[i] {
			t.Fatalf("calls = %v; want %v", dispatcher.calls, want)
		}
	}
	if dispatcher.txPurgeLimit != 11 || processed != 25 {
		t.Fatalf("transactional purge limit %d, processed %d; want 11 and 25", dispatcher.txPurgeLimit, processed)
	}

	busy := &recordingMailDispatcher{transactional: 30, queued: 3}
	processed, err = mailBroadcastWorker{dispatcher: busy}.DispatchWebhooks(context.Background(), 25)
	if err != nil || processed != 25 || len(busy.calls) != 1 {
		t.Fatalf("calls = %v, processed %d, %v; want only transactional when it fills the batch", busy.calls, processed, err)
	}
}

// TestMailWorkerPurgesRevokedExternalCopiesLast runs the retention of revoked external copies on
// every tick, with what the batch has left, independent of the external mail worker.
func TestMailWorkerPurgesRevokedExternalCopiesLast(t *testing.T) {
	dispatcher := &recordingMailDispatcher{transactional: 2, queued: 3, sent: 4, purged: 5, txPurged: 1}
	if _, err := (mailBroadcastWorker{dispatcher: dispatcher}).DispatchWebhooks(context.Background(), 25); err != nil {
		t.Fatalf("DispatchWebhooks: %v", err)
	}
	if last := dispatcher.calls[len(dispatcher.calls)-1]; last != "purge-external" || dispatcher.externalPurgeLimit != 10 {
		t.Fatalf("calls = %v, external purge limit %d; want purge-external last with 10", dispatcher.calls, dispatcher.externalPurgeLimit)
	}
}

// appPasswordDispatcher is a dispatcher that also prunes revoked mail client passwords.
type appPasswordDispatcher struct {
	recordingMailDispatcher
	prunedLimit int
}

func (d *appPasswordDispatcher) PurgeRevokedAppPasswords(_ context.Context, _ time.Time, limit int) (int, error) {
	d.calls = append(d.calls, "purge-app-passwords")
	d.prunedLimit = limit
	return 1, nil
}

// TestMailWorkerPurgesRevokedAppPasswords purges revoked mail client passwords after the other
// steps, with what they left of the batch, when the dispatcher can.
func TestMailWorkerPurgesRevokedAppPasswords(t *testing.T) {
	dispatcher := &appPasswordDispatcher{recordingMailDispatcher: recordingMailDispatcher{queued: 3}}
	processed, err := mailBroadcastWorker{dispatcher: dispatcher}.DispatchWebhooks(context.Background(), 25)
	if err != nil {
		t.Fatalf("DispatchWebhooks: %v", err)
	}
	last := dispatcher.calls[len(dispatcher.calls)-1]
	if last != "purge-app-passwords" || dispatcher.prunedLimit != 22 || processed != 4 {
		t.Fatalf("calls = %v, limit %d, processed %d", dispatcher.calls, dispatcher.prunedLimit, processed)
	}
}

func TestParseMailBroadcastWorkerArgs(t *testing.T) {
	parsed, err := parseMailBroadcastWorkerArgs([]string{
		"--batch=50", "--poll=300ms", "--lanes=transactional,personal", "--concurrency=8", "--once",
	})
	if err != nil {
		t.Fatalf("parseMailBroadcastWorkerArgs: %v", err)
	}
	if parsed.worker.Batch != 50 || parsed.worker.Poll != 300*time.Millisecond || !parsed.worker.Once {
		t.Fatalf("unexpected worker config: %+v", parsed.worker)
	}
	if parsed.concurrency != 8 {
		t.Fatalf("concurrency = %d; want 8", parsed.concurrency)
	}
	if !parsed.lanes.Transactional || !parsed.lanes.Personal || parsed.lanes.Broadcast || parsed.lanes.Purge {
		t.Fatalf("lanes = %+v; want transactional+personal only", parsed.lanes)
	}
	if parsed.lanes.String() != "transactional,personal" {
		t.Fatalf("lanes.String() = %q", parsed.lanes.String())
	}

	for _, bad := range [][]string{
		{"--lanes=invalid"},
		{"--concurrency=0"},
		{"--concurrency=33"},
		{"--concurrency=abc"},
	} {
		if _, err := parseMailBroadcastWorkerArgs(bad); err == nil {
			t.Fatalf("expected error for args %v", bad)
		}
	}
}

func TestMailWorkerLaneIsolation(t *testing.T) {
	fastLanes, err := parseMailWorkerLanes("transactional,personal")
	if err != nil {
		t.Fatalf("parseMailWorkerLanes: %v", err)
	}
	fastDispatcher := &recordingMailDispatcher{transactional: 2, queued: 3, sent: 10, purged: 5}
	processed, err := mailBroadcastWorker{dispatcher: fastDispatcher, lanes: fastLanes}.DispatchWebhooks(context.Background(), 25)
	if err != nil {
		t.Fatalf("DispatchWebhooks fast lanes: %v", err)
	}
	if processed != 5 || len(fastDispatcher.calls) != 2 || fastDispatcher.calls[0] != "transactional" || fastDispatcher.calls[1] != "queued" {
		t.Fatalf("fast lanes calls = %v, processed = %d; want transactional + queued (5)", fastDispatcher.calls, processed)
	}

	slowLanes, err := parseMailWorkerLanes("broadcast,purge")
	if err != nil {
		t.Fatalf("parseMailWorkerLanes: %v", err)
	}
	slowDispatcher := &recordingMailDispatcher{transactional: 10, queued: 10, sent: 4, purged: 2}
	processed, err = mailBroadcastWorker{dispatcher: slowDispatcher, lanes: slowLanes}.DispatchWebhooks(context.Background(), 25)
	if err != nil {
		t.Fatalf("DispatchWebhooks slow lanes: %v", err)
	}
	if processed != 6 || len(slowDispatcher.calls) < 2 || slowDispatcher.calls[0] != "broadcasts" || slowDispatcher.calls[1] != "purge" {
		t.Fatalf("slow lanes calls = %v, processed = %d; want broadcasts + purge (6)", slowDispatcher.calls, processed)
	}
}

type concurrentTestWorker struct {
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
	barrier     chan struct{}
}

func (w *concurrentTestWorker) DispatchWebhooks(_ context.Context, _ int) (int, error) {
	cur := w.inFlight.Add(1)
	for {
		prev := w.maxInFlight.Load()
		if cur <= prev || w.maxInFlight.CompareAndSwap(prev, cur) {
			break
		}
	}
	<-w.barrier
	w.inFlight.Add(-1)
	return 3, nil
}

func TestRunConcurrentMailWorkerParallelism(t *testing.T) {
	worker := &concurrentTestWorker{barrier: make(chan struct{})}
	cfg := webhookWorkerConfig{Batch: 10, Poll: 10 * time.Millisecond, Once: true}

	done := make(chan struct{})
	var total int
	var runErr error
	go func() {
		total, runErr = runConcurrentMailWorker(context.Background(), worker, cfg, 4)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for worker.maxInFlight.Load() < 4 {
		select {
		case <-deadline:
			close(worker.barrier)
			t.Fatalf("expected 4 concurrent workers in flight, got max %d", worker.maxInFlight.Load())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	close(worker.barrier)
	<-done

	if runErr != nil {
		t.Fatalf("runConcurrentMailWorker error: %v", runErr)
	}
	if total != 12 {
		t.Fatalf("total = %d; want 12 (4 workers * 3)", total)
	}
}
