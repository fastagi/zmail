package imapgateway

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// Sign-in limits (spec §4.3). The IP and username budgets are separate buckets, never one
// IP+username key.
const (
	ruleAuthAttemptsIP = "mail_client_auth_ip"
	ruleAuthFailIP     = "mail_client_fail_ip"
	ruleAuthFailUser   = "mail_client_fail_user"
	authAttemptsPerIP  = 60
	authAttemptWindow  = time.Minute
	authFailuresPerIP  = 20
	authFailuresUser   = 10
	authFailureWindow  = 15 * time.Minute
	// knownSignInTTL is how long a username that signed in from an IP stays exempt there from the
	// username lock, so an attacker cannot lock an employee out of their usual network.
	knownSignInTTL = 24 * time.Hour
	// maxTrackedKeys bounds the process-local lock and known-sign-in tables.
	maxTrackedKeys = 10000
)

// authLimiter decides whether a sign-in may be tried. Locks are kept in this process: a bucket
// that runs out locks its key until the bucket's window ends, even for the right password.
type authLimiter struct {
	attempts ratelimit.Store
	failIP   ratelimit.Store
	failUser ratelimit.Store
	now      func() time.Time

	mu    sync.Mutex
	locks *expiringSet
	known *expiringSet
}

func newAuthLimiter(stores *ratelimit.Stores, now func() time.Time) *authLimiter {
	if stores == nil {
		stores = ratelimit.NewMemoryStores()
	}
	if now == nil {
		now = time.Now
	}
	return &authLimiter{
		attempts: stores.New(ratelimit.Rule{Name: ruleAuthAttemptsIP, Max: authAttemptsPerIP, Window: authAttemptWindow}),
		failIP:   stores.New(ratelimit.Rule{Name: ruleAuthFailIP, Max: authFailuresPerIP, Window: authFailureWindow}),
		failUser: stores.New(ratelimit.Rule{Name: ruleAuthFailUser, Max: authFailuresUser, Window: authFailureWindow}),
		now:      now,
		locks:    newExpiringSet(maxTrackedKeys),
		known:    newExpiringSet(maxTrackedKeys),
	}
}

// userKey is the bucket key of a username: its lowered SHA-256, cut to 16 hex digits, so neither
// keys nor logs ever carry an address.
func userKey(username string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return "user:" + hex.EncodeToString(sum[:])[:16]
}

func ipKey(ip string) string { return "ip:" + ip }

func knownKey(username, ip string) string { return userKey(username) + "|" + ip }

// Allow reports whether a sign-in may be tried now; it spends one attempt of the IP's budget.
func (l *authLimiter) Allow(ctx context.Context, username, ip string) bool {
	now := l.now()
	l.mu.Lock()
	lockedIP := l.locks.has(ipKey(ip), now)
	lockedUser := l.locks.has(userKey(username), now) && !l.known.has(knownKey(username, ip), now)
	l.mu.Unlock()
	if lockedIP || lockedUser {
		return false
	}
	allowed, _, _ := l.attempts.Take(ctx, ipKey(ip))
	return allowed
}

// Failed counts a refused sign-in against the IP and, from an IP the username never signed in
// from, against the username; a budget that runs out locks its key.
func (l *authLimiter) Failed(ctx context.Context, username, ip string) {
	now := l.now()
	if allowed, remaining, resetAt := l.failIP.Take(ctx, ipKey(ip)); !allowed || remaining == 0 {
		l.lock(ipKey(ip), resetAt, now)
	}
	l.mu.Lock()
	known := l.known.has(knownKey(username, ip), now)
	l.mu.Unlock()
	if known {
		return
	}
	if allowed, remaining, resetAt := l.failUser.Take(ctx, userKey(username)); !allowed || remaining == 0 {
		l.lock(userKey(username), resetAt, now)
	}
}

// Succeeded remembers that the username signed in from the IP.
func (l *authLimiter) Succeeded(_ context.Context, username, ip string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.known.add(knownKey(username, ip), now.Add(knownSignInTTL))
}

func (l *authLimiter) lock(key string, until, now time.Time) {
	// A shared store's window is measured by its own clock; never lock for less than the window.
	if until.Before(now.Add(authFailureWindow)) {
		until = now.Add(authFailureWindow)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.locks.add(key, until)
}

// expiringSet is a bounded set of keys that expire; past the bound the oldest entry goes.
type expiringSet struct {
	max     int
	order   *list.List
	entries map[string]*list.Element
}

type expiringEntry struct {
	key     string
	expires time.Time
}

func newExpiringSet(max int) *expiringSet {
	return &expiringSet{max: max, order: list.New(), entries: map[string]*list.Element{}}
}

func (s *expiringSet) has(key string, now time.Time) bool {
	element, ok := s.entries[key]
	if !ok {
		return false
	}
	if !now.Before(entryOf(element).expires) {
		s.order.Remove(element)
		delete(s.entries, key)
		return false
	}
	return true
}

func (s *expiringSet) add(key string, expires time.Time) {
	if element, ok := s.entries[key]; ok {
		entryOf(element).expires = expires
		s.order.MoveToFront(element)
		return
	}
	s.entries[key] = s.order.PushFront(&expiringEntry{key: key, expires: expires})
	for len(s.entries) > s.max {
		oldest := s.order.Back()
		s.order.Remove(oldest)
		delete(s.entries, entryOf(oldest).key)
	}
}

// entryOf is the entry an element of an expiringSet holds.
func entryOf(element *list.Element) *expiringEntry {
	entry, ok := element.Value.(*expiringEntry)
	if !ok {
		panic("imapgateway: a list element of another type")
	}
	return entry
}
