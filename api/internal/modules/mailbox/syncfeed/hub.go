package syncfeed

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// FrameKind identifies the variant of a StreamFrame delivered by Hub.
type FrameKind string

const (
	// DefaultRingSize is the default per-organization replay ring buffer capacity.
	DefaultRingSize = 512
	// DefaultHeartbeatInterval is the default SSE keepalive interval.
	DefaultHeartbeatInterval = 25 * time.Second

	FrameKindEvent     FrameKind = "event"
	FrameKindReset     FrameKind = "reset"
	FrameKindHeartbeat FrameKind = "heartbeat"
)

// StreamFrame is a single unit delivered over a Hub subscription stream.
type StreamFrame struct {
	Kind          FrameKind    `json:"kind"`
	Event         *ChangeEvent `json:"event,omitempty"`
	Cursor        string       `json:"cursor,omitempty"`
	ResetRequired bool         `json:"reset_required,omitempty"`
	SentAt        time.Time    `json:"sent_at"`
}

// HubOptions configures a Hub. Zero values use defaults; negative HeartbeatInterval disables heartbeats.
type HubOptions struct {
	RingSize          int
	SubscriberBuffer  int
	HeartbeatInterval time.Duration
}

// HubStats captures point-in-time observability counters for a Hub.
type HubStats struct {
	ActiveSubscribers int64 `json:"active_subscribers"`
	PublishedTotal    int64 `json:"published_total"`
	DroppedTotal      int64 `json:"dropped_total"`
	ReplayedTotal     int64 `json:"replayed_total"`
}

type orgRing struct {
	buf                       []ChangeEvent
	head, count               int
	latestSeq, lastEvictedSeq uint64
}

func (r *orgRing) push(ev ChangeEvent) {
	if n := len(r.buf); r.count < n {
		r.buf[(r.head+r.count)%n] = ev
		r.count++
	} else {
		r.lastEvictedSeq, r.buf[r.head], r.head = r.buf[r.head].Seq, ev, (r.head+1)%n
	}
	if ev.Seq > r.latestSeq {
		r.latestSeq = ev.Seq
	}
}

func (r *orgRing) replayAfter(seq uint64, userID uint) ([]ChangeEvent, bool) {
	if seq > r.latestSeq || seq < r.lastEvictedSeq {
		return nil, true
	}
	if r.count == 0 || seq == r.latestSeq {
		return nil, false
	}
	n := len(r.buf)
	out := make([]ChangeEvent, 0, r.count)
	for i := 0; i < r.count; i++ {
		if ev := r.buf[(r.head+i)%n]; ev.Seq > seq && (ev.UserID == 0 || ev.UserID == userID) {
			out = append(out, ev)
		}
	}
	return out, false
}

type hubSubscriber struct {
	id, minLiveSeq     uint64
	orgID, userID      uint
	bufSize            int
	out                chan StreamFrame
	mu                 sync.Mutex
	closed, liveDirect bool
	pending            []StreamFrame
}

func (s *hubSubscriber) deliver(frame StreamFrame) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || (frame.Kind == FrameKindEvent && frame.Event != nil && frame.Event.Seq <= s.minLiveSeq) {
		return false
	}
	if !s.liveDirect {
		if frame.Kind == FrameKindHeartbeat {
			return false
		}
		if len(s.pending) >= s.bufSize {
			copy(s.pending, s.pending[1:])
			s.pending[len(s.pending)-1] = frame
			return true
		}
		s.pending = append(s.pending, frame)
		return false
	}
	select {
	case s.out <- frame:
		return false
	default:
		select {
		case <-s.out:
		default:
		}
		select {
		case s.out <- frame:
		default:
		}
		return true
	}
}

// Hub is an in-process SSE and delta sync hub built on top of Broker.
type Hub struct {
	broker                                                  *Broker
	ringSize, subBufSize                                    int
	heartbeatInterval                                       time.Duration
	nextSubID                                               atomic.Uint64
	activeSubs, publishedTotal, droppedTotal, replayedTotal atomic.Int64
	mu                                                      sync.RWMutex
	rings                                                   map[uint]*orgRing
	subs                                                    map[uint]map[uint64]*hubSubscriber
	closed                                                  bool
	closedCh                                                chan struct{}
	wg                                                      sync.WaitGroup
}

// NewHub creates a Hub with the provided options and an underlying Broker.
func NewHub(opts HubOptions) *Hub {
	if opts.RingSize <= 0 {
		opts.RingSize = DefaultRingSize
	}
	if opts.SubscriberBuffer <= 0 {
		opts.SubscriberBuffer = DefaultSubscriberBuffer
	}
	if opts.HeartbeatInterval == 0 {
		opts.HeartbeatInterval = DefaultHeartbeatInterval
	}
	return &Hub{
		broker: NewBroker(opts.SubscriberBuffer), ringSize: opts.RingSize,
		subBufSize: opts.SubscriberBuffer, heartbeatInterval: opts.HeartbeatInterval,
		rings: make(map[uint]*orgRing), subs: make(map[uint]map[uint64]*hubSubscriber), closedCh: make(chan struct{}),
	}
}

// Broker returns the underlying fan-out Broker.
func (h *Hub) Broker() *Broker { return h.broker }

// Stats returns a point-in-time snapshot of Hub observability counters.
func (h *Hub) Stats() HubStats {
	return HubStats{
		ActiveSubscribers: h.activeSubs.Load(), PublishedTotal: h.publishedTotal.Load(),
		DroppedTotal: h.droppedTotal.Load(), ReplayedTotal: h.replayedTotal.Load(),
	}
}

