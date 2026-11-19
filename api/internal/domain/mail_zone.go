package domain

import (
	"context"
	"time"
)

// MaxMailZonesPerOrganization is the highest per-organization managed zone limit an operator may
// configure; it also bounds how many zones one organization's list returns.
const MaxMailZonesPerOrganization = 20

// MailZoneStatus tells whether the registrar delegates a managed zone to its assigned name servers.
type MailZoneStatus string

const (
	MailZoneStatusPending MailZoneStatus = "pending"
	MailZoneStatusActive  MailZoneStatus = "active"
)

// MailZone is a DNS zone Zmail created at the DNS provider for an organization's apex domain. It
// becomes active once the registrar points the domain at NameServers.
type MailZone struct {
	ID             uint
	OrganizationID uint
	// Name is the apex domain, unique across the instance.
	Name             string
	CloudflareZoneID string
	Status           MailZoneStatus
	// NameServers are the name servers the provider assigned to the zone.
	NameServers []string
	// OriginalNameServers are the public name servers found when the zone was created, so the
	// organization can move the domain back.
	OriginalNameServers []string
	ActivatedAt         *time.Time
	LastCheckedAt       *time.Time
	CreatedBy           uint
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// MailZoneRepository defines persistence for managed zones.
type MailZoneRepository interface {
	// CreateZone stores a zone unless the organization already has limit zones or the name or
	// provider zone is taken anywhere on the instance.
	CreateZone(ctx context.Context, zone *MailZone, limit int) error
	// FindZone reads a zone of the organization. A reservation is ErrMailZoneNotFound: it has no
	// provider zone yet.
	FindZone(ctx context.Context, organizationID, id uint) (*MailZone, error)
	FindZoneByName(ctx context.Context, name string) (*MailZone, error)
	// ListZones lists the organization's zones, leaving out reservations whose provider zone is
	// still being created.
	ListZones(ctx context.Context, organizationID uint) ([]*MailZone, error)
	// ListPendingZones returns pending zones, least recently checked first.
	ListPendingZones(ctx context.Context, limit int) ([]*MailZone, error)
	UpdateZone(ctx context.Context, zone *MailZone) error
	// CompleteZone stores the provider zone ID and name servers of a reserved zone. A zone that is
	// not a reservation, or no longer exists, is ErrMailZoneNotFound.
	CompleteZone(ctx context.Context, zone *MailZone) error
	// ActivateZone moves a pending zone to active at the given time. It reports false, without an
	// error, when the zone is no longer pending, so exactly one caller wins each activation.
	ActivateZone(ctx context.Context, id uint, at time.Time) (bool, error)
	// TouchZone records when a zone was last checked without changing its status.
	TouchZone(ctx context.Context, id uint, at time.Time) error
	// DeleteZone removes a zone no mail domain belongs to. It holds the zone locked while
	// removeAtProvider runs with the zone as stored, and commits only when that succeeds, so a
	// provider refusal keeps the row and the zone's status cannot change during the call.
	DeleteZone(ctx context.Context, organizationID, id uint, removeAtProvider func(context.Context, *MailZone) error) error
	// DeletePendingZone removes a zone together with its domains, which are pending while the
	// zone is. It holds the zone locked as pending while removeAtProvider runs with the zone as
	// stored, and commits only when that succeeds, so a zone activated concurrently is never
	// removed. A zone that is no longer pending is ErrMailZoneNotFound, and removeAtProvider is not
	// called.
	DeletePendingZone(ctx context.Context, id uint, removeAtProvider func(context.Context, *MailZone) error) error
	ListDomainsInZone(ctx context.Context, zoneID uint) ([]*MailDomain, error)
	// ListUncheckedDomainsInActiveZones returns up to limit pending domains that were never
	// checked although their managed zone is active: domains whose setup an activation started
	// but did not finish.
	// Only domains created before createdBefore are listed, so a domain whose own request is
	// still setting it up is left to that request.
	ListUncheckedDomainsInActiveZones(ctx context.Context, createdBefore time.Time, limit int) ([]*MailDomain, error)
}
