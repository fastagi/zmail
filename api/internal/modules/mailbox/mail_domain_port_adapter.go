package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailDomainPort = (*service)(nil)

func (s *service) ListMailDomainsPort(ctx context.Context, organizationID uint) ([]*domain.MailDomain, error) {
	return s.ListMailDomains(ctx, organizationID)
}

func (s *service) GetMailDomainPort(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	return s.GetMailDomain(ctx, organizationID, id)
}

func (s *service) ConnectMailDomainPort(ctx context.Context, organizationID, userID uint, name string, capability domain.MailDomainCapability) (*domain.MailDomain, error) {
	return s.ConnectMailDomain(ctx, organizationID, userID, name, capability)
}

func (s *service) SetUpMailDomainPort(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	return s.SetUpMailDomain(ctx, organizationID, id)
}

func (s *service) CheckMailDomainPort(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	return s.CheckMailDomain(ctx, organizationID, id)
}

func (s *service) EnableMailDomainReceivingPort(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	return s.EnableMailDomainReceiving(ctx, organizationID, id)
}

func (s *service) SetDefaultMailDomainPort(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	return s.SetDefaultMailDomain(ctx, organizationID, id)
}

func (s *service) DeleteMailDomainPort(ctx context.Context, organizationID, id uint) error {
	return s.DeleteMailDomain(ctx, organizationID, id)
}

func (s *service) PreviewMailDomainPort(ctx context.Context, name string) (domain.MailDomainPreviewView, error) {
	preview, err := s.PreviewMailDomain(ctx, name)
	if err != nil {
		return domain.MailDomainPreviewView{}, err
	}
	return domain.MailDomainPreviewView{
		Name:                preview.Name,
		Apex:                preview.Apex,
		ExistingMX:          preview.ExistingMX,
		SuggestedCapability: preview.SuggestedCapability,
	}, nil
}

func (s *service) ListMailZonesPort(ctx context.Context, organizationID uint) ([]*domain.MailZone, error) {
	return s.ListMailZones(ctx, organizationID)
}

func (s *service) GetMailZonePort(ctx context.Context, organizationID, id uint) (*domain.MailZone, error) {
	return s.GetMailZone(ctx, organizationID, id)
}

func (s *service) DeleteMailZonePort(ctx context.Context, organizationID, id uint) error {
	return s.DeleteMailZone(ctx, organizationID, id)
}