// Publish records event in the org ring buffer, fans it out, and forwards to Broker.
func (h *Hub) Publish(event ChangeEvent) ChangeEvent {
	if event.OrganizationID == 0 {
		return event
	}
	now := time.Now().UTC()
	if event.OccurredAt == "" {
		event.OccurredAt = now.Format(time.RFC3339Nano)
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return event
	}
	ring := h.rings[event.OrganizationID]
	if ring == nil {
		ring = &orgRing{buf: make([]ChangeEvent, h.ringSize)}
		h.rings[event.OrganizationID] = ring
	}
	if event.Seq == 0 {
		event.Seq = ring.latestSeq + 1
	}
	ring.push(event)
	var targets []*hubSubscriber
	for _, sub := range h.subs[event.OrganizationID] {
		if event.UserID == 0 || event.UserID == sub.userID {
			targets = append(targets, sub)
		}
	}
	h.mu.Unlock()
	h.publishedTotal.Add(1)
	if len(targets) > 0 {
		frame := buildEventFrame(event, now)
		for _, sub := range targets {
			if sub.deliver(frame) {
				h.droppedTotal.Add(1)
			}
		}
	}
	h.broker.Publish(event)
	return event
}

// Subscribe opens a stream for (orgID, userID), replaying missed deltas from cursorToken or signaling ResetRequired.
func (h *Hub) Subscribe(ctx context.Context, orgID, userID uint, cursorToken string) <-chan StreamFrame {
	if ctx == nil || orgID == 0 || ctx.Err() != nil {
		return closedFrameChan()
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return closedFrameChan()
	}
	var replay []ChangeEvent
	resetRequired, highSeq := false, uint64(0)
	if cursorToken != "" {
		if cur, err := DecodeCursor(cursorToken); err != nil {
			resetRequired = true
		} else if ring := h.rings[orgID]; ring == nil {
			resetRequired = cur.Seq > 0
		} else if replay, resetRequired = ring.replayAfter(cur.Seq, userID); !resetRequired {
			highSeq = ring.latestSeq
		}
	}
	sub := &hubSubscriber{
		id: h.nextSubID.Add(1), orgID: orgID, userID: userID, bufSize: h.subBufSize,
		minLiveSeq: highSeq, out: make(chan StreamFrame, h.subBufSize), liveDirect: len(replay) == 0,
	}
	if resetRequired {
		sub.out <- StreamFrame{Kind: FrameKindReset, ResetRequired: true, SentAt: time.Now().UTC()}
	}
	if h.subs[orgID] == nil {
		h.subs[orgID] = make(map[uint64]*hubSubscriber)
	}
	h.subs[orgID][sub.id] = sub
	closedCh := h.closedCh
	h.activeSubs.Add(1)
	h.wg.Add(1)
	h.mu.Unlock()
	go h.runSubscriber(ctx, sub, replay, closedCh)
	return sub.out
}

func (h *Hub) runSubscriber(ctx context.Context, sub *hubSubscriber, replay []ChangeEvent, closedCh <-chan struct{}) {
	defer func() {
		h.mu.Lock()
		if delete(h.subs[sub.orgID], sub.id); len(h.subs[sub.orgID]) == 0 {
			delete(h.subs, sub.orgID)
		}
		h.mu.Unlock()
		sub.mu.Lock()
		sub.closed, sub.pending = true, nil
		close(sub.out)
		sub.mu.Unlock()
		h.activeSubs.Add(-1)
		h.wg.Done()
	}()
	if len(replay) > 0 && !h.drainReplay(ctx, sub, replay, closedCh) {
		return
	}
	var hbCh <-chan time.Time
	if h.heartbeatInterval > 0 {
		ticker := time.NewTicker(h.heartbeatInterval)
		defer ticker.Stop()
		hbCh = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-closedCh:
			return
		case ts := <-hbCh:
			if sub.deliver(StreamFrame{Kind: FrameKindHeartbeat, SentAt: ts.UTC()}) {
				h.droppedTotal.Add(1)
			}
		}
	}
}

func (h *Hub) drainReplay(ctx context.Context, sub *hubSubscriber, replay []ChangeEvent, closedCh <-chan struct{}) bool {
	for i := 0; ; i++ {
		var frame StreamFrame
		isReplay := i < len(replay)
		if isReplay {
			frame = buildEventFrame(replay[i], time.Now().UTC())
		} else {
			sub.mu.Lock()
			if len(sub.pending) == 0 {
				sub.liveDirect, sub.pending = true, nil
				sub.mu.Unlock()
				return true
			}
			frame, sub.pending = sub.pending[0], sub.pending[1:]
			sub.mu.Unlock()
		}
		select {
		case sub.out <- frame:
			if isReplay {
				h.replayedTotal.Add(1)
			}
		case <-ctx.Done():
			return false
		case <-closedCh:
			return false
		}
	}
}

// Close shuts down the Hub, closes active streams, and waits for all goroutines.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed, h.subs = true, make(map[uint]map[uint64]*hubSubscriber)
	close(h.closedCh)
	h.mu.Unlock()
	h.broker.Close()
	h.wg.Wait()
}

func closedFrameChan() <-chan StreamFrame {
	ch := make(chan StreamFrame)
	close(ch)
	return ch
}

func buildEventFrame(ev ChangeEvent, sentAt time.Time) StreamFrame {
	evCopy, occ := ev, sentAt
	if ts, err := time.Parse(time.RFC3339Nano, ev.OccurredAt); err == nil {
		occ = ts.UTC()
	}
	cur, _ := EncodeCursor(Cursor{Seq: ev.Seq, UpdatedAt: occ})
	return StreamFrame{Kind: FrameKindEvent, Event: &evCopy, Cursor: cur, SentAt: sentAt}
}
