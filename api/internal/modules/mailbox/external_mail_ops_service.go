package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/metrics"
)

// ErrExternalMailConnectionNotRevoked means the copies of a connection that is still bound cannot
// be purged: it must be revoked first.
var ErrExternalMailConnectionNotRevoked = errors.New("external mail connection is not revoked")

// errExternalMailFetchTimeout is a fetch that ran past its deadline; it counts as fetch_failed.
var errExternalMailFetchTimeout = errors.New("external mail fetch timed out")

// errExternalMailCursorLoop is a source that reports an empty cursor expired; it counts as
// fetch_failed, so the connection backs off instead of restarting forever.
var errExternalMailCursorLoop = errors.New("external mail source expired an empty cursor")

const (
	// externalMailMaxBackoff caps the wait after repeated fetch failures.
	externalMailMaxBackoff = time.Hour
	// externalMailRunPruneBatch bounds the runs past their retention one pass deletes.
	externalMailRunPruneBatch = 1000
	// externalMailPurgeStepsPerPass bounds the purge steps of one worker pass.
	externalMailPurgeStepsPerPass = 5
	// externalMailImmediatePurgeSteps bounds the purge steps one owner request runs before it
	// leaves the rest to the worker.
	externalMailImmediatePurgeSteps = 5
	// externalMailLeaseMargin is added to the fetch deadline when a lease is renewed.
	externalMailLeaseMargin = 30 * time.Second
	// externalMailPurgeRetry is how long a connection whose purge failed waits before the next
	// attempt.
	externalMailPurgeRetry = time.Hour
	// externalMailMissingMailbox is the revoke reason of a connection whose mailbox is gone.
	externalMailMissingMailbox = "mailbox_missing"
)

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// externalMailPurgeBatch bounds the copies one purge step deletes; tests lower it.
var externalMailPurgeBatch = 1000

// Reasons recorded with a purge.
const (
	externalMailPurgeRetention = "retention"
	externalMailPurgeOwner     = "owner"
	// externalMailPurgeRebound purges a revoked connection of another member whose account a new
	// binder proved control of.
	externalMailPurgeRebound = "rebound"
)

// externalMailBackoff is the wait before the next fetch after failures failed fetches in a row:
// the sync interval, doubled per further failure, capped at an hour, less up to a fifth of it so
// connections that failed together do not retry together.
func externalMailBackoff(failures int, jitter func(time.Duration) time.Duration) time.Duration {
	delay := externalMailSyncInterval
	for i := 1; i < failures && delay < externalMailMaxBackoff; i++ {
		delay *= 2
	}
	delay = min(delay, externalMailMaxBackoff)
	return delay - jitter(delay/5)
}

// externalMailJitter is a uniform random duration in [0, limit).
func externalMailJitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	return rand.N(limit)
}

// ExternalMailOpsService shows a connection's sync history to its owner and lets the owner delete
// the copies of a revoked connection at once.
type ExternalMailOpsService interface {
	// ListExternalMailSyncRuns lists the latest sync runs of the viewer's own connection.
	ListExternalMailSyncRuns(ctx context.Context, viewer domain.OrganizationContext, id uint) ([]*domain.ExternalMailSyncRun, error)
	// PurgeExternalMailCopies deletes the copies of the viewer's own revoked connection.
	PurgeExternalMailCopies(ctx context.Context, viewer domain.OrganizationContext, id uint) (ExternalMailPurgeResult, error)
}

// ExternalMailPurgeResult is what an owner's purge did. Completed is false when more copies are
// left than one request deletes; the worker deletes the rest in its next passes.
type ExternalMailPurgeResult struct {
	DeletedMessages int
	Completed       bool
}

// ownExternalMailConnection returns a connection the viewer manages: their own account, or a
// shared connection of a mailbox whose members they manage. Anyone else, managers of another member's
// account included, is answered not found. withSync also requires the feature to be on; deleting
// revoked copies does not, so turning the providers off never strands them.
func (s *service) ownExternalMailConnection(ctx context.Context, viewer domain.OrganizationContext, id uint, withSync bool) (*domain.ExternalMailConnection, error) {
	if s.externalMail == nil || (withSync && !s.externalMailAvailable()) {
		return nil, ErrExternalMailConnectionNotFound
	}
	return s.managedConnection(ctx, viewer, id)
}

