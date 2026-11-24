package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailTriagePort = (*service)(nil)

func (s *service) GetTriageSettingPort(ctx context.Context, viewer domain.OrganizationContext) (domain.MailTriageSettingView, error) {
	setting, err := s.TriageSetting(ctx, viewer)
	if err != nil {
		return domain.MailTriageSettingView{}, err
	}
	return domain.MailTriageSettingView{
		MailTriageSettings: setting.MailTriageSettings,
		Available:          setting.Available,
		InstanceDailyCap:   setting.InstanceDailyCap,
	}, nil
}

func (s *service) SaveTriageSettingPort(ctx context.Context, viewer domain.OrganizationContext, input domain.MailTriageSettingInput) (domain.MailTriageSettingView, error) {
	setting, err := s.SaveTriageSetting(ctx, viewer, TriageSettingInput{
		Enabled:         input.Enabled,
		DailyMessageCap: input.DailyMessageCap,
		DraftEnabled:    input.DraftEnabled,
	})
	if err != nil {
		return domain.MailTriageSettingView{}, err
	}
	return domain.MailTriageSettingView{
		MailTriageSettings: setting.MailTriageSettings,
		Available:          setting.Available,
		InstanceDailyCap:   setting.InstanceDailyCap,
	}, nil
}

func (s *service) OverrideTriagePort(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint, input domain.MailTriageOverrideInput) (*domain.MailTriage, error) {
	return s.OverrideTriage(ctx, viewer, mailboxID, messageID, TriageOverrideInput{
		Category: input.Category,
		DueAt:    input.DueAt,
	})
}

func (s *service) DraftReplyPort(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint) (domain.MailReplyDraft, error) {
	draft, err := s.DraftReply(ctx, viewer, mailboxID, messageID)
	if err != nil {
		return domain.MailReplyDraft{}, err
	}
	basis := make([]domain.MailReplyDraftBasis, 0, len(draft.Basis))
	for _, item := range draft.Basis {
		basis = append(basis, domain.MailReplyDraftBasis{
			Kind:  item.Kind,
			ID:    item.ID,
			Label: item.Label,
		})
	}
	return domain.MailReplyDraft{
		Body:  draft.Body,
		Basis: basis,
	}, nil
}
