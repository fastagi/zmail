package syncfeed

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readFrame(t *testing.T, ch <-chan StreamFrame, timeout time.Duration) StreamFrame {
	t.Helper()
	select {
	case frame, ok := <-ch:
		if !ok {
			t.Fatal("stream closed unexpectedly")
		}
		return frame
	case <-time.After(timeout):
		t.Fatalf("timed out after %v waiting for stream frame", timeout)
		return StreamFrame{}
	}
}

func TestHubReconnectReplayAndLiveStreaming(t *testing.T) {
	hub := NewHub(HubOptions{
		RingSize:          16,
		SubscriberBuffer:  4,
		HeartbeatInterval: -1,
	})
	defer hub.Close()

	// Publish initial events and capture cursor after seq 2.
	hub.Publish(ChangeEvent{OrganizationID: 1, Type: ChangeKindDeleteMessage, EventID: "e1", MessageID: 1})
	ev2 := hub.Publish(ChangeEvent{OrganizationID: 1, Type: ChangeKindDeleteMessage, EventID: "e2", MessageID: 2})
	cursorAfter2, err := EncodeCursor(Cursor{Seq: ev2.Seq, UpdatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("EncodeCursor() error = %v", err)
	}

	// Publish missed events while client is disconnected (more than SubscriberBuffer to exercise async replay).
	hub.Publish(ChangeEvent{OrganizationID: 2, Type: ChangeKindDeleteMessage, EventID: "other-org", MessageID: 99})
	hub.Publish(ChangeEvent{OrganizationID: 1, UserID: 20, Type: ChangeKindDeleteMessage, EventID: "other-user", MessageID: 98})
	for i := 3; i <= 8; i++ {
		userID := uint(0)
		if i == 5 {
			userID = 10
		}
		hub.Publish(ChangeEvent{
			OrganizationID: 1,
			UserID:         userID,
			Type:           ChangeKindDeleteMessage,
			EventID:        fmt.Sprintf("e%d", i),
			MessageID:      uint(i),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Reconnect as (org=1, user=10) from cursorAfter2.
	ch := hub.Subscribe(ctx, 1, 10, cursorAfter2)

	// Publish live events immediately while replay may still be draining.
	hub.Publish(ChangeEvent{OrganizationID: 1, Type: ChangeKindDeleteMessage, EventID: "e9", MessageID: 9})
	hub.Publish(ChangeEvent{OrganizationID: 1, UserID: 10, Type: ChangeKindDeleteMessage, EventID: "e10", MessageID: 10})

	wantIDs := []string{"e3", "e4", "e5", "e6", "e7", "e8", "e9", "e10"}
	var lastSeq uint64
	for idx, wantID := range wantIDs {
		frame := readFrame(t, ch, time.Second)
		if frame.Kind != FrameKindEvent || frame.Event == nil {
			t.Fatalf("frame[%d] = %+v, want event frame", idx, frame)
		}
		if frame.Event.EventID != wantID {
			t.Fatalf("frame[%d].EventID = %q, want %q", idx, frame.Event.EventID, wantID)
		}
		if frame.Event.Seq <= lastSeq {
			t.Fatalf("non-monotonic seq: got %d after %d", frame.Event.Seq, lastSeq)
		}
		lastSeq = frame.Event.Seq
		if cur, err := DecodeCursor(frame.Cursor); err != nil || cur.Seq != frame.Event.Seq {
			t.Fatalf("frame[%d] invalid cursor %q: err=%v cur=%+v", idx, frame.Cursor, err, cur)
		}
	}

	stats := hub.Stats()
	if stats.PublishedTotal != 12 {
		t.Fatalf("PublishedTotal = %d, want 12", stats.PublishedTotal)
	}
	if stats.ReplayedTotal != 6 {
		t.Fatalf("ReplayedTotal = %d, want 6", stats.ReplayedTotal)
	}
	if stats.ActiveSubscribers != 1 {
		t.Fatalf("ActiveSubscribers = %d, want 1", stats.ActiveSubscribers)
	}
}

func TestHubStaleAndInvalidCursorResetRequired(t *testing.T) {
	hub := NewHub(HubOptions{
		RingSize:          4,
		SubscriberBuffer:  8,
		HeartbeatInterval: -1,
	})
	defer hub.Close()

	var cursors []string
	for i := 1; i <= 7; i++ {
		ev := hub.Publish(ChangeEvent{
			OrganizationID: 10,
			Type:           ChangeKindDeleteMessage,
			EventID:        fmt.Sprintf("ev-%d", i),
			MessageID:      uint(i),
		})
		tok, err := EncodeCursor(Cursor{Seq: ev.Seq, UpdatedAt: time.Now().UTC()})
		if err != nil {
			t.Fatalf("EncodeCursor() error = %v", err)
		}
		cursors = append(cursors, tok)
	}
	// Ring size is 4, so ring retains seq 4, 5, 6, 7 and lastEvictedSeq is 3.

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cursor at seq 2 fell behind ring buffer -> must emit ResetRequired.
	staleCh := hub.Subscribe(ctx, 10, 0, cursors[1])
	resetFrame := readFrame(t, staleCh, time.Second)
	if resetFrame.Kind != FrameKindReset || !resetFrame.ResetRequired {
		t.Fatalf("stale cursor frame = %+v, want ResetRequired", resetFrame)
	}

	// Cursor at seq 3 equals lastEvictedSeq -> no events lost, replays 4..7 cleanly.
	boundaryCh := hub.Subscribe(ctx, 10, 0, cursors[2])
	for want := 4; want <= 7; want++ {
		f := readFrame(t, boundaryCh, time.Second)
		if f.Kind != FrameKindEvent || f.Event == nil || f.Event.Seq != uint64(want) {
			t.Fatalf("boundary replay frame = %+v, want seq %d", f, want)
		}
	}

	// Malformed cursor token -> must emit ResetRequired.
	badCh := hub.Subscribe(ctx, 10, 0, "not-a-valid-cursor")
	badFrame := readFrame(t, badCh, time.Second)
	if badFrame.Kind != FrameKindReset || !badFrame.ResetRequired {
		t.Fatalf("malformed cursor frame = %+v, want ResetRequired", badFrame)
	}

	// Future cursor (seq > latestSeq) -> must emit ResetRequired.
	futureTok, _ := EncodeCursor(Cursor{Seq: 999, UpdatedAt: time.Now().UTC()})
	futureCh := hub.Subscribe(ctx, 10, 0, futureTok)
	futureFrame := readFrame(t, futureCh, time.Second)
	if futureFrame.Kind != FrameKindReset || !futureFrame.ResetRequired {
		t.Fatalf("future cursor frame = %+v, want ResetRequired", futureFrame)
	}

	// After ResetRequired, live events still stream normally.
	hub.Publish(ChangeEvent{OrganizationID: 10, Type: ChangeKindDeleteMessage, EventID: "live-after-reset", MessageID: 8})
	live := readFrame(t, staleCh, time.Second)
	if live.Kind != FrameKindEvent || live.Event == nil || live.Event.EventID != "live-after-reset" {
		t.Fatalf("post-reset live frame = %+v, want live-after-reset", live)
	}
}

func TestHubSlowSubscriberDropsOldestWithoutBlockingFast(t *testing.T) {
	const bufSize = 4
	const totalEvents = 50

	hub := NewHub(HubOptions{
		RingSize:          128,
		SubscriberBuffer:  bufSize,
		HeartbeatInterval: -1,
	})
	defer hub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slowCh := hub.Subscribe(ctx, 42, 0, "")
	fastCh := hub.Subscribe(ctx, 42, 0, "")

	fastDone := make(chan []string, 1)
	go func() {
		got := make([]string, 0, totalEvents)
		for len(got) < totalEvents {
			f := <-fastCh
			if f.Event != nil {
				got = append(got, f.Event.EventID)
			}
		}
		fastDone <- got
	}()

	pubDone := make(chan struct{})
	go func() {
		for i := 1; i <= totalEvents; i++ {
			hub.Publish(ChangeEvent{
				OrganizationID: 42,
				Type:           ChangeKindDeleteMessage,
				EventID:        fmt.Sprintf("m-%d", i),
				MessageID:      uint(i),
			})
			// Yield briefly so the fast reader keeps up while the slow reader never reads.
			if i%2 == 0 {
				runtime.Gosched()
			}
		}
		close(pubDone)
	}()

	select {
	case <-pubDone:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}

	select {
	case fastGot := <-fastDone:
		if len(fastGot) != totalEvents || fastGot[0] != "m-1" || fastGot[totalEvents-1] != fmt.Sprintf("m-%d", totalEvents) {
			t.Fatalf("fast subscriber got %d events (%v..%v)", len(fastGot), fastGot[0], fastGot[len(fastGot)-1])
		}
	case <-time.After(time.Second):
		t.Fatal("fast subscriber timed out")
	}

	// Slow subscriber should retain only the newest bufSize events (drop-oldest).
	var slowGot []string
	for i := 0; i < bufSize; i++ {
		f := readFrame(t, slowCh, time.Second)
		slowGot = append(slowGot, f.Event.EventID)
	}
	for i := 0; i < bufSize; i++ {
		want := fmt.Sprintf("m-%d", totalEvents-bufSize+1+i)
		if slowGot[i] != want {
			t.Fatalf("slow subscriber got %v, want newest %d events starting at %s", slowGot, bufSize, want)
		}
	}

	if stats := hub.Stats(); stats.DroppedTotal < int64(totalEvents-bufSize) {
		t.Fatalf("DroppedTotal = %d, want >= %d", stats.DroppedTotal, totalEvents-bufSize)
	}
}

func TestHubHeartbeatBrokerForwardAndContextCancelNoLeak(t *testing.T) {
	hub := NewHub(HubOptions{
		RingSize:          32,
		SubscriberBuffer:  8,
		HeartbeatInterval: 15 * time.Millisecond,
	})
	defer hub.Close()

	// Pre-canceled context and invalid orgID return closed channels immediately.
	preCtx, preCancel := context.WithCancel(context.Background())
	preCancel()
	if _, ok := <-hub.Subscribe(preCtx, 1, 0, ""); ok {
		t.Fatal("expected closed channel for pre-canceled context")
	}
	if _, ok := <-hub.Subscribe(context.Background(), 0, 0, ""); ok {
		t.Fatal("expected closed channel for orgID == 0")
	}

	baseGoroutines := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	ch := hub.Subscribe(ctx, 7, 0, "")
	brokerCh := hub.Broker().Subscribe(ctx, 7)

	// Verify heartbeat frame arrives.
	hb := readFrame(t, ch, 500*time.Millisecond)
	if hb.Kind != FrameKindHeartbeat || hb.SentAt.IsZero() {
		t.Fatalf("expected heartbeat frame, got %+v", hb)
	}

	// Verify underlying Broker receives forwarded events.
	hub.Publish(ChangeEvent{OrganizationID: 7, Type: ChangeKindDeleteMessage, EventID: "bridged", MessageID: 77})
	select {
	case ev := <-brokerCh:
		if ev.EventID != "bridged" {
			t.Fatalf("broker got %q, want bridged", ev.EventID)
		}
	case <-time.After(time.Second):
		t.Fatal("broker subscriber timed out")
	}

	cancel()
	for range ch {
	}

	deadline := time.Now().Add(time.Second)
	for hub.Stats().ActiveSubscribers != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := hub.Stats().ActiveSubscribers; got != 0 {
		t.Fatalf("ActiveSubscribers = %d, want 0 after cancel", got)
	}

	deadline = time.Now().Add(time.Second)
	for runtime.NumGoroutine() > baseGoroutines+2 && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > baseGoroutines+2 {
		t.Fatalf("possible goroutine leak: before=%d after=%d", baseGoroutines, after)
	}
}

func TestHubStress50SubscribersFanOutLatencyAndNoLeaks(t *testing.T) {
	const (
		subscriberCount = 50
		eventCount      = 200
		maxAllowedP99   = 10 * time.Millisecond
	)

	baseGoroutines := runtime.NumGoroutine()
	hub := NewHub(HubOptions{
		RingSize:          DefaultRingSize,
		SubscriberBuffer:  eventCount + 32,
		HeartbeatInterval: -1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	var (
		wg        sync.WaitGroup
		latMu     sync.Mutex
		latencies = make([]time.Duration, 0, subscriberCount*eventCount)
		received  atomic.Int64
	)

	channels := make([]<-chan StreamFrame, subscriberCount)
	for i := 0; i < subscriberCount; i++ {
		channels[i] = hub.Subscribe(ctx, 1, 0, "")
	}
	if got := hub.Stats().ActiveSubscribers; got != subscriberCount {
		t.Fatalf("ActiveSubscribers = %d, want %d", got, subscriberCount)
	}

	wg.Add(subscriberCount)
	for i := 0; i < subscriberCount; i++ {
		ch := channels[i]
		go func() {
			defer wg.Done()
			local := make([]time.Duration, 0, eventCount)
			for count := 0; count < eventCount; count++ {
				frame, ok := <-ch
				if !ok {
					return
				}
				local = append(local, time.Since(frame.SentAt))
				received.Add(1)
			}
			latMu.Lock()
			latencies = append(latencies, local...)
			latMu.Unlock()
		}()
	}

	for i := 1; i <= eventCount; i++ {
		hub.Publish(ChangeEvent{
			OrganizationID: 1,
			Type:           ChangeKindDeleteMessage,
			EventID:        fmt.Sprintf("stress-%d", i),
			MessageID:      uint(i),
		})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("stress test timed out: received %d/%d", received.Load(), subscriberCount*eventCount)
	}

	if got := received.Load(); got != subscriberCount*eventCount {
		t.Fatalf("received = %d, want %d", got, subscriberCount*eventCount)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)/2]
	p99 := latencies[(len(latencies)*99)/100]
	maxLat := latencies[len(latencies)-1]
	t.Logf("50-subscriber fan-out (%d deliveries): p50=%v p99=%v max=%v", len(latencies), p50, p99, maxLat)

	if p99 >= maxAllowedP99 {
		t.Fatalf("fan-out p99 latency = %v, want < %v (max=%v)", p99, maxAllowedP99, maxLat)
	}

	cancel()
	hub.Close()

	if stats := hub.Stats(); stats.ActiveSubscribers != 0 || stats.PublishedTotal != eventCount || stats.DroppedTotal != 0 {
		t.Fatalf("unexpected final stats: %+v", stats)
	}

	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > baseGoroutines+2 && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > baseGoroutines+2 {
		t.Fatalf("possible goroutine leak after stress test: before=%d after=%d", baseGoroutines, after)
	}
}
