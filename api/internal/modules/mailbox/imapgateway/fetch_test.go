package imapgateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// sourceStore serves message sources of a set size, blocking each read until release closes.
type sourceStore struct {
	*fakeStore
	size     int
	release  chan struct{}
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (s *sourceStore) ClientMessageSource(ctx context.Context, _ *domain.MailClientPrincipal, _ uint, _ string, _ uint32) ([]byte, error) {
	now := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		peak := s.peak.Load()
		if now <= peak || s.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return make([]byte, s.size), nil
}

func TestSourceFetchesAreBounded(t *testing.T) {
	store := &sourceStore{fakeStore: newFakeStore(), size: 10, release: make(chan struct{})}
	server := NewServer(store, Config{MaxSourceFetches: 2}, nil)
	sess := &session{server: server}
	principal, selected := &domain.MailClientPrincipal{}, &selection{}

	var wg sync.WaitGroup
	var held atomic.Int32
	for i := range 6 {
		wg.Go(func() {
			raw, done, err := sess.source(context.Background(), principal, selected, domain.MailClientMessage{UID: uint32(i + 1), MessageID: uint(i + 1)})
			if err != nil || len(raw) != 10 {
				t.Errorf("source %d = %d bytes, %v", i, len(raw), err)
				return
			}
			if now := held.Add(1); now > 2 {
				t.Errorf("%d sources held at once; want at most 2", now)
			}
			time.Sleep(10 * time.Millisecond)
			held.Add(-1)
			done()
		})
	}
	waitUntil(t, func() bool { return store.inFlight.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	if got := store.inFlight.Load(); got != 2 {
		t.Fatalf("%d source reads in flight; want 2", got)
	}
	close(store.release)
	wg.Wait()
	if peak := store.peak.Load(); peak != 2 {
		t.Fatalf("peak source reads = %d; want 2", peak)
	}

	// A command whose time runs out while waiting for a slot gives up.
	blocked := &sourceStore{fakeStore: newFakeStore(), size: 10, release: make(chan struct{})}
	server = NewServer(blocked, Config{MaxSourceFetches: 1}, nil)
	sess = &session{server: server}
	go func() {
		_, _, _ = sess.source(context.Background(), principal, selected, domain.MailClientMessage{UID: 1, MessageID: 1})
	}()
	waitUntil(t, func() bool { return blocked.inFlight.Load() == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := sess.source(ctx, principal, selected, domain.MailClientMessage{UID: 2, MessageID: 2}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting for a slot past the deadline = %v", err)
	}
	close(blocked.release)
}

func TestOversizedSourceIsRefused(t *testing.T) {
	store := &sourceStore{fakeStore: newFakeStore(), size: domain.MailClientMaxSourceBytes + 1, release: make(chan struct{})}
	close(store.release)
	server := NewServer(store, Config{}, nil)
	sess := &session{server: server}
	_, _, err := sess.source(context.Background(), &domain.MailClientPrincipal{}, &selection{}, domain.MailClientMessage{UID: 1, MessageID: 9})
	if !errors.Is(err, domain.ErrMailClientMessageTooLarge) {
		t.Fatalf("an oversized source = %v; want ErrMailClientMessageTooLarge", err)
	}
	if server.cache.Size() != 0 {
		t.Fatal("an oversized source was cached")
	}
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
