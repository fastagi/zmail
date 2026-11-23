package imapgateway

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// revocationDeadline is the longest a session may outlive the revocation of its password or
// access: one sweep interval plus the window in which failing sweeps still keep sessions open.
const revocationDeadline = time.Minute

// Goodbyes of the sweep. They name no account, address, or mailbox.
const (
	byeRevoked     = "Access was revoked; sign in again"
	byeNoMailbox   = "Access to this mailbox was removed"
	byeUnavailable = "Service temporarily unavailable"
)

// defaultRevalidateInterval is the sweep interval Sweep assumes until RunSweeper sets one.
const defaultRevalidateInterval = 20 * time.Second

// sweepState tracks the last sweep that reached the store.
type sweepState struct {
	lastOK time.Time
	// failClosedAfter is how long failing sweeps keep sessions open.
	failClosedAfter time.Duration
}

// Sweep re-checks every signed-in session against its password, the organization switch, the
// member's feature, and the mailboxes it shows, and ends the sessions that lost them. When the
// store cannot answer for longer than the fail-closed window, it ends every signed-in session.
func (s *Server) Sweep(ctx context.Context) error {
	type signedIn struct {
		session   *session
		principal *domain.MailClientPrincipal
		selected  uint
	}
	var live []signedIn
	var sessions []*session
	var principals []*domain.MailClientPrincipal
	for _, sess := range s.registry.snapshot() {
		principal, selected := sess.current()
		if principal == nil {
			continue
		}
		entry := signedIn{session: sess, principal: principal}
		if selected != nil {
			entry.selected = selected.mailboxID
		}
		live = append(live, entry)
		sessions = append(sessions, sess)
		principals = append(principals, principal)
	}
	now := s.cfg.Now()
	s.mu.Lock()
	if s.sweep.lastOK.IsZero() {
		s.sweep.lastOK = now
	}
	s.mu.Unlock()
	if len(live) == 0 {
		s.sweepSucceeded(now)
		return nil
	}

	valid, err := s.store.RevalidateClients(ctx, principals)
	if err != nil {
		return s.sweepFailed(now, sessions, err)
	}
	type visibleKey struct {
		passwordID, mailboxID uint
	}
	visible := map[visibleKey]bool{}
	check := func(principal *domain.MailClientPrincipal, mailboxID uint) (bool, error) {
		key := visibleKey{principal.PasswordID, mailboxID}
		if seen, ok := visible[key]; ok {
			return seen, nil
		}
		ok, err := s.store.ClientMailboxVisible(ctx, principal, mailboxID)
		if err != nil {
			return false, err
		}
		visible[key] = ok
		return ok, nil
	}
	revoked, removed := 0, 0
	for _, entry := range live {
		if !valid[entry.principal.PasswordID] {
			go entry.session.bye(byeRevoked)
			revoked++
			continue
		}
		for _, mailboxID := range []uint{entry.principal.RootMailboxID, entry.selected} {
			if mailboxID == 0 {
				continue
			}
			ok, err := check(entry.principal, mailboxID)
			if err != nil {
				return s.sweepFailed(now, sessions, err)
			}
			if !ok {
				go entry.session.bye(byeNoMailbox)
				removed++
				break
			}
		}
	}
	recordSweepClosed("revoked", revoked)
	recordSweepClosed("mailbox", removed)
	if revoked+removed > 0 {
		s.logger.Info("imap_gateway.sweep_closed", "revoked", revoked, "mailbox", removed)
	}
	s.sweepSucceeded(now)
	return nil
}

func (s *Server) sweepSucceeded(now time.Time) {
	s.mu.Lock()
	s.sweep.lastOK = now
	s.mu.Unlock()
}

// sweepFailed keeps sessions open while the store has answered within the fail-closed window,
// and ends them all after it.
func (s *Server) sweepFailed(now time.Time, sessions []*session, err error) error {
	s.mu.Lock()
	lastOK, window := s.sweep.lastOK, s.sweep.failClosedAfter
	s.mu.Unlock()
	s.logger.Error("imap_gateway.sweep_failed", "sessions", len(sessions), "error", err)
	if now.Sub(lastOK) < window {
		return err
	}
	for _, sess := range sessions {
		go sess.bye(byeUnavailable)
	}
	recordSweepClosed("unavailable", len(sessions))
	s.logger.Warn("imap_gateway.sweep_failed_closed", "sessions", len(sessions))
	return err
}

// RunSweeper sweeps every interval until ctx ends. Failing sweeps keep sessions open for at most
// one minute less the interval, so no revoked session lives past a minute.
func (s *Server) RunSweeper(ctx context.Context, interval time.Duration) {
	s.mu.Lock()
	s.sweep.failClosedAfter = max(revocationDeadline-interval, interval)
	s.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(ctx, max(interval, time.Second))
			_ = s.Sweep(sweepCtx) //nolint:errcheck // Sweep logs and acts on its own failures
			cancel()
		}
	}
}
