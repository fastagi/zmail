package imapgateway

import (
	"errors"
	"net/netip"
	"sync"
)

// Connection caps, answered with BYE [UNAVAILABLE] at the greeting or NO [LIMIT] at sign-in.
var (
	errTooManyConnections = errors.New("too many connections, try again later")
	errTooManyFromAddress = errors.New("too many connections from this address")
	errTooManyForAccount  = errors.New("too many connections for this account")
)

// sessionRegistry tracks live sessions and enforces the connection caps: in total and per IP from
// the greeting, per membership from sign-in.
type sessionRegistry struct {
	maxTotal, maxPerUser, maxPerIP int

	mu     sync.Mutex
	byID   map[uint64]*session
	byIP   map[string]int
	byUser map[uint]int
}

func newSessionRegistry(maxTotal, maxPerUser, maxPerIP int) *sessionRegistry {
	return &sessionRegistry{
		maxTotal: maxTotal, maxPerUser: maxPerUser, maxPerIP: maxPerIP,
		byID: map[uint64]*session{}, byIP: map[string]int{}, byUser: map[uint]int{},
	}
}

// open admits a new connection, or answers why not.
func (r *sessionRegistry) open(s *session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case len(r.byID) >= r.maxTotal:
		return errTooManyConnections
	case r.byIP[ipBucket(s.ip)] >= r.maxPerIP:
		return errTooManyFromAddress
	}
	r.byID[s.id] = s
	r.byIP[ipBucket(s.ip)]++
	return nil
}

// signIn counts a signed-in session against its membership, or answers why not.
func (r *sessionRegistry) signIn(membershipID uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byUser[membershipID] >= r.maxPerUser {
		return errTooManyForAccount
	}
	r.byUser[membershipID]++
	return nil
}

// close forgets a session; membershipID is zero when it never signed in.
func (r *sessionRegistry) close(s *session, membershipID uint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[s.id]; !ok {
		return
	}
	delete(r.byID, s.id)
	bucket := ipBucket(s.ip)
	if r.byIP[bucket]--; r.byIP[bucket] <= 0 {
		delete(r.byIP, bucket)
	}
	if membershipID != 0 {
		if r.byUser[membershipID]--; r.byUser[membershipID] <= 0 {
			delete(r.byUser, membershipID)
		}
	}
}

func (r *sessionRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byID)
}

// snapshot lists the live sessions.
func (r *sessionRegistry) snapshot() []*session {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessions := make([]*session, 0, len(r.byID))
	for _, s := range r.byID {
		sessions = append(sessions, s)
	}
	return sessions
}

// ipBucket is the key the per-IP caps and sign-in budgets count under: an IPv4 address as it is
// (an IPv4-mapped IPv6 one as IPv4), and the /64 of an IPv6 address, since one subscriber is
// handed a whole /64. Anything that does not parse stays as it is.
func ipBucket(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return ip
	}
	return prefix.String()
}
