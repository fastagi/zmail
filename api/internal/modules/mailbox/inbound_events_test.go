package mailbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/events"
)

func TestMailEventPublisherPublishesAndSubscribes(t *testing.T) {
	bus := events.NewEventBus()
	publisher := NewMailEventPublisher(bus)

	var (
		mu       sync.Mutex
		received []domain.MailReceivedEvent
		wildcard []string
	)
	sub := SubscribeMailReceived(bus, func(_ context.Context, event domain.MailReceivedEvent) error {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, event)
		return nil
	})
	if sub == nil {
		t.Fatal("SubscribeMailReceived returned nil subscription")
	}
	defer sub.Unsubscribe()

	wildSub := bus.Subscribe("mail.*", func(_ context.Context, event events.Event) error {
		mu.Lock()
		defer mu.Unlock()
		wildcard = append(wildcard, event.EventName())
		return nil
	})
	defer wildSub.Unsubscribe()

	at := time.Date(2026, time.November, 24, 15, 30, 0, 0, time.UTC)
	want := domain.NewMailReceivedEvent(
		42,
		7,
		3,
		domain.MailDirectionInbound,
		"alice@sender.test",
		"Quarterly invoice",
		at,
	)
	if err := publisher.PublishMailReceived(context.Background(), want); err != nil {
		t.Fatalf("PublishMailReceived: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("received %d events, want 1", len(received))
	}
	got := received[0]
	if got != want {
		t.Fatalf("received = %+v, want %+v", got, want)
	}
	if got.EventName() != domain.EventMailReceived {
		t.Fatalf("EventName = %q, want %q", got.EventName(), domain.EventMailReceived)
	}
	if !got.OccurredAt().Equal(at) {
		t.Fatalf("OccurredAt = %v, want %v", got.OccurredAt(), at)
	}
	if len(wildcard) != 1 || wildcard[0] != domain.EventMailReceived {
		t.Fatalf("wildcard received = %v, want [%s]", wildcard, domain.EventMailReceived)
	}
}

func TestMailEventPublisherNilAndErrorPaths(t *testing.T) {
	// Nil bus acts as a safe no-op publisher.
	noop := NewMailEventPublisher(nil)
	event := domain.NewMailReceivedEvent(1, 2, 3, domain.MailDirectionInbound, "a@b.test", "Hi", time.Time{})
	if event.ReceivedAt.IsZero() {
		t.Fatal("NewMailReceivedEvent should default zero ReceivedAt to current UTC time")
	}
	if err := noop.PublishMailReceived(context.Background(), event); err != nil {
		t.Fatalf("nil-bus PublishMailReceived = %v, want nil", err)
	}

	if SubscribeMailReceived(nil, func(context.Context, domain.MailReceivedEvent) error { return nil }) != nil {
		t.Fatal("SubscribeMailReceived(nil, ...) should return nil")
	}
	bus := events.NewEventBus()
	if SubscribeMailReceived(bus, nil) != nil {
		t.Fatal("SubscribeMailReceived(bus, nil) should return nil")
	}

	// Synchronous subscriber error propagates from the adapter.
	boom := errors.New("subscriber boom")
	sub := SubscribeMailReceived(bus, func(context.Context, domain.MailReceivedEvent) error {
		return boom
	})
	publisher := NewMailEventPublisher(bus)
	if err := publisher.PublishMailReceived(context.Background(), event); !errors.Is(err, boom) {
		t.Fatalf("PublishMailReceived error = %v, want %v", err, boom)
	}
	sub.Unsubscribe()

	// Closed bus error propagates from the adapter.
	bus.Close()
	if err := publisher.PublishMailReceived(context.Background(), event); !errors.Is(err, events.ErrEventBusClosed) {
		t.Fatalf("closed bus PublishMailReceived error = %v, want %v", err, events.ErrEventBusClosed)
	}
}

