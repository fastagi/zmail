package syncfeed

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/events"
)

// DefaultSubscriberBuffer is the default bounded channel capacity per subscriber.
const DefaultSubscriberBuffer = 64

type subscriberKey struct {
	orgID  uint
	userID uint
}

type subscriber struct {
	id     uint64
	key    subscriberKey
	ch     chan ChangeEvent
	mu     sync.Mutex
	closed bool
}

// deliver pushes event into sub.ch without blocking. When the buffer is full,
// the oldest queued event is evicted to make room for the newest event.
func (s *subscriber) deliver(event ChangeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- event:
		return
	default:
	}
	// Buffer full: drop oldest item, then enqueue the newest item.
	select {
	case <-s.ch:
	default:
	}
	select {
	case s.ch <- event:
	default:
	}
}

func (s *subscriber) close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
	s.mu.Unlock()
}

// Broker is a thread-safe in-process SSE fan-out hub keyed by OrganizationID
// or (OrganizationID, UserID) with bounded drop-oldest subscriber buffers.
type Broker struct {
	bufSize   int
	nextSubID atomic.Uint64
	nextSeq   atomic.Uint64

	mu       sync.RWMutex
	byOrg    map[uint]map[uint64]*subscriber
	closed   bool
	closedCh chan struct{}
}

// NewBroker creates a Broker with the given per-subscriber buffer capacity.
// Non-positive bufferSize defaults to DefaultSubscriberBuffer.
func NewBroker(bufferSize int) *Broker {
	if bufferSize <= 0 {
		bufferSize = DefaultSubscriberBuffer
	}
	return &Broker{
		bufSize:  bufferSize,
		byOrg:    make(map[uint]map[uint64]*subscriber),
		closedCh: make(chan struct{}),
	}
}

// Subscribe registers an organization-level subscriber that receives org-wide
// change events for orgID. The returned channel closes automatically when ctx
// is canceled or the broker is closed.
func (b *Broker) Subscribe(ctx context.Context, orgID uint) <-chan ChangeEvent {
	return b.SubscribeUser(ctx, orgID, 0)
}

// SubscribeUser registers a subscriber scoped to (orgID, userID).
// When userID is 0, the subscriber receives org-wide events (event.UserID == 0).
// When userID > 0, the subscriber receives both org-wide events and events
// targeted specifically to (orgID, userID).
func (b *Broker) SubscribeUser(ctx context.Context, orgID, userID uint) <-chan ChangeEvent {
	if ctx == nil || orgID == 0 || ctx.Err() != nil {
		ch := make(chan ChangeEvent)
		close(ch)
		return ch
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		ch := make(chan ChangeEvent)
		close(ch)
		return ch
	}

	sub := &subscriber{
		id:  b.nextSubID.Add(1),
		key: subscriberKey{orgID: orgID, userID: userID},
		ch:  make(chan ChangeEvent, b.bufSize),
	}
	subs := b.byOrg[orgID]
	if subs == nil {
		subs = make(map[uint64]*subscriber)
		b.byOrg[orgID] = subs
	}
	subs[sub.id] = sub
	closedCh := b.closedCh
	b.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			b.unsubscribe(sub)
		case <-closedCh:
		}
	}()

	return sub.ch
}

func (b *Broker) unsubscribe(sub *subscriber) {
	b.mu.Lock()
	if subs := b.byOrg[sub.key.orgID]; subs != nil {
		delete(subs, sub.id)
		if len(subs) == 0 {
			delete(b.byOrg, sub.key.orgID)
		}
	}
	b.mu.Unlock()
	sub.close()
}

// Publish fans out event to all matching subscribers without blocking.
// Events with OrganizationID == 0 are ignored.
func (b *Broker) Publish(event ChangeEvent) {
	if event.OrganizationID == 0 {
		return
	}
	if event.Seq == 0 {
		event.Seq = b.nextSeq.Add(1)
	}

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	orgSubs := b.byOrg[event.OrganizationID]
	if len(orgSubs) == 0 {
		b.mu.RUnlock()
		return
	}
	targets := make([]*subscriber, 0, len(orgSubs))
	for _, sub := range orgSubs {
		if event.UserID == 0 || sub.key.userID == event.UserID {
			targets = append(targets, sub)
		}
	}
	b.mu.RUnlock()

	for _, sub := range targets {
		sub.deliver(event)
	}
}

// SubscriberCount returns the total number of active subscribers across all organizations.
func (b *Broker) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	total := 0
	for _, subs := range b.byOrg {
		total += len(subs)
	}
	return total
}

// OrgSubscriberCount returns the number of active subscribers for orgID.
func (b *Broker) OrgSubscriberCount(orgID uint) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.byOrg[orgID])
}

// Close closes the broker and all active subscriber channels.
func (b *Broker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	close(b.closedCh)
	var all []*subscriber
	for _, subs := range b.byOrg {
		for _, sub := range subs {
			all = append(all, sub)
		}
	}
	b.byOrg = make(map[uint]map[uint64]*subscriber)
	b.mu.Unlock()

	for _, sub := range all {
		sub.close()
	}
}

// BridgeEventBus subscribes to domain.EventMailReceived on bus and forwards
// received mail events as real-time ChangeEvents into the broker.
// Returns nil when bus is nil.
func (b *Broker) BridgeEventBus(bus *events.EventBus) events.Subscription {
	if bus == nil {
		return nil
	}
	return bus.Subscribe(domain.EventMailReceived, func(_ context.Context, event events.Event) error {
		received, ok := unwrapMailReceived(event)
		if !ok || received.OrganizationID == 0 {
			return nil
		}
		b.Publish(mailReceivedToChangeEvent(event, received))
		return nil
	})
}

func unwrapMailReceived(event events.Event) (domain.MailReceivedEvent, bool) {
	if wrapped, ok := event.(events.WrappedEvent); ok {
		if v, ok := wrapped.BasicEvent.(domain.MailReceivedEvent); ok {
			return v, true
		}
		if ptr, ok := wrapped.BasicEvent.(*domain.MailReceivedEvent); ok && ptr != nil {
			return *ptr, true
		}
	}
	return domain.MailReceivedEvent{}, false
}

func mailReceivedToChangeEvent(raw events.Event, received domain.MailReceivedEvent) ChangeEvent {
	occurred := received.ReceivedAt
	if occurred.IsZero() && raw != nil {
		occurred = raw.OccurredAt()
	}
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	occurred = occurred.UTC()
	occurredStr := occurred.Format(time.RFC3339Nano)

	eventID := ""
	if raw != nil {
		eventID = raw.Metadata().ID
	}
	if eventID == "" {
		eventID = fmt.Sprintf("mail-received-%d-%d", received.MessageID, occurred.UnixNano())
	}

	direction := string(received.Direction)
	if direction == "" {
		direction = string(domain.MailDirectionInbound)
	}

	return ChangeEvent{
		OrganizationID: received.OrganizationID,
		Type:           ChangeKindUpsertMessage,
		EventID:        eventID,
		OccurredAt:     occurredStr,
		Message: &MessageSummary{
			ID:        received.MessageID,
			MailboxID: received.MailboxID,
			Direction: direction,
			Status:    "received",
			From:      received.FromAddress,
			To:        []string{},
			Cc:        []string{},
			Subject:   received.Subject,
			Read:      false,
			Starred:   false,
			Placement: PlacementDefault,
			LabelIDs:  []uint{},
			CreatedAt: occurredStr,
		},
	}
}
