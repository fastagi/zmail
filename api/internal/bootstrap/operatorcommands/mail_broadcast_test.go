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
	purgeLimit int
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
	if len(dispatcher.calls) != 2 || dispatcher.calls[0] != "queued" || dispatcher.calls[1] != "broadcasts" {
		t.Fatalf("calls = %v; want queued sends, then broadcasts", dispatcher.calls)
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
	if len(dispatcher.calls) != 3 || dispatcher.calls[2] != "purge" || dispatcher.purgeLimit != 18 {
		t.Fatalf("calls = %v, purge limit %d; want queued, broadcasts, then purge with 18", dispatcher.calls, dispatcher.purgeLimit)
	}
	if processed != 25 {
		t.Fatalf("processed = %d; want 3 + 4 + 18", processed)
	}

	busy := &recordingMailDispatcher{queued: 25, purged: 100}
	if _, err := (mailBroadcastWorker{dispatcher: busy}).DispatchWebhooks(context.Background(), 25); err != nil {
		t.Fatalf("DispatchWebhooks: %v", err)
	}
	if len(busy.calls) != 1 {
		t.Fatalf("calls = %v; want only queued sends when they fill the batch", busy.calls)
	}
}
