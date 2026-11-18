package operatorcommands

import (
	"context"
	"testing"
)

type recordingMailDispatcher struct {
	calls  []string
	queued int
	sent   int
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
