package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/transactional"
)

var _ domain.TransactionalMailRepository = (*repository)(nil)

const (
	transactionalLogDefaultPerPage = 50
	transactionalLogMaxPerPage     = 100
)

func (r *repository) transactionalRepo() *transactional.Repository {
	return transactional.NewRepository(r.db, ErrTransactionalMessageNotFound, ErrMailMessageInvalid)
}

func (r *repository) CreateTransactional(ctx context.Context, message *domain.TransactionalMessage) (bool, error) {
	return r.transactionalRepo().CreateTransactional(ctx, message)
}

func (r *repository) FindTransactionalByIdempotencyKey(ctx context.Context, organizationID uint, mode domain.APIKeyMode, key string) (*domain.TransactionalMessage, error) {
	return r.transactionalRepo().FindTransactionalByIdempotencyKey(ctx, organizationID, mode, key)
}

func (r *repository) ReleaseIdempotencyKey(ctx context.Context, id uint) error {
	return r.transactionalRepo().ReleaseIdempotencyKey(ctx, id)
}

func (r *repository) FindTransactional(ctx context.Context, organizationID, id uint) (*domain.TransactionalMessage, error) {
	return r.transactionalRepo().FindTransactional(ctx, organizationID, id)
}

func (r *repository) FindTransactionalByID(ctx context.Context, id uint) (*domain.TransactionalMessage, error) {
	return r.transactionalRepo().FindTransactionalByID(ctx, id)
}

func (r *repository) OldestQueuedTransactional(ctx context.Context) (*time.Time, error) {
	return r.transactionalRepo().OldestQueuedTransactional(ctx)
}

func (r *repository) FindTransactionalByProviderID(ctx context.Context, providerMessageID string) (*domain.TransactionalMessage, error) {
	return r.transactionalRepo().FindTransactionalByProviderID(ctx, providerMessageID)
}

func (r *repository) CountTransactionalAttemptsSince(ctx context.Context, organizationID uint, since time.Time) (int64, error) {
	return r.transactionalRepo().CountTransactionalAttemptsSince(ctx, organizationID, since)
}

func (r *repository) CountTransactionalWaiting(ctx context.Context, organizationID uint) (int64, error) {
	return r.transactionalRepo().CountTransactionalWaiting(ctx, organizationID)
}

func (r *repository) LeaseTransactional(ctx context.Context, organizationID uint, ids []uint, now, leaseUntil, windowStart time.Time, laneLimit, limit int) ([]*domain.TransactionalMessage, error) {
	return r.transactionalRepo().LeaseTransactional(ctx, organizationID, ids, now, leaseUntil, windowStart, laneLimit, limit)
}

func (r *repository) TransactionalDueOrganizations(ctx context.Context, now time.Time, limit int) ([]uint, error) {
	return r.transactionalRepo().TransactionalDueOrganizations(ctx, now, limit)
}

func (r *repository) StartTransactionalAttempt(ctx context.Context, id uint, lease, at time.Time) (bool, error) {
	return r.transactionalRepo().StartTransactionalAttempt(ctx, id, lease, at)
}

func (r *repository) FinishTransactional(ctx context.Context, id uint, lease time.Time, outcome domain.TransactionalOutcome) (bool, error) {
	return r.transactionalRepo().FinishTransactional(ctx, id, lease, outcome)
}

func (r *repository) ScheduleTransactionalRetry(ctx context.Context, id uint, lease, next time.Time, failureCode string) (bool, error) {
	return r.transactionalRepo().ScheduleTransactionalRetry(ctx, id, lease, next, failureCode)
}

func (r *repository) ExpireTransactional(ctx context.Context, now time.Time, limit int) ([]*domain.TransactionalMessage, error) {
	return r.transactionalRepo().ExpireTransactional(ctx, now, limit)
}

func (r *repository) FailUnknownTransactional(ctx context.Context, now time.Time, limit int) ([]*domain.TransactionalMessage, error) {
	return r.transactionalRepo().FailUnknownTransactional(ctx, now, limit)
}

func (r *repository) PurgeTransactional(ctx context.Context, before time.Time, limit int) (int, error) {
	return r.transactionalRepo().PurgeTransactional(ctx, before, limit)
}

func (r *repository) AddTransactionalEvents(ctx context.Context, id uint, events []domain.MailEvent) (int, domain.MailEventType, error) {
	return r.transactionalRepo().AddTransactionalEvents(ctx, id, events)
}

func (r *repository) ListTransactionalEvents(ctx context.Context, id uint, limit int) ([]domain.MailEvent, error) {
	return r.transactionalRepo().ListTransactionalEvents(ctx, id, limit)
}

func (r *repository) ListTransactionalLog(ctx context.Context, organizationID uint, filter domain.TransactionalLogFilter) ([]*domain.TransactionalMessage, error) {
	return r.transactionalRepo().ListTransactionalLog(ctx, organizationID, filter)
}

func (r *repository) CountTransactionalSince(ctx context.Context, organizationID uint, mode domain.APIKeyMode, templateKey string, since time.Time, limit int) (int64, error) {
	return r.transactionalRepo().CountTransactionalSince(ctx, organizationID, mode, templateKey, since, limit)
}