func (s *service) ListExternalMailSyncRuns(ctx context.Context, viewer domain.OrganizationContext, id uint) ([]*domain.ExternalMailSyncRun, error) {
	connection, err := s.ownExternalMailConnection(ctx, viewer, id, true)
	if err != nil {
		return nil, err
	}
	return s.externalMail.ListExternalMailSyncRuns(ctx, connection.ID, domain.MaxExternalMailSyncRuns)
}

func (s *service) PurgeExternalMailCopies(ctx context.Context, viewer domain.OrganizationContext, id uint) (ExternalMailPurgeResult, error) {
	connection, err := s.ownExternalMailConnection(ctx, viewer, id, false)
	if err != nil {
		return ExternalMailPurgeResult{}, err
	}
	// Purging a shared connection deletes its mailbox with every copy and member, so only those who
	// may delete the mailbox may: owners and admins. A manage member reconnects and unbinds it, and
	// the worker purges it once the retention ends.
	if connection.Shared && !viewer.Role.CanManageOrganization() {
		return ExternalMailPurgeResult{}, domain.ErrPermissionDenied
	}
	if connection.Status != domain.ExternalMailStatusRevoked {
		return ExternalMailPurgeResult{}, ErrExternalMailConnectionNotRevoked
	}
	var result ExternalMailPurgeResult
	for range externalMailImmediatePurgeSteps {
		purge, stepErr := s.purgeExternalMailStep(ctx, domain.ExternalMailPurgeStep{
			ConnectionID: connection.ID, Now: s.now().UTC(), Messages: externalMailPurgeBatch,
		}, viewer.UserID, externalMailPurgeOwner)
		if stepErr != nil {
			return result, stepErr
		}
		if purge == nil {
			// The worker holds the connection right now; it finishes the purge.
			break
		}
		result.DeletedMessages += purge.DeletedMessages
		if purge.Completed {
			result.Completed = true
			return result, nil
		}
	}
	// More copies than one request deletes: make the rest due now, so the worker deletes them.
	due := s.now().UTC().Add(-s.settings.ExternalMail.Sync.Retention())
	pending, err := s.externalMail.ExpediteExternalMailPurge(ctx, connection.ID, due)
	if err != nil {
		return result, err
	}
	if !pending {
		// The connection is gone, purged by the worker meanwhile, or bound again.
		if _, err := s.externalMail.FindExternalMailConnection(ctx, viewer.OrganizationID, connection.ID); errors.Is(err, ErrExternalMailConnectionNotFound) {
			result.Completed = true
			return result, nil
		}
		return result, ErrExternalMailConnectionNotRevoked
	}
	return result, nil
}

// pruneExternalMailSyncRuns deletes a bounded batch of sync runs past their retention. A failure
// is logged: the next pass tries again, and it never holds up syncing.
func (s *service) pruneExternalMailSyncRuns(ctx context.Context, now time.Time) {
	if _, err := s.externalMail.PruneExternalMailSyncRuns(ctx, now.Add(-domain.ExternalMailSyncRunRetention), externalMailRunPruneBatch); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "external_mail.sync_runs_prune_failed", slog.String("error", err.Error()))
	}
}

// PurgeRevokedExternalMail deletes copies of connections revoked longer than the retention: at
// most min(limit, externalMailPurgeStepsPerPass) bounded steps. The mail worker runs it on every
// tick whenever the mailbox starter is on, whether or not any provider is enabled. A step that
// fails is logged and its connection held back for an hour, so it never blocks the connections
// revoked after it and never fails the tick.
func (s *service) PurgeRevokedExternalMail(ctx context.Context, now time.Time, limit int) (int, error) {
	if s.externalMail == nil || limit <= 0 {
		return 0, nil
	}
	now = now.UTC()
	step := domain.ExternalMailPurgeStep{
		RevokedBefore: now.Add(-s.settings.ExternalMail.Sync.Retention()), Now: now, Messages: externalMailPurgeBatch,
	}
	done := 0
	for range min(limit, externalMailPurgeStepsPerPass) {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		purge, err := s.purgeExternalMailStep(ctx, step, 0, externalMailPurgeRetention)
		if err != nil {
			if purge == nil || purge.Connection == nil {
				// The connection is not known, so nothing can be held back: try next tick.
				slog.WarnContext(ctx, "external_mail.copies_purge_failed", slog.String("error", err.Error()))
				return done, nil
			}
			slog.WarnContext(ctx, "external_mail.copies_purge_failed",
				slog.Uint64("connection_id", uint64(purge.Connection.ID)), slog.String("error", err.Error()))
			if deferErr := s.externalMail.DeferExternalMailPurge(ctx, purge.Connection.ID, now.Add(externalMailPurgeRetry)); deferErr != nil {
				slog.WarnContext(ctx, "external_mail.copies_purge_defer_failed",
					slog.Uint64("connection_id", uint64(purge.Connection.ID)), slog.String("error", deferErr.Error()))
				return done, nil
			}
			done++
			continue
		}
		if purge == nil {
			return done, nil
		}
		done++
	}
	return done, nil
}

