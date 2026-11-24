package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailBroadcastPort = (*service)(nil)

func (s *service) ListBroadcastsPort(ctx context.Context, viewer domain.OrganizationContext, page, perPage int) ([]*domain.MailBroadcast, int64, error) {
	return s.ListBroadcasts(ctx, viewer, page, perPage)
}

func (s *service) GetBroadcastPort(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, error) {
	return s.GetBroadcast(ctx, viewer, id)
}

func (s *service) BroadcastProgressPort(ctx context.Context, broadcast *domain.MailBroadcast) (*domain.MailBroadcastProgress, error) {
	return s.BroadcastProgress(ctx, broadcast)
}

func (s *service) CreateBroadcastPort(ctx context.Context, viewer domain.OrganizationContext, input domain.MailBroadcastInput) (*domain.MailBroadcast, error) {
	return s.CreateBroadcast(ctx, viewer, BroadcastInput(input))
}

func (s *service) UpdateBroadcastPort(ctx context.Context, viewer domain.OrganizationContext, id uint, input domain.MailBroadcastInput) (*domain.MailBroadcast, error) {
	return s.UpdateBroadcast(ctx, viewer, id, BroadcastInput(input))
}

func (s *service) DeleteBroadcastPort(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	return s.DeleteBroadcast(ctx, viewer, id)
}

func (s *service) SendBroadcastPort(ctx context.Context, viewer domain.OrganizationContext, id uint, scheduledAt *time.Time) (*domain.MailBroadcast, error) {
	return s.SendBroadcast(ctx, viewer, id, scheduledAt)
}

func (s *service) CancelBroadcastPort(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, bool, error) {
	return s.CancelBroadcast(ctx, viewer, id)
}

func (s *service) PauseBroadcastPort(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, bool, error) {
	return s.PauseBroadcast(ctx, viewer, id)
}

func (s *service) ResumeBroadcastPort(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, error) {
	return s.ResumeBroadcast(ctx, viewer, id)
}

func (s *service) ListBroadcastRecipientsPort(ctx context.Context, viewer domain.OrganizationContext, id uint, status domain.MailRecipientStatus, page, perPage int) ([]*domain.MailBroadcastRecipient, int64, error) {
	return s.ListBroadcastRecipients(ctx, viewer, id, status, page, perPage)
}

func (s *service) UnsubscribePort(ctx context.Context, token string) error {
	return s.Unsubscribe(ctx, token)
}
