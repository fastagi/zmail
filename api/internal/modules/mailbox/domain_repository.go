package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/domainsetup"
)

var _ domain.MailDomainRepository = (*repository)(nil)

// MailDomainPO is the persistence object of a connected domain.
type MailDomainPO = domainsetup.MailDomainPO

func (r *repository) domainSetupRepo() *domainsetup.Repository {
	return domainsetup.NewRepository(r.db, domainsetup.Errors{
		DomainNotFound:     ErrMailDomainNotFound,
		DomainTaken:        ErrMailDomainTaken,
		DomainLimitReached: ErrMailDomainLimitReached,
		DomainInUse:        ErrMailDomainInUse,
		DomainNotVerified:  ErrMailDomainNotVerified,
		ZoneNotFound:       ErrMailZoneNotFound,
		ZoneLimitReached:   ErrMailZoneLimitReached,
		ZoneInUse:          ErrMailZoneInUse,
	}, zoneReservationPrefix)
}

func truncateRunes(value string, limit int) string {
	return domainsetup.TruncateRunes(value, limit)
}

func (r *repository) CreateDomain(ctx context.Context, mailDomain *domain.MailDomain, limit int) error {
	return r.domainSetupRepo().CreateDomain(ctx, mailDomain, limit)
}

func (r *repository) ListDomains(ctx context.Context, organizationID uint) ([]*domain.MailDomain, error) {
	return r.domainSetupRepo().ListDomains(ctx, organizationID)
}

func (r *repository) FindDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	return r.domainSetupRepo().FindDomain(ctx, organizationID, id)
}

func (r *repository) FindDomainByName(ctx context.Context, name string) (*domain.MailDomain, error) {
	return r.domainSetupRepo().FindDomainByName(ctx, name)
}

func (r *repository) UpdateDomain(ctx context.Context, mailDomain *domain.MailDomain) error {
	return r.domainSetupRepo().UpdateDomain(ctx, mailDomain)
}

func (r *repository) DeleteDomain(ctx context.Context, organizationID, id uint) error {
	return r.domainSetupRepo().DeleteDomain(ctx, organizationID, id)
}

func (r *repository) SetMailboxRoutingRule(ctx context.Context, mailboxID uint, ruleID string) error {
	return r.domainSetupRepo().SetMailboxRoutingRule(ctx, mailboxID, ruleID)
}

func (r *repository) SetDomainCapability(ctx context.Context, organizationID, id uint, capability domain.MailDomainCapability) error {
	return r.domainSetupRepo().SetDomainCapability(ctx, organizationID, id, capability)
}

func (r *repository) SetDefaultDomain(ctx context.Context, organizationID, id uint) error {
	return r.domainSetupRepo().SetDefaultDomain(ctx, organizationID, id)
}

func (r *repository) CountMailboxesByDomain(ctx context.Context, organizationID uint) (map[string]int, error) {
	return r.domainSetupRepo().CountMailboxesByDomain(ctx, organizationID)
}

func (r *repository) ListMailboxesWithoutRoutingRule(ctx context.Context, organizationID uint, domainName string) ([]*domain.Mailbox, error) {
	return r.domainSetupRepo().ListMailboxesWithoutRoutingRule(ctx, organizationID, domainName)
}
