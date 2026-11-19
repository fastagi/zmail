package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// zoneCleanupTimeout bounds removing a zone this request created, which runs even when the
// request itself was canceled.
const zoneCleanupTimeout = 30 * time.Second

// apexDomain returns the registrable domain of name, the label right below its public suffix:
// example.com for mail.example.com, example.com.cn for a.b.example.com.cn. A name that is itself
// a public suffix has none.
func apexDomain(name string) (string, error) {
	apex, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return "", ErrMailDomainInvalid
	}
	return apex, nil
}

// domainZone is the DNS zone a new mail domain goes into.
type domainZone struct {
	providerID string
	name       string
	// managed is the organization's managed zone; nil for a zone the operator manages.
	managed *domain.MailZone
	// created is true when this request created the managed zone.
	created bool
}

// resolveZone finds the zone for a new domain: a managed zone of the organization for its apex,
// else a zone the operator added to the provider account, else a new managed zone for the apex.
// A managed zone of another organization makes the domain taken.
func (s *service) resolveZone(ctx context.Context, organizationID, createdBy uint, name string) (domainZone, error) {
	apex, err := apexDomain(name)
	if err != nil {
		return domainZone{}, err
	}
	managed, err := s.zones.FindZoneByName(ctx, apex)
	switch {
	case err == nil:
		if managed.OrganizationID != organizationID {
			return domainZone{}, ErrMailDomainTaken
		}
		return domainZone{providerID: managed.CloudflareZoneID, name: managed.Name, managed: managed}, nil
	case !errors.Is(err, ErrMailZoneNotFound):
		return domainZone{}, err
	}
	zone, err := s.domainProvider.FindZone(ctx, name)
	if err == nil {
		return domainZone{providerID: zone.ID, name: zone.Name}, nil
	}
	if !errors.Is(err, mailtransport.ErrZoneNotFound) {
		return domainZone{}, providerError(err)
	}
	managed, err = s.createManagedZone(ctx, organizationID, createdBy, apex)
	if err != nil {
		return domainZone{}, err
	}
	return domainZone{providerID: managed.CloudflareZoneID, name: managed.Name, managed: managed, created: true}, nil
}

// createManagedZone adds apex to the provider account and stores it as a pending managed zone.
// The limit is checked before the provider call so a refused request leaves no zone behind; the
// repository checks it again under its lock.
func (s *service) createManagedZone(ctx context.Context, organizationID, createdBy uint, apex string) (*domain.MailZone, error) {
	limit := s.settings.MaxZonesPerOrganization
	owned, err := s.zones.ListZones(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if len(owned) >= limit {
		return nil, ErrMailZoneLimitReached
	}
	details, err := s.domainProvider.CreateZone(ctx, apex)
	if err != nil {
		if errors.Is(err, mailtransport.ErrZoneExists) {
			// Another provider account holds the domain; this one can never manage it.
			return nil, ErrMailDomainTaken
		}
		return nil, providerError(err)
	}
	if err := s.domainProvider.ScanRecords(ctx, details.ID); err != nil {
		// The organization can still add its records by hand before moving the name servers.
		slog.WarnContext(ctx, "existing records of a new mail zone were not imported",
			slog.String("zone", apex), slog.String("error", err.Error()))
	}
	zone := &domain.MailZone{
		OrganizationID:      organizationID,
		Name:                apex,
		CloudflareZoneID:    details.ID,
		Status:              domain.MailZoneStatusPending,
		NameServers:         details.NameServers,
		OriginalNameServers: details.OriginalNameServers,
		CreatedBy:           createdBy,
	}
	if err := s.zones.CreateZone(ctx, zone, limit); err != nil {
		s.deleteProviderZone(ctx, apex, details.ID)
		return nil, err
	}
	return zone, nil
}

// discardManagedZone removes a managed zone this request created when its domain could not be
// stored, so the zone does not count against the organization's limit. A zone that is no longer
// pending, or that a concurrent connect already put a domain in, is kept.
func (s *service) discardManagedZone(ctx context.Context, zone *domain.MailZone) {
	cleanupContext := context.WithoutCancel(ctx)
	current, err := s.zones.FindZone(cleanupContext, zone.OrganizationID, zone.ID)
	if err != nil || current.Status != domain.MailZoneStatusPending {
		if err != nil && !errors.Is(err, ErrMailZoneNotFound) {
			slog.WarnContext(ctx, "unused mail zone was not removed",
				slog.String("zone", zone.Name), slog.String("error", err.Error()))
		}
		return
	}
	if err := s.zones.DeleteZone(cleanupContext, zone.OrganizationID, zone.ID); err != nil {
		if !errors.Is(err, ErrMailZoneInUse) {
			slog.WarnContext(ctx, "unused mail zone was not removed",
				slog.String("zone", zone.Name), slog.String("error", err.Error()))
		}
		return
	}
	s.deleteProviderZone(ctx, zone.Name, zone.CloudflareZoneID)
}

// deleteProviderZone removes a zone this request added to the provider account. A failure leaves
// a pending zone nobody delegates to, which is logged for the operator.
func (s *service) deleteProviderZone(ctx context.Context, name, providerID string) {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), zoneCleanupTimeout)
	defer cancel()
	if err := s.domainProvider.DeleteZone(cleanupContext, providerID); err != nil {
		slog.WarnContext(ctx, "unused zone was not removed from the DNS provider",
			slog.String("zone", name), slog.String("zone_id", providerID), slog.String("error", err.Error()))
	}
}

// attachZones fills Zone on the domains that live in a managed zone of the organization.
func (s *service) attachZones(ctx context.Context, organizationID uint, domains []*domain.MailDomain) error {
	managed := false
	for _, mailDomain := range domains {
		if mailDomain.MailZoneID != nil {
			managed = true
			break
		}
	}
	if !managed {
		return nil
	}
	zones, err := s.zones.ListZones(ctx, organizationID)
	if err != nil {
		return err
	}
	byID := make(map[uint]*domain.MailZone, len(zones))
	for _, zone := range zones {
		byID[zone.ID] = zone
	}
	for _, mailDomain := range domains {
		if mailDomain.MailZoneID != nil {
			mailDomain.Zone = byID[*mailDomain.MailZoneID]
		}
	}
	return nil
}
