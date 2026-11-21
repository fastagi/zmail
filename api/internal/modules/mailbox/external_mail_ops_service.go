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
)

// externalMailPurgeBatch bounds the copies one purge step deletes; tests lower it.
var externalMailPurgeBatch = 1000

// Reasons recorded with a purge.
const (
	externalMailPurgeRetention = "retention"
	externalMailPurgeOwner     = "owner"
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

// ownExternalMailConnection returns a connection of the viewer's own. Anyone else, managers
// included, is answered not found.
func (s *service) ownExternalMailConnection(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.ExternalMailConnection, error) {
	if !s.externalMailAvailable() {
		return nil, ErrExternalMailConnectionNotFound
	}
	connection, err := s.externalMail.FindExternalMailConnection(ctx, viewer.OrganizationID, id)
	if err != nil {
		return nil, err
	}
	if connection.OwnerUserID == nil || viewer.UserID == 0 || *connection.OwnerUserID != viewer.UserID {
		return nil, ErrExternalMailConnectionNotFound
	}
	return connection, nil
}

func (s *service) ListExternalMailSyncRuns(ctx context.Context, viewer domain.OrganizationContext, id uint) ([]*domain.ExternalMailSyncRun, error) {
	connection, err := s.ownExternalMailConnection(ctx, viewer, id)
	if err != nil {
		return nil, err
	}
	return s.externalMail.ListExternalMailSyncRuns(ctx, connection.ID, domain.MaxExternalMailSyncRuns)
}

func (s *service) PurgeExternalMailCopies(ctx context.Context, viewer domain.OrganizationContext, id uint) (ExternalMailPurgeResult, error) {
	connection, err := s.ownExternalMailConnection(ctx, viewer, id)
	if err != nil {
		return ExternalMailPurgeResult{}, err
	}
	if connection.Status != domain.ExternalMailStatusRevoked {
		return ExternalMailPurgeResult{}, ErrExternalMailConnectionNotRevoked
	}
	var result ExternalMailPurgeResult
	for range externalMailImmediatePurgeSteps {
		purge, stepErr := s.purgeExternalMailStep(ctx, domain.ExternalMailPurgeStep{
			ConnectionID: connection.ID, Messages: externalMailPurgeBatch,
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

// maintainExternalMail runs the bounded housekeeping of a sync pass: it prunes sync runs past
// their retention and deletes copies of connections revoked longer than the retention.
func (s *service) maintainExternalMail(ctx context.Context, now time.Time) error {
	if _, err := s.externalMail.PruneExternalMailSyncRuns(ctx, now.Add(-domain.ExternalMailSyncRunRetention), externalMailRunPruneBatch); err != nil {
		return err
	}
	step := domain.ExternalMailPurgeStep{
		RevokedBefore: now.Add(-s.settings.ExternalMail.Sync.Retention()), Messages: externalMailPurgeBatch,
	}
	for range externalMailPurgeStepsPerPass {
		if err := ctx.Err(); err != nil {
			return err
		}
		purge, err := s.purgeExternalMailStep(ctx, step, 0, externalMailPurgeRetention)
		if err != nil || purge == nil {
			return err
		}
	}
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
		return nil, err
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
