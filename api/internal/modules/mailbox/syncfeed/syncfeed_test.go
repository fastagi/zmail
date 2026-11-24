package syncfeed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/events"
)

func TestCursorRoundTrip(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	ts := time.Date(2026, 11, 24, 16, 30, 45, 123456789, loc)

	cases := []struct {
		name string
		in   Cursor
		want Cursor
	}{
		{
			name: "non-UTC normalized to UTC",
			in:   Cursor{Version: CurrentCursorVersion, Seq: 42, UpdatedAt: ts},
			want: Cursor{Version: CurrentCursorVersion, Seq: 42, UpdatedAt: ts.UTC()},
		},
		{
			name: "zero version defaults to v1",
			in:   Cursor{Seq: 0, UpdatedAt: ts.UTC()},
			want: Cursor{Version: CurrentCursorVersion, Seq: 0, UpdatedAt: ts.UTC()},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, err := EncodeCursor(tc.in)
			if err != nil {
				t.Fatalf("EncodeCursor() error = %v", err)
			}
			if strings.ContainsAny(tok, "+/=") {
				t.Fatalf("token %q is not unpadded URL-safe base64", tok)
			}
			got, err := DecodeCursor(tok)
			if err != nil {
				t.Fatalf("DecodeCursor() error = %v", err)
			}
			if got.Version != tc.want.Version || got.Seq != tc.want.Seq || !got.UpdatedAt.Equal(tc.want.UpdatedAt) {
				t.Fatalf("DecodeCursor() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCursorValidationErrors(t *testing.T) {
	now := time.Now().UTC()
	if _, err := EncodeCursor(Cursor{Version: 2, Seq: 1, UpdatedAt: now}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("EncodeCursor(bad version) err = %v, want ErrInvalidCursor", err)
	}
	if _, err := EncodeCursor(Cursor{Version: CurrentCursorVersion, Seq: 1}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("EncodeCursor(zero time) err = %v, want ErrInvalidCursor", err)
	}

	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	validJSON := `{"v":1,"seq":50,"updated_at":"2026-11-24T16:00:00Z"}`

	badTokens := []struct{ name, token string }{
		{"empty", ""},
		{"oversized", strings.Repeat("a", MaxCursorLength+1)},
		{"invalid base64", "!!!not-base64!!!"},
		{"padded base64", base64.URLEncoding.EncodeToString([]byte(validJSON))},
		{"malformed json", b64("{bad")},
		{"unknown field", b64(`{"v":1,"seq":5,"updated_at":"2026-11-24T16:00:00Z","extra":1}`)},
		{"trailing payload", b64(validJSON + `{}`)},
		{"unsupported version", b64(`{"v":99,"seq":5,"updated_at":"2026-11-24T16:00:00Z"}`)},
		{"zero version", b64(`{"v":0,"seq":5,"updated_at":"2026-11-24T16:00:00Z"}`)},
		{"missing updated_at", b64(`{"v":1,"seq":5}`)},
	}
	for _, tc := range badTokens {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeCursor(tc.token); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("DecodeCursor(%q) err = %v, want ErrInvalidCursor", tc.name, err)
			}
		})
	}
}

func TestWireSchemaMatchesMailCore(t *testing.T) {
	readTrue := true
	kinds := []ChangeKind{
		ChangeKindUpsertMessage, ChangeKindUpdateFlags,
		ChangeKindMoveMessage, ChangeKindDeleteMessage, ChangeKindThreadUpdated,
	}
	for _, k := range kinds {
		if !k.Valid() {
			t.Fatalf("ChangeKind(%q).Valid() = false", k)
		}
	}
	if ChangeKind("unknown").Valid() {
		t.Fatalf("unknown ChangeKind reported valid")
	}

	page := SyncPage{
		Changes: []ChangeEvent{
			{
				OrganizationID: 7, UserID: 3, Seq: 10,
				Type: ChangeKindUpsertMessage, EventID: "e1", OccurredAt: "2026-11-24T16:00:00Z",
				Message: &MessageSummary{
					ID: 101, MailboxID: 2, ThreadID: 55, Direction: "inbound",
					Status: "received", From: "alice@example.com", Subject: "Hello",
					CreatedAt: "2026-11-24T16:00:00Z",
				},
			},
			{Type: ChangeKindMoveMessage, EventID: "e2", OccurredAt: "2026-11-24T16:00:01Z", MessageID: 101, Placement: PlacementArchive},
			{Type: ChangeKindUpdateFlags, EventID: "e3", OccurredAt: "2026-11-24T16:00:02Z", MessageID: 101, Read: &readTrue},
			{Type: ChangeKindDeleteMessage, EventID: "e4", OccurredAt: "2026-11-24T16:00:03Z", MessageID: 101},
			{Type: ChangeKindThreadUpdated, EventID: "e5", OccurredAt: "2026-11-24T16:00:04Z", ThreadID: 55, Thread: &ThreadSummary{MessageCount: 2, Unread: true}},
		},
		NextCursor:    nil,
		HasMore:       true,
		ResetRequired: true,
		SyncedAt:      "2026-11-24T16:00:05Z",
	}

	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("json.Marshal(SyncPage) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}
	if _, ok := decoded["next_cursor"]; !ok || decoded["next_cursor"] != nil {
		t.Fatalf("next_cursor = %#v, want explicit JSON null", decoded["next_cursor"])
	}
	if decoded["has_more"] != true || decoded["reset_required"] != true || decoded["synced_at"] != "2026-11-24T16:00:05Z" {
		t.Fatalf("unexpected page metadata: %#v", decoded)
	}

	evList := decoded["events"].([]any)
	if len(evList) != 5 {
		t.Fatalf("len(events) = %d, want 5", len(evList))
	}

	first := evList[0].(map[string]any)
	if first["type"] != "message.upserted" {
		t.Fatalf("first.type = %v", first["type"])
	}
	for _, hidden := range []string{"OrganizationID", "UserID", "Seq", "organization_id", "user_id", "seq"} {
		if _, exists := first[hidden]; exists {
			t.Fatalf("routing field %q leaked into wire JSON", hidden)
		}
	}
	msg := first["message"].(map[string]any)
	if len(msg["to"].([]any)) != 0 || len(msg["cc"].([]any)) != 0 || len(msg["label_ids"].([]any)) != 0 {
		t.Fatalf("expected non-null empty arrays in message: %#v", msg)
	}

	moved := evList[1].(map[string]any)
	if moved["type"] != "message.moved" {
		t.Fatalf("moved.type = %v", moved["type"])
	}
	if _, ok := moved["folder_id"]; !ok || moved["folder_id"] != nil {
		t.Fatalf("moved.folder_id = %#v, want explicit null", moved["folder_id"])
	}
	if _, ok := moved["trashed_at"]; !ok || moved["trashed_at"] != nil {
		t.Fatalf("moved.trashed_at = %#v, want explicit null", moved["trashed_at"])
	}

	emptyRaw, err := json.Marshal(SyncPage{SyncedAt: "2026-11-24T16:00:05Z"})
	if err != nil {
		t.Fatalf("json.Marshal(empty SyncPage) error = %v", err)
	}
	if !strings.Contains(string(emptyRaw), `"events":[]`) {
		t.Fatalf("empty SyncPage JSON = %s, want empty events array", emptyRaw)
	}
}

func TestBrokerFanOutAndTenantIsolation(t *testing.T) {
	b := NewBroker(8)
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subOrg1A := b.Subscribe(ctx, 1)
	subOrg1B := b.Subscribe(ctx, 1)
	subOrg1User10 := b.SubscribeUser(ctx, 1, 10)
	subOrg1User20 := b.SubscribeUser(ctx, 1, 20)
	subOrg2 := b.Subscribe(ctx, 2)

	b.Publish(ChangeEvent{OrganizationID: 1, Type: ChangeKindDeleteMessage, EventID: "org1-all", MessageID: 50})
	for name, ch := range map[string]<-chan ChangeEvent{
		"org1A": subOrg1A, "org1B": subOrg1B, "org1User10": subOrg1User10, "org1User20": subOrg1User20,
	} {
		select {
		case got := <-ch:
			if got.EventID != "org1-all" {
				t.Fatalf("%s got %q, want org1-all", name, got.EventID)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s timed out waiting for org-wide event", name)
		}
	}
	select {
	case leaked := <-subOrg2:
		t.Fatalf("org 2 received org 1 event: %+v", leaked)
	default:
	}

	b.Publish(ChangeEvent{OrganizationID: 1, UserID: 10, Type: ChangeKindUpdateFlags, EventID: "user10-only", MessageID: 51})
	select {
	case got := <-subOrg1User10:
		if got.EventID != "user10-only" {
			t.Fatalf("user10 got %q, want user10-only", got.EventID)
		}
	case <-time.After(time.Second):
		t.Fatal("user10 timed out waiting for targeted event")
	}
	for name, ch := range map[string]<-chan ChangeEvent{"org1A": subOrg1A, "org1User20": subOrg1User20} {
		select {
		case leaked := <-ch:
			t.Fatalf("%s received user10 event: %+v", name, leaked)
		default:
		}
	}
}

func TestBrokerSlowConsumerDropsOldestAndNeverBlocks(t *testing.T) {
	b := NewBroker(2)
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := b.Subscribe(ctx, 42)

	done := make(chan struct{})
	go func() {
		for _, id := range []string{"e1", "e2", "e3", "e4", "e5"} {
			b.Publish(ChangeEvent{OrganizationID: 42, Type: ChangeKindDeleteMessage, EventID: id})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on slow consumer")
	}

	var got []string
	for len(got) < 2 {
		select {
		case ev := <-ch:
			got = append(got, ev.EventID)
		case <-time.After(time.Second):
			t.Fatalf("timed out reading buffered events, got %v", got)
		}
	}
	if got[0] != "e4" || got[1] != "e5" {
		t.Fatalf("slow consumer got %v, want [e4 e5] (drop-oldest)", got)
	}
}

func TestBrokerBridgeEventBus(t *testing.T) {
	b := NewBroker(8)
	defer b.Close()

	if sub := b.BridgeEventBus(nil); sub != nil {
		t.Fatalf("BridgeEventBus(nil) = %v, want nil", sub)
	}

	bus := events.NewEventBus()
	sub := b.BridgeEventBus(bus)
	if sub == nil {
		t.Fatal("BridgeEventBus returned nil subscription")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := b.Subscribe(ctx, 9)

	receivedAt := time.Date(2026, 11, 24, 16, 5, 0, 0, time.UTC)
	mailEv := domain.NewMailReceivedEvent(
		777, 12, 9, domain.MailDirectionInbound,
		"sender@example.com", "Quarterly report", receivedAt,
	)
	if err := bus.Publish(ctx, mailEv); err != nil {
		t.Fatalf("bus.Publish error = %v", err)
	}

	select {
	case got := <-ch:
		if got.Type != ChangeKindUpsertMessage || got.EventID == "" {
			t.Fatalf("unexpected bridged header: type=%q id=%q", got.Type, got.EventID)
		}
		if got.Message == nil || got.Message.ID != 777 || got.Message.MailboxID != 12 ||
			got.Message.From != "sender@example.com" || got.Message.Subject != "Quarterly report" ||
			got.Message.Placement != PlacementDefault {
			t.Fatalf("unexpected Message payload: %+v", got.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for bridged MailReceivedEvent")
	}

	sub.Unsubscribe()
	if err := bus.Publish(ctx, mailEv); err != nil {
		t.Fatalf("bus.Publish after unsubscribe error = %v", err)
	}
	select {
	case extra := <-ch:
		t.Fatalf("received event after unsubscribe: %+v", extra)
	default:
	}
}

func TestBrokerContextCancelNoLeakAndConcurrency(t *testing.T) {
	b := NewBroker(4)
	defer b.Close()

	canceledCtx, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, ok := <-b.Subscribe(canceledCtx, 1); ok {
		t.Fatal("expected closed channel for pre-canceled context")
	}
	if b.SubscriberCount() != 0 {
		t.Fatalf("SubscriberCount = %d, want 0", b.SubscriberCount())
	}

	baseGoroutines := runtime.NumGoroutine()
	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers * 2)

	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			ch := b.Subscribe(ctx, uint(id%3+1))
			time.Sleep(time.Millisecond)
			cancel()
			for range ch {
			}
		}(i)

		go func(id int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				b.Publish(ChangeEvent{
					OrganizationID: uint(id%3 + 1),
					Type:           ChangeKindDeleteMessage,
					EventID:        "stress",
					MessageID:      uint(j + 1),
				})
			}
		}(i)
	}
	wg.Wait()

	deadline := time.Now().Add(time.Second)
	for b.SubscriberCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if b.SubscriberCount() != 0 || b.OrgSubscriberCount(1) != 0 {
		t.Fatalf("residual subscribers: total=%d org1=%d", b.SubscriberCount(), b.OrgSubscriberCount(1))
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
