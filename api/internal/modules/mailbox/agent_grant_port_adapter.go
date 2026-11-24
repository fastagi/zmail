package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailAgentGrantPort = (*service)(nil)

func (s *service) IssueAgentGrantPort(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, input domain.IssueMailAgentGrantInput) (*domain.MailAgentGrant, error) {
	return s.IssueAgentGrant(ctx, viewer, mailboxID, IssueAgentGrantInput{
		APIKeyID:      input.APIKeyID,
		Capabilities:  input.Capabilities,
		RevealSecrets: input.RevealSecrets,
		Senders:       input.Senders,
		LabelIDs:      input.LabelIDs,
		ExpiresAt:     input.ExpiresAt,
	})
}

func (s *service) ListAgentGrantsPort(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, page, perPage int) ([]*domain.MailAgentGrant, int64, error) {
	return s.ListAgentGrants(ctx, viewer, mailboxID, page, perPage)
}

func (s *service) RevokeAgentGrantPort(ctx context.Context, viewer domain.OrganizationContext, mailboxID, grantID uint) error {
	return s.RevokeAgentGrant(ctx, viewer, mailboxID, grantID)
}

func (s *service) AgentGrantActivityPort(ctx context.Context, viewer domain.OrganizationContext, mailboxID, grantID uint) ([]*domain.MailAgentAuditEntry, error) {
	return s.AgentGrantActivity(ctx, viewer, mailboxID, grantID)
}

func (s *service) ListAgentDraftsPort(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, page, perPage int) ([]*domain.MailAgentDraft, int64, error) {
	return s.ListAgentDrafts(ctx, viewer, mailboxID, page, perPage)
}

func (s *service) AgentMailEventsPort(ctx context.Context, viewer domain.OrganizationContext, apiKeyID, after uint, limit int) (domain.MailAgentFeedPage, error) {
	feed, err := s.AgentMailEvents(ctx, viewer, apiKeyID, after, limit)
	if err != nil {
		return domain.MailAgentFeedPage{}, err
	}
	return domain.MailAgentFeedPage{
		Events:     feed.Events,
		NextCursor: feed.NextCursor,
		HasMore:    feed.HasMore,
	}, nil
}

func (s *service) SaveAgentDraftPort(ctx context.Context, viewer domain.OrganizationContext, apiKeyID uint, input domain.MailAgentDraftInput) (*domain.MailAgentDraft, bool, error) {
	return s.SaveAgentDraft(ctx, viewer, apiKeyID, AgentDraftInput{
		MessageID: input.MessageID,
		Text:      input.Text,
		HTML:      input.HTML,
	})
}
