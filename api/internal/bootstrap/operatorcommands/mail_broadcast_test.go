package operatorcommands

import (
	"context"
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
