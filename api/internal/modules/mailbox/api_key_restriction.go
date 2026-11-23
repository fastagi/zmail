package mailbox

import (
	"context"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.SendingDomainDirectory = (*service)(nil)

// requireAPIKeySendingDomain refuses a send from an address whose domain the authenticating API key
// is not restricted to. Sessions and unrestricted keys pass.
func requireAPIKeySendingDomain(ctx context.Context, address string) error {
	if !domain.APIKeyRestrictionFromContext(ctx).AllowsSendingFrom(address) {
		return domain.ErrAPIKeyDomainNotAllowed
	}
	return nil
}

// SendingDomains lists, for a member, the domains the organization's mailboxes may send from: the
// shared domain and the organization's verified domains. A key may be restricted to these.
func (s *service) SendingDomains(ctx context.Context, organizationID, userID uint) ([]string, error) {
	if s.memberships == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if _, err := s.memberships.FindForUser(ctx, organizationID, userID); err != nil {
		return nil, err
	}
	domains, err := s.MailboxDomains(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(domains))
	for _, mailboxDomain := range domains {
		if name := strings.ToLower(strings.TrimSpace(mailboxDomain.Name)); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}
