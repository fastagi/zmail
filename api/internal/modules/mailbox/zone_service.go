package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// zoneCleanupTimeout bounds removing a zone this request created, which runs even when the
// request itself was canceled.
const zoneCleanupTimeout = 30 * time.Second

// pendingZoneLifetime is how long a managed zone may wait for its registrar delegation. Nobody
// delegates to a zone that never became active, so removing it later affects no one.
const pendingZoneLifetime = 14 * 24 * time.Hour

// providerZoneActive is the provider status of a zone the registrar delegates to it.
const providerZoneActive = "active"

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

// errZoneNoLongerPending keeps a zone that became active before an unused-zone cleanup ran.
var errZoneNoLongerPending = errors.New("mail zone is no longer pending")

// discardManagedZone removes a managed zone this request created when its domain could not be
// stored, so the zone does not count against the organization's limit. A zone that is no longer
// pending, or that a concurrent connect already put a domain in, is kept. The provider zone is
// removed only while the row is locked as pending; a provider failure is logged and the row goes.
func (s *service) discardManagedZone(ctx context.Context, zone *domain.MailZone) {
	cleanupContext := context.WithoutCancel(ctx)
	err := s.zones.DeleteZone(cleanupContext, zone.OrganizationID, zone.ID, func(providerContext context.Context, locked *domain.MailZone) error {
		if locked.Status != domain.MailZoneStatusPending {
			return errZoneNoLongerPending
		}
		s.deleteProviderZone(providerContext, locked.Name, locked.CloudflareZoneID)
		return nil
	})
	if err != nil && !errors.Is(err, ErrMailZoneInUse) && !errors.Is(err, ErrMailZoneNotFound) &&
		!errors.Is(err, errZoneNoLongerPending) {
		slog.WarnContext(ctx, "unused mail zone was not removed",
			slog.String("zone", zone.Name), slog.String("error", err.Error()))
	}
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

// CheckPendingZones checks up to limit pending managed zones, the least recently checked first:
// a zone the provider now serves becomes active and its pending domains are set up, and a zone
// that stayed pending past pendingZoneLifetime is removed with its domains. One zone's failure is
// logged and does not stop the others. It returns how many zones it checked.
func (s *service) CheckPendingZones(ctx context.Context, limit int) (int, error) {
	if !s.domainsEnabled() {
		// Without custom domains there are no managed zones to check.
		return 0, nil
	}
	zones, err := s.zones.ListPendingZones(ctx, limit)
	if err != nil {
		return 0, err
	}
	for _, zone := range zones {
		if err := s.checkPendingZone(ctx, zone); err != nil {
			slog.WarnContext(ctx, "pending mail zone check failed",
				slog.String("zone", zone.Name), slog.String("error", err.Error()))
		}
	}
	return len(zones), nil
}

func (s *service) checkPendingZone(ctx context.Context, zone *domain.MailZone) error {
	active, err := s.refreshZone(ctx, zone)
	switch {
	case providerZoneGone(err):
		// Nobody can delegate to a zone the provider no longer has.
		return s.expireZone(ctx, zone)
	case err != nil:
		return err
	case active:
		// The registrar may have delegated the zone just before its deadline; an active zone is
		// never removed.
		return nil
	case s.now().UTC().Sub(zone.CreatedAt) > pendingZoneLifetime:
		return s.expireZone(ctx, zone)
	}
	return nil
}

// expireZone removes a pending zone from the provider together with its row and domains. The
// repository keeps the row locked as pending while the provider call runs and commits only after
// it succeeds, so a zone a concurrent check activates is never removed at the provider, and a
// provider failure leaves the row for the next pass to retry.
func (s *service) expireZone(ctx context.Context, zone *domain.MailZone) error {
	err := s.zones.DeletePendingZone(ctx, zone.ID, func(providerContext context.Context) error {
		if err := s.domainProvider.DeleteZone(providerContext, zone.CloudflareZoneID); err != nil && !providerZoneGone(err) {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrMailZoneNotFound) {
			// Already removed, or activated by a concurrent check.
			return nil
		}
		return err
	}
	slog.InfoContext(ctx, "pending mail zone expired",
		slog.String("zone", zone.Name), slog.String("zone_id", zone.CloudflareZoneID))
	return nil
}

// refreshZone reads the zone's status from the provider and reports whether the provider serves
// it. It only ever records when the zone was checked; a zone the provider serves is activated by
// one conditional update, and only the caller that wins it sets the zone's pending domains up.
func (s *service) refreshZone(ctx context.Context, zone *domain.MailZone) (bool, error) {
	details, lookupErr := s.domainProvider.GetZone(ctx, zone.CloudflareZoneID)
	now := s.now().UTC()
	zone.LastCheckedAt = &now
	if lookupErr != nil || details.Status != providerZoneActive {
		if err := s.zones.TouchZone(ctx, zone.ID, now); err != nil {
			return false, err
		}
		return false, lookupErr
	}
	activated, err := s.zones.ActivateZone(ctx, zone.ID, now)
	if err != nil {
		return false, err
	}
	if activated {
		zone.Status = domain.MailZoneStatusActive
		zone.ActivatedAt = &now
		slog.InfoContext(ctx, "mail zone activated", slog.String("zone", zone.Name))
		s.setUpPendingDomains(ctx, zone)
	}
	return true, nil
}

// setUpPendingDomains starts the setup of every pending domain in a zone that just became active.
func (s *service) setUpPendingDomains(ctx context.Context, zone *domain.MailZone) {
	domains, err := s.zones.ListDomainsInZone(ctx, zone.ID)
	if err != nil {
		slog.WarnContext(ctx, "domains of an activated mail zone were not set up",
			slog.String("zone", zone.Name), slog.String("error", err.Error()))
		return
	}
	for _, mailDomain := range domains {
		if mailDomain.Status != domain.MailDomainStatusPending {
			continue
		}
		if _, err := s.startSetUp(ctx, mailDomain); err != nil {
			slog.WarnContext(ctx, "mail domain setup did not start",
				slog.String("domain", mailDomain.Name), slog.String("error", err.Error()))
		}
	}
}

// pendingZoneOf returns the managed zone of a domain while it is pending, or nil when the domain
// is in an active or operator zone.
func (s *service) pendingZoneOf(ctx context.Context, mailDomain *domain.MailDomain) (*domain.MailZone, error) {
	if mailDomain.MailZoneID == nil {
		return nil, nil
	}
	zone, err := s.zones.FindZone(ctx, mailDomain.OrganizationID, *mailDomain.MailZoneID)
	if err != nil {
		return nil, err
	}
	if zone.Status != domain.MailZoneStatusPending {
		return nil, nil
	}
	return zone, nil
}

// checkDomainZone asks the provider to look for the zone's name servers now, then checks the
// zone as the poller does. A rate-limited request is reported on the domain, not as an error.
// The domain stays pending, without records, until its zone is active.
func (s *service) checkDomainZone(ctx context.Context, mailDomain *domain.MailDomain, zone *domain.MailZone) (*domain.MailDomain, error) {
	limited := false
	if err := s.domainProvider.RequestActivationCheck(ctx, zone.CloudflareZoneID); err != nil {
		if !errors.Is(err, mailtransport.ErrActivationCheckLimited) {
			return nil, providerError(err)
		}
		limited = true
	}
	active, err := s.refreshZone(ctx, zone)
	if err != nil {
		return nil, providerError(err)
	}
	current := mailDomain
	if active {
		// The activation started the setup of the domain; read it as that left it.
		if current, err = s.domains.FindDomain(ctx, mailDomain.OrganizationID, mailDomain.ID); err != nil {
			return nil, err
		}
	}
	presented, err := s.present(ctx, current)
	if err != nil {
		return nil, err
	}
	presented.ZoneCheckLimited = limited
	return presented, nil
}

// providerZoneGone reports whether the provider no longer has the zone.
func providerZoneGone(err error) bool {
	var failure *mailtransport.DomainError
	return errors.As(err, &failure) && failure.Status == http.StatusNotFound
}

// Public DNS lookups for previews and zone removal.
const (
	// publicLookupTimeout bounds one public DNS lookup.
	publicLookupTimeout = 5 * time.Second
	// maxPreviewMX bounds the MX hosts a preview returns.
	maxPreviewMX = 20
	// routingMXSuffix ends every MX host of Cloudflare Email Routing.
	routingMXSuffix = ".mx.cloudflare.net"
	// providerNameServerSuffix ends every name server Cloudflare assigns to a zone.
	providerNameServerSuffix = ".ns.cloudflare.com"
)

// mxResolver reads a name's public MX records; *net.Resolver implements it.
type mxResolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

// nsResolver reads a name's public NS records; *net.Resolver implements it.
type nsResolver interface {
	LookupNS(ctx context.Context, name string) ([]*net.NS, error)
}

// MailDomainPreview is what connecting a domain would find, read before connecting it.
type MailDomainPreview struct {
	Name string
	Apex string
	// ExistingMX are the public MX hosts of the name, lowercase without the trailing dot.
	ExistingMX []string
	// SuggestedCapability is send_only when another mail service receives for the name.
	SuggestedCapability domain.MailDomainCapability
}

// PreviewMailDomain normalizes a name, finds its apex, and reads its public MX records. A failed
// lookup, including a name without MX records, suggests send_receive: nothing receives there yet.
func (s *service) PreviewMailDomain(ctx context.Context, name string) (*MailDomainPreview, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	name, err := s.normalizeDomainName(name)
	if err != nil {
		return nil, err
	}
	apex, err := apexDomain(name)
	if err != nil {
		return nil, err
	}
	preview := &MailDomainPreview{
		Name: name, Apex: apex, ExistingMX: []string{}, SuggestedCapability: domain.MailDomainSendReceive,
	}
	lookupContext, cancel := context.WithTimeout(ctx, publicLookupTimeout)
	defer cancel()
	records, err := s.mxResolver.LookupMX(lookupContext, name)
	if err != nil {
		return preview, nil
	}
	for _, record := range records {
		host := strings.TrimSuffix(strings.ToLower(record.Host), ".")
		if host == "" || len(preview.ExistingMX) == maxPreviewMX {
			// A null MX (".") states that the name receives no mail at all.
			continue
		}
		preview.ExistingMX = append(preview.ExistingMX, host)
		if !strings.HasSuffix(host, routingMXSuffix) {
			preview.SuggestedCapability = domain.MailDomainSendOnly
		}
	}
	return preview, nil
}

func (s *service) ListMailZones(ctx context.Context, organizationID uint) ([]*domain.MailZone, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	return s.zones.ListZones(ctx, organizationID)
}

func (s *service) GetMailZone(ctx context.Context, organizationID, id uint) (*domain.MailZone, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	return s.zones.FindZone(ctx, organizationID, id)
}

// DeleteMailZone removes a managed zone no mail domain uses. An active zone goes only once the
// public name servers of its apex are no longer Cloudflare's, so removing it never takes down DNS
// the registrar still points at it; a failed lookup refuses as well. A pending zone was never
// delegated and skips the lookup. The zone stays locked while this runs, and a provider refusal
// keeps the row.
func (s *service) DeleteMailZone(ctx context.Context, organizationID, id uint) error {
	if err := s.requireDomains(); err != nil {
		return err
	}
	err := s.zones.DeleteZone(ctx, organizationID, id, func(providerContext context.Context, zone *domain.MailZone) error {
		if zone.Status != domain.MailZoneStatusPending {
			if err := s.requireUndelegated(providerContext, zone.Name); err != nil {
				return err
			}
		}
		if err := s.domainProvider.DeleteZone(providerContext, zone.CloudflareZoneID); err != nil && !providerZoneGone(err) {
			return providerError(err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "mail zone removed", slog.Uint64("zone_id", uint64(id)))
	return nil
}

// requireUndelegated refuses while any public name server of the apex is Cloudflare's, or when
// the name servers cannot be read.
func (s *service) requireUndelegated(ctx context.Context, apex string) error {
	lookupContext, cancel := context.WithTimeout(ctx, publicLookupTimeout)
	defer cancel()
	servers, err := s.nsResolver.LookupNS(lookupContext, apex)
	if err != nil {
		return fmt.Errorf("%w: its name servers could not be read", ErrMailZoneStillDelegated)
	}
	for _, server := range servers {
		if strings.HasSuffix(strings.TrimSuffix(strings.ToLower(server.Host), "."), providerNameServerSuffix) {
			return ErrMailZoneStillDelegated
		}
	}
	return nil
}

// EnableMailDomainReceiving makes a send-only domain receive through Zmail and starts its setup,
// which enables Email Routing and routes the mailboxes the domain already has. A domain that
// already receives is returned unchanged. The domain's zone must be active.
func (s *service) EnableMailDomainReceiving(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	mailDomain, err := s.domains.FindDomain(ctx, organizationID, id)
	if err != nil {
		return nil, err
	}
	pending, err := s.pendingZoneOf(ctx, mailDomain)
	if err != nil {
		return nil, err
	}
	if pending != nil {
		return nil, ErrMailZonePending
	}
	if receives(mailDomain) {
		return s.present(ctx, mailDomain)
	}
	if err := s.domains.SetDomainCapability(ctx, organizationID, id, domain.MailDomainSendReceive); err != nil {
		return nil, err
	}
	mailDomain.Capability = domain.MailDomainSendReceive
	return s.startSetUp(ctx, mailDomain)
}
