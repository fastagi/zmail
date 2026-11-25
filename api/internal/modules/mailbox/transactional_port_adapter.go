package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailTransactionalPort = (*service)(nil)

func (s *service) TransactionalEnabledPort() bool {
	return s.TransactionalPolicy().Enabled
}

func (s *service) SendTransactionalPort(ctx context.Context, actor domain.MailTransactionalActor, input domain.MailTransactionalSendInput) (*domain.TransactionalMessage, bool, error) {
	return s.SendTransactional(ctx, TransactionalActor{
		OrganizationID: actor.OrganizationID,
		UserID:         actor.UserID,
		APIKeyID:       actor.APIKeyID,
		Mode:           actor.Mode,
		Viewer:         actor.Viewer,
	}, TransactionalSendInput{
		Template:       input.Template,
		To:             input.To,
		IdempotencyKey: input.IdempotencyKey,
		Variables:      input.Variables,
	})
}

func (s *service) SendDirectTransactionalPort(ctx context.Context, actor domain.MailTransactionalActor, input domain.MailDirectSendInput) (*domain.TransactionalMessage, bool, error) {
	return s.SendDirectTransactional(ctx, TransactionalActor{
		OrganizationID: actor.OrganizationID,
		UserID:         actor.UserID,
		APIKeyID:       actor.APIKeyID,
		Mode:           actor.Mode,
		Viewer:         actor.Viewer,
	}, TransactionalDirectSendInput{
		From:           input.From,
		To:             input.To,
		Subject:        input.Subject,
		HTML:           input.HTML,
		Text:           input.Text,
		IdempotencyKey: input.IdempotencyKey,
	})
}

func (s *service) GetTransactionalPort(ctx context.Context, organizationID uint, mode domain.APIKeyMode, id uint) (*domain.TransactionalMessage, error) {
	return s.GetTransactional(ctx, organizationID, mode, id)
}

func (s *service) ListTransactionalLogPort(ctx context.Context, organizationID uint, filter domain.TransactionalLogFilter) (domain.MailTransactionalLogPage, error) {
	page, err := s.ListTransactionalLog(ctx, organizationID, filter)
	if err != nil {
		return domain.MailTransactionalLogPage{}, err
	}
	return domain.MailTransactionalLogPage{
		Items:      page.Items,
		NextCursor: page.NextCursor,
		Total24h:   page.Total24h,
	}, nil
}

func (s *service) GetTransactionalLogEntryPort(ctx context.Context, organizationID, id uint) (*domain.TransactionalMessage, error) {
	return s.GetTransactionalLogEntry(ctx, organizationID, id)
}

func (s *service) SendTransactionalTestPort(ctx context.Context, viewer domain.OrganizationContext, input domain.MailTransactionalTestInput) (*domain.TransactionalMessage, error) {
	return s.SendTransactionalTest(ctx, viewer, TransactionalTestInput{
		Template:  input.Template,
		Mode:      input.Mode,
		Variables: input.Variables,
	})
}

func (s *service) ListSendLanesPort(ctx context.Context, viewer domain.OrganizationContext) ([]domain.MailSendLaneUsage, time.Time, error) {
	lanes, computedAt, err := s.ListSendLanes(ctx, viewer)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := make([]domain.MailSendLaneUsage, 0, len(lanes))
	for _, lane := range lanes {
		out = append(out, domain.MailSendLaneUsage{
			Lane:    lane.Lane,
			Limit:   lane.Limit,
			Used:    lane.Used,
			Waiting: lane.Waiting,
		})
	}
	return out, computedAt, nil
}

func (s *service) ListSuppressionsPort(ctx context.Context, viewer domain.OrganizationContext, filter domain.MailSuppressionFilter) ([]domain.MailSuppression, *uint, error) {
	return s.ListSuppressions(ctx, viewer, filter)
}

func (s *service) RemoveSuppressionPort(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	return s.RemoveSuppression(ctx, viewer, id)
}
