package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.PlatformAccountRepository = (*repository)(nil)

func (r *repository) ListPlatformAccounts(ctx context.Context, organizationID uint, filter domain.PlatformAccountFilter) ([]*domain.PlatformAccount, int64, error) {
	return r.rosterRepo().ListPlatformAccounts(ctx, organizationID, filter)
}

func (r *repository) FindPlatformAccount(ctx context.Context, organizationID, id uint) (*domain.PlatformAccount, error) {
	return r.rosterRepo().FindPlatformAccount(ctx, organizationID, id)
}

func (r *repository) CreatePlatformAccount(ctx context.Context, account *domain.PlatformAccount, actorUserID uint) error {
	return r.rosterRepo().CreatePlatformAccount(ctx, account, actorUserID)
}

func (r *repository) UpdatePlatformAccount(ctx context.Context, account *domain.PlatformAccount, actorUserID uint) error {
	return r.rosterRepo().UpdatePlatformAccount(ctx, account, actorUserID)
}

func (r *repository) DeletePlatformAccount(ctx context.Context, organizationID, id, actorUserID uint) error {
	return r.rosterRepo().DeletePlatformAccount(ctx, organizationID, id, actorUserID)
}

func (r *repository) ListPlatformAccountEvents(ctx context.Context, organizationID, accountID uint, page, size int) ([]*domain.PlatformAccountEvent, int64, error) {
	return r.rosterRepo().ListPlatformAccountEvents(ctx, organizationID, accountID, page, size)
}

func (r *repository) HandOverPlatformAccounts(ctx context.Context, organizationID, fromUserID, toUserID, actorUserID uint) ([]uint, error) {
	return r.rosterRepo().HandOverPlatformAccounts(ctx, organizationID, fromUserID, toUserID, actorUserID)
}
