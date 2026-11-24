package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/externalmail"
)

// maxConsecutiveFailures keeps the failure count inside its smallint column; the backoff is
// capped long before it.
const maxConsecutiveFailures = externalmail.MaxConsecutiveFailures

// ErrExternalMailPurgeRefused means a revoked connection points at a mailbox that does not hold
// synced copies; nothing is deleted.
var ErrExternalMailPurgeRefused = errors.New("external mail purge refused: the mailbox does not hold synced copies")

// ExternalMailSyncRunPO is the persistent object of one fetch of one connection.
type ExternalMailSyncRunPO = externalmail.ExternalMailSyncRunPO

var _ domain.ExternalMailOpsRepository = (*repository)(nil)

func recordExternalMailSyncRun(tx *gorm.DB, run *domain.ExternalMailSyncRun) error {
	return externalmail.RecordExternalMailSyncRun(tx, run)
}

func (r *repository) ListExternalMailSyncRuns(ctx context.Context, connectionID uint, limit int) ([]*domain.ExternalMailSyncRun, error) {
	return r.externalMailRepo().ListExternalMailSyncRuns(ctx, connectionID, limit)
}

func (r *repository) PruneExternalMailSyncRuns(ctx context.Context, before time.Time, limit int) (int, error) {
	return r.externalMailRepo().PruneExternalMailSyncRuns(ctx, before, limit)
}

func (r *repository) PurgeRevokedExternalMail(ctx context.Context, step domain.ExternalMailPurgeStep) (*domain.ExternalMailPurge, error) {
	return r.externalMailRepo().PurgeRevokedExternalMail(ctx, step)
}

func (r *repository) DeferExternalMailPurge(ctx context.Context, id uint, retryAt time.Time) error {
	return r.externalMailRepo().DeferExternalMailPurge(ctx, id, retryAt)
}

func (r *repository) RenewExternalMailLease(ctx context.Context, id uint, heldUntil, until time.Time) (bool, error) {
	return r.externalMailRepo().RenewExternalMailLease(ctx, id, heldUntil, until)
}

func (r *repository) DeleteExternalMailConnection(ctx context.Context, id uint) error {
	return r.externalMailRepo().DeleteExternalMailConnection(ctx, id)
}

func (r *repository) ExpediteExternalMailPurge(ctx context.Context, id uint, revokedAt time.Time) (bool, error) {
	return r.externalMailRepo().ExpediteExternalMailPurge(ctx, id, revokedAt)
}
