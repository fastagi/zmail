package imapgateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

func TestLimiterLocksIPAndUserIndependently(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	limiter := newAuthLimiter(ratelimit.NewMemoryStores(), func() time.Time { return now })

	// A username that signed in from an IP keeps working there after the username is locked.
	limiter.Succeeded(ctx, "alice@mail.example", "10.0.0.1")
	for i := range 10 {
		if !limiter.Allow(ctx, "Alice@Mail.Example", "10.0.0."+string(rune('2'+i%5))) {
			t.Fatalf("attempt %d refused before the username budget ran out", i)
		}
		limiter.Failed(ctx, "Alice@Mail.Example", "10.0.0."+string(rune('2'+i%5)))
	}
	if limiter.Allow(ctx, "alice@mail.example", "10.0.0.9") {
		t.Fatal("the username is locked for an unknown IP")
	}
	if !limiter.Allow(ctx, "alice@mail.example", "10.0.0.1") {
		t.Fatal("the IP that signed in before is still allowed")
	}
	if !limiter.Allow(ctx, "bob@mail.example", "10.0.0.9") {
		t.Fatal("another username from the same IP is allowed")
	}

	// Twenty failures from one IP lock the IP for every username, the right password included.
	for i := range 20 {
		limiter.Failed(ctx, "user"+string(rune('a'+i))+"@mail.example", "192.0.2.7")
	}
	if limiter.Allow(ctx, "carol@mail.example", "192.0.2.7") {
		t.Fatal("the IP is locked")
	}
	if !limiter.Allow(ctx, "carol@mail.example", "192.0.2.8") {
		t.Fatal("another IP is allowed")
	}

	// Locks end with their window.
	now = now.Add(16 * time.Minute)
	if !limiter.Allow(ctx, "carol@mail.example", "192.0.2.7") || !limiter.Allow(ctx, "alice@mail.example", "10.0.0.9") {
		t.Fatal("locks outlived their window")
	}
}

func TestLimiterSlowsAttemptsPerIP(t *testing.T) {
	ctx := context.Background()
	limiter := newAuthLimiter(ratelimit.NewMemoryStores(), time.Now)
	for i := range 60 {
		if !limiter.Allow(ctx, "alice@mail.example", "198.51.100.1") {
			t.Fatalf("attempt %d refused", i)
		}
	}
	if limiter.Allow(ctx, "alice@mail.example", "198.51.100.1") {
		t.Fatal("the 61st attempt in a minute is refused")
	}
}

func TestUserKeyHidesTheUsername(t *testing.T) {
	key := userKey(" Alice@Mail.Example ")
	if key != userKey("alice@mail.example") || strings.Contains(key, "alice") || len(key) != len("user:")+16 {
		t.Fatalf("userKey = %q", key)
	}
}