// dropConnectionWithoutMailbox handles a claimed connection whose mailbox is gone: nothing can be
// stored, so the grant is handed back and the connection revoked, which stops its polling and
// starts the retention that deletes it. The run is recorded as dropped.
func (s *service) dropConnectionWithoutMailbox(ctx context.Context, connection *domain.ExternalMailConnection, result domain.ExternalMailSyncResult, startedAt time.Time) error {
	if s.encryptor != nil {
		s.handBackExternalGrant(ctx, connection)
	}
	now := s.now().UTC()
	if err := s.externalMail.RevokeExternalMailConnection(ctx, connection.OrganizationID, connection.ID, now); err != nil {
		return err
	}
	result.Run = &domain.ExternalMailSyncRun{
		ConnectionID: connection.ID, StartedAt: startedAt, FinishedAt: now, Outcome: domain.ExternalMailSyncDropped,
	}
	// The connection is revoked now, so the save stores nothing and records the run only.
	if _, _, err := s.externalMail.SaveExternalMailSync(ctx, result); err != nil {
		return err
	}
	metrics.RecordExternalMailSync(string(connection.Provider), string(domain.ExternalMailSyncDropped), 0)
	slog.WarnContext(ctx, "external_mail.revoked",
		slog.Uint64("connection_id", uint64(connection.ID)), slog.Uint64("mailbox_id", uint64(connection.MailboxID)),
		slog.String("provider", string(connection.Provider)), slog.String("reason", externalMailMissingMailbox))
	return nil
}

// purgeExternalMailStep deletes one batch of a revoked connection's copies in one transaction.
// The step that deletes the last copy also soft-deletes the mailbox, records copies_purged in
// its access history, and deletes the connection with its runs, so binding the account again
// starts a new mailbox. It returns nil when no connection is due or the one asked for is locked.
func (s *service) purgeExternalMailStep(ctx context.Context, step domain.ExternalMailPurgeStep, actorUserID uint, reason string) (*domain.ExternalMailPurge, error) {
	var purge *domain.ExternalMailPurge
	err := s.repo.InTransaction(ctx, func(tx context.Context) error {
		var err error
		purge, err = s.externalMail.PurgeRevokedExternalMail(tx, step)
		if err != nil || purge == nil || !purge.Completed {
			return err
		}
		connection := purge.Connection
		if err := s.repo.DeleteMailbox(tx, connection.OrganizationID, connection.MailboxID); err != nil && !errors.Is(err, ErrMailboxNotFound) {
			return err
		}
		owner := uint(0)
		if connection.OwnerUserID != nil {
			owner = *connection.OwnerUserID
		}
		mailbox := &domain.Mailbox{ID: connection.MailboxID, OrganizationID: connection.OrganizationID}
		if err := s.recordAccess(tx, mailbox, domain.MailboxAccessCopiesPurged, actorUserID, owner, map[string]any{
			"connection_id": connection.ID, "provider": string(connection.Provider), "reason": reason,
		}); err != nil {
			return err
		}
		return s.externalMail.DeleteExternalMailConnection(tx, connection.ID)
	})
	if err != nil || purge == nil {
		// On an error the purge, when known, names the connection, so the caller can hold it back.
		return purge, err
	}
	metrics.RecordExternalMailCopiesPurged(purge.DeletedMessages)
	attributes := []any{
		slog.Uint64("connection_id", uint64(purge.Connection.ID)), slog.Uint64("mailbox_id", uint64(purge.Connection.MailboxID)),
		slog.String("provider", string(purge.Connection.Provider)), slog.String("reason", reason),
		slog.Int("deleted_messages", purge.DeletedMessages), slog.Bool("completed", purge.Completed),
	}
	if purge.Completed {
		slog.InfoContext(ctx, "external_mail.copies_purged", attributes...)
	} else {
		slog.InfoContext(ctx, "external_mail.copies_purge_progress", attributes...)
	}
	return purge, nil
}
