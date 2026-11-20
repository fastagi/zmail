package mailbox

import (
	"context"
	"errors"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
)

// The operator console commands reach the mailbox starter only through this seam.
var _ domain.MailboxOperatorConsole = (*service)(nil)

// OrganizationMailboxSummary counts the organization's live mailboxes against the configured limit
// and lists its connected domains, for the operator. It applies no membership check.
func (s *service) OrganizationMailboxSummary(ctx context.Context, organizationID uint) (*domain.OrganizationMailboxSummary, error) {
	if organizationID == 0 {
		return nil, domain.ErrInvalidInput
	}
	if s.directory == nil {
		return nil, domain.ErrServiceUnavailable
	}
	count, err := s.directory.CountMailboxes(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	summary := &domain.OrganizationMailboxSummary{Mailboxes: count, MaxMailboxes: s.MaxMailboxes(), Domains: []*domain.MailDomain{}}
	if s.domains == nil {
		return summary, nil
	}
	domains, err := s.domains.ListDomains(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if domains != nil {
		summary.Domains = domains
	}
	return summary, nil
}

// FindConnectedDomain loads a connected domain by name, trimmed, lowercased, and without a final
// dot, whichever organization connected it; nil when no organization connected it.
func (s *service) FindConnectedDomain(ctx context.Context, name string) (*domain.MailDomain, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" {
		return nil, domain.ErrInvalidInput
	}
	if s.domains == nil {
		return nil, nil
	}
	found, err := s.domains.FindDomainByName(ctx, name)
	if errors.Is(err, ErrMailDomainNotFound) {
		return nil, nil
	}
	return found, err
}
