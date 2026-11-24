package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.ExternalMailPort = (*service)(nil)

func (s *service) ListExternalMailConnectorsPort(ctx context.Context) []domain.ExternalMailConnectorView {
	connectors := s.ListExternalMailConnectors(ctx)
	items := make([]domain.ExternalMailConnectorView, 0, len(connectors))
	for _, c := range connectors {
		items = append(items, domain.ExternalMailConnectorView{
			ID:       string(c.ID),
			AuthKind: string(c.AuthKind),
			Read:     c.Capabilities.Read,
			Send:     c.Capabilities.Send,
			Tenant:   c.Tenant != nil,
		})
	}
	return items
}

func (s *service) StartExternalMailAuthorizationPort(ctx context.Context, viewer domain.OrganizationContext, input domain.ExternalMailAuthorizationInput) (domain.ExternalMailAuthorizationStart, error) {
	started, err := s.StartExternalMailAuthorization(ctx, viewer, ExternalMailAuthorizationInput{
		Provider:     input.Provider,
		ConnectionID: input.ConnectionID,
		BackfillDays: input.BackfillDays,
		Sharing:      input.Sharing,
	})
	if err != nil {
		return domain.ExternalMailAuthorizationStart{}, err
	}
	return domain.ExternalMailAuthorizationStart{
		AuthorizationURL: started.AuthorizationURL,
		ExpiresAt:        started.ExpiresAt,
	}, nil
}

func (s *service) CompleteExternalMailAuthorizationPort(ctx context.Context, viewer domain.OrganizationContext, state, code string) (*domain.ExternalMailConnection, bool, error) {
	return s.CompleteExternalMailAuthorization(ctx, viewer, state, code)
}

func (s *service) ListExternalMailConnectionsPort(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.ExternalMailConnection, error) {
	return s.ListExternalMailConnections(ctx, viewer)
}

func (s *service) RevokeExternalMailConnectionPort(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	return s.RevokeExternalMailConnection(ctx, viewer, id)
}
