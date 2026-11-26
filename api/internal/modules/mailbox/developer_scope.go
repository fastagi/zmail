package mailbox

import (
	"context"
	"errors"

	"github.com/fastagi/zmail/api/internal/domain"
)

// requireDeveloperSender lets a viewer act on transactional mail sent from address: owners and
// admins always may; a member needs the developer grant, and the address's domain must be in its
// scope as it stands now. An all-domains scope covers the organization's verified domains, never
// the platform's shared domain.
func (s *service) requireDeveloperSender(ctx context.Context, viewer domain.OrganizationContext, address string) error {
	grant, unrestricted, ok := viewer.DeveloperScope()
	if !ok {
		return domain.ErrOrganizationFeatureNotGranted
	}
	if unrestricted {
		return nil
	}
	name := domain.AddressDomain(address)
	verified := false
	if grant.AllDomains {
		var err error
		if verified, err = s.isVerifiedOrganizationDomain(ctx, viewer.OrganizationID, name); err != nil {
			return err
		}
	}
	if !grant.AllowsDomain(name, verified) {
		return domain.ErrDeveloperDomainNotAllowed
	}
	return nil
}

// isVerifiedOrganizationDomain reports whether name is a verified custom domain of the organization.
func (s *service) isVerifiedOrganizationDomain(ctx context.Context, organizationID uint, name string) (bool, error) {
	if s.domains == nil || name == "" || s.isSharedDomain(name) {
		return false, nil
	}
	mailDomain, err := s.domains.FindDomainByName(ctx, name)
	switch {
	case errors.Is(err, ErrMailDomainNotFound):
		return false, nil
	case err != nil:
		return false, err
	}
	return mailDomain.OrganizationID == organizationID && mailDomain.Status == domain.MailDomainStatusVerified, nil
}

// canUseDeveloperTools reports whether the viewer may read usage and manage suppressions: owners,
// admins, and members holding a developer grant.
func canUseDeveloperTools(viewer domain.OrganizationContext) bool {
	_, _, ok := viewer.DeveloperScope()
	return ok
}

// TemplatesOutsideSendingScope marks, for a developer member, the transactional templates whose
// sender is outside the grant; owners and admins see none marked.
func (s *service) TemplatesOutsideSendingScope(
	ctx context.Context,
	viewer domain.OrganizationContext,
	templates []*domain.MailTemplate,
) map[uint]bool {
	marked := map[uint]bool{}
	if _, unrestricted, ok := viewer.DeveloperScope(); !ok || unrestricted {
		return marked
	}
	for _, template := range templates {
		if template == nil || template.Kind != domain.MailTemplateTransactional {
			continue
		}
		if err := s.requireDeveloperSender(ctx, viewer, template.FromAddress); err != nil {
			marked[template.ID] = true
		}
	}
	return marked
}