func TestNewMailReceivedEventHelper(t *testing.T) {
	if _, ok := newMailReceivedEvent(nil, &domain.MailMessage{ID: 1}, time.Now()); ok {
		t.Fatal("expected false for nil mailbox")
	}
	if _, ok := newMailReceivedEvent(&domain.Mailbox{ID: 1}, nil, time.Now()); ok {
		t.Fatal("expected false for nil message")
	}
	if _, ok := newMailReceivedEvent(&domain.Mailbox{ID: 1}, &domain.MailMessage{}, time.Now()); ok {
		t.Fatal("expected false for zero message ID")
	}

	createdAt := time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)
	event, ok := newMailReceivedEvent(
		&domain.Mailbox{OrganizationID: 9},
		&domain.MailMessage{
			ID:          11,
			MailboxID:   5,
			Direction:   domain.MailDirectionInbound,
			FromAddress: "sender@example.com",
			Subject:     "Hello",
			CreatedAt:   createdAt,
		},
		time.Time{},
	)
	if !ok {
		t.Fatal("expected true for valid mailbox and message")
	}
	if event.MessageID != 11 || event.MailboxID != 5 || event.OrganizationID != 9 ||
		event.Direction != domain.MailDirectionInbound || event.FromAddress != "sender@example.com" ||
		event.Subject != "Hello" || !event.ReceivedAt.Equal(createdAt) {
		t.Fatalf("unexpected event = %+v", event)
	}
}

func TestReceiveInboundPublishesMailReceivedEventAndIgnoresSubscriberErrors(t *testing.T) {
	fixture := newServiceFixture(t)
	bus := events.NewEventBus()
	fixture.service.mailEvents = NewMailEventPublisher(bus)
	// Leave notifications nil to confirm domain events are decoupled from the notification starter.
	fixture.service.notifications = nil

	ctx := context.Background()
	box, err := fixture.service.CreateMailbox(ctx, as(1), CreateMailboxInput{
		LocalPart: "events",
		Type:      domain.MailboxTypePersonal,
	})
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	var received []domain.MailReceivedEvent
	var subscriberErr error
	sub := SubscribeMailReceived(bus, func(_ context.Context, event domain.MailReceivedEvent) error {
		received = append(received, event)
		return subscriberErr
	})
	defer sub.Unsubscribe()

	raw := rawMail("Alice Example <alice@sender.test>", "Welcome aboard", "inbound-evt-1@sender.test")
	message, created, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("events@mail.example", raw))
	if err != nil || !created {
		t.Fatalf("ReceiveInbound = %v, created %v", err, created)
	}
	if len(received) != 1 {
		t.Fatalf("received %d events, want 1", len(received))
	}
	got := received[0]
	if got.MessageID != message.ID ||
		got.MailboxID != box.ID ||
		got.OrganizationID != box.OrganizationID ||
		got.Direction != domain.MailDirectionInbound ||
		got.FromAddress != "alice@sender.test" ||
		got.Subject != "Welcome aboard" ||
		!got.ReceivedAt.Equal(fixture.now.UTC()) {
		t.Fatalf("event = %+v", got)
	}

	// Duplicate delivery must not publish a second event.
	if _, created, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("events@mail.example", raw)); err != nil || created {
		t.Fatalf("duplicate ReceiveInbound = %v, created %v", err, created)
	}
	if len(received) != 1 {
		t.Fatalf("duplicate delivery published again: %d events", len(received))
	}

	// A failing subscriber must not fail inbound delivery.
	subscriberErr = errors.New("downstream failure")
	raw2 := rawMail("Bob Example <bob@sender.test>", "Follow up", "inbound-evt-2@sender.test")
	if _, created, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("events@mail.example", raw2)); err != nil || !created {
		t.Fatalf("ReceiveInbound with failing subscriber = %v, created %v", err, created)
	}
	if len(received) != 2 {
		t.Fatalf("received %d events, want 2", len(received))
	}
}
