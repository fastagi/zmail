package mailbox

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// fakeMailZones stores managed zones in memory with the limit and uniqueness rules of the
// repository. createErr, when set, fails the next CreateZone the way a concurrent insert does.
type fakeMailZones struct {
	zones     map[uint]*domain.MailZone
	nextID    uint
	domains   *fakeDomains
	createErr error
}

func newFakeMailZones(domains *fakeDomains) *fakeMailZones {
	return &fakeMailZones{zones: map[uint]*domain.MailZone{}, domains: domains}
}

// store adds a zone directly, as an earlier connect would have.
func (z *fakeMailZones) store(zone domain.MailZone) *domain.MailZone {
	z.nextID++
	zone.ID = z.nextID
	z.zones[zone.ID] = &zone
	return &zone
}

func (z *fakeMailZones) CreateZone(_ context.Context, zone *domain.MailZone, limit int) error {
	if z.createErr != nil {
		err := z.createErr
		z.createErr = nil
		return err
	}
	owned := 0
	for _, existing := range z.zones {
		if existing.Name == zone.Name || existing.CloudflareZoneID == zone.CloudflareZoneID {
			return ErrMailDomainTaken
		}
		if existing.OrganizationID == zone.OrganizationID {
			owned++
		}
	}
	if owned >= limit {
		return ErrMailZoneLimitReached
	}
	stored := z.store(*zone)
	*zone = *stored
	return nil
}

func (z *fakeMailZones) FindZone(_ context.Context, organizationID, id uint) (*domain.MailZone, error) {
	zone, ok := z.zones[id]
	if !ok || zone.OrganizationID != organizationID {
		return nil, ErrMailZoneNotFound
	}
	copied := *zone
	return &copied, nil
}

func (z *fakeMailZones) FindZoneByName(_ context.Context, name string) (*domain.MailZone, error) {
	for _, zone := range z.zones {
		if zone.Name == name {
			copied := *zone
			return &copied, nil
		}
	}
	return nil, ErrMailZoneNotFound
}

func (z *fakeMailZones) ListZones(_ context.Context, organizationID uint) ([]*domain.MailZone, error) {
	var zones []*domain.MailZone
	for id := uint(1); id <= z.nextID; id++ {
		if zone, ok := z.zones[id]; ok && zone.OrganizationID == organizationID {
			copied := *zone
			zones = append(zones, &copied)
		}
	}
	return zones, nil
}

func (z *fakeMailZones) ListPendingZones(_ context.Context, limit int) ([]*domain.MailZone, error) {
	var zones []*domain.MailZone
	for id := uint(1); id <= z.nextID && len(zones) < limit; id++ {
		if zone, ok := z.zones[id]; ok && zone.Status == domain.MailZoneStatusPending {
			copied := *zone
			zones = append(zones, &copied)
		}
	}
	return zones, nil
}

func (z *fakeMailZones) UpdateZone(_ context.Context, zone *domain.MailZone) error {
	existing, ok := z.zones[zone.ID]
	if !ok || existing.OrganizationID != zone.OrganizationID {
		return ErrMailZoneNotFound
	}
	stored := *zone
	z.zones[zone.ID] = &stored
	return nil
}

func (z *fakeMailZones) DeleteZone(_ context.Context, organizationID, id uint) error {
	zone, ok := z.zones[id]
	if !ok || zone.OrganizationID != organizationID {
		return ErrMailZoneNotFound
	}
	for _, mailDomain := range z.domains.domains {
		if mailDomain.MailZoneID != nil && *mailDomain.MailZoneID == id {
			return ErrMailZoneInUse
		}
	}
	delete(z.zones, id)
	return nil
}

func (z *fakeMailZones) DeletePendingZone(_ context.Context, id uint) error {
	zone, ok := z.zones[id]
	if !ok || zone.Status != domain.MailZoneStatusPending {
		return ErrMailZoneNotFound
	}
	for domainID, mailDomain := range z.domains.domains {
		if mailDomain.MailZoneID != nil && *mailDomain.MailZoneID == id {
			delete(z.domains.domains, domainID)
		}
	}
	delete(z.zones, id)
	return nil
}

func (z *fakeMailZones) ListDomainsInZone(_ context.Context, zoneID uint) ([]*domain.MailDomain, error) {
	var domains []*domain.MailDomain
	for _, mailDomain := range z.domains.domains {
		if mailDomain.MailZoneID != nil && *mailDomain.MailZoneID == zoneID {
			copied := *mailDomain
			domains = append(domains, &copied)
		}
	}
	return domains, nil
}

var _ domain.MailZoneRepository = (*fakeMailZones)(nil)

func TestApexDomain(t *testing.T) {
	for name, want := range map[string]string{
		"mail.example.com":   "example.com",
		"example.com":        "example.com",
		"a.b.example.com.cn": "example.com.cn",
		"foo.co.uk":          "foo.co.uk",
		"x.github.io":        "x.github.io",
	} {
		if got, err := apexDomain(name); err != nil || got != want {
			t.Errorf("apexDomain(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"com.cn", "co.uk", "com"} {
		if got, err := apexDomain(name); !errors.Is(err, ErrMailDomainInvalid) {
			t.Errorf("apexDomain(%q) = %q, %v; want ErrMailDomainInvalid", name, got, err)
		}
	}
}

func TestConnectCreatesPendingZone(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()

	mailDomain := fixture.connect(t, "mail.example.org")

	if mailDomain.Status != domain.MailDomainStatusPending || mailDomain.SettingUp || len(mailDomain.Records) != 0 {
		t.Fatalf("domain = %+v; want pending without a setup", mailDomain)
	}
	if mailDomain.Capability != domain.MailDomainSendReceive {
		t.Fatalf("capability = %q", mailDomain.Capability)
	}
	if mailDomain.MailZoneID == nil || mailDomain.Zone == nil || *mailDomain.MailZoneID != mailDomain.Zone.ID {
		t.Fatalf("zone = %v / %+v", mailDomain.MailZoneID, mailDomain.Zone)
	}
	zone := mailDomain.Zone
	if zone.Name != "example.org" || zone.Status != domain.MailZoneStatusPending || zone.OrganizationID != 7 ||
		zone.CreatedBy != testCreator || zone.CloudflareZoneID != "cf-zone-1" ||
		!slices.Equal(zone.NameServers, fakeNameServers) || len(zone.OriginalNameServers) != 2 {
		t.Fatalf("zone = %+v", zone)
	}
	if mailDomain.ZoneID != "cf-zone-1" || mailDomain.ZoneName != "example.org" {
		t.Fatalf("provider zone = %s / %s", mailDomain.ZoneID, mailDomain.ZoneName)
	}
	if !slices.Equal(fixture.zones.createdZones, []string{"example.org"}) || !slices.Equal(fixture.zones.scanned, []string{"cf-zone-1"}) {
		t.Fatalf("created %v, scanned %v", fixture.zones.createdZones, fixture.zones.scanned)
	}
	if len(fixture.zones.routingFor) != 0 || len(fixture.zones.created) != 0 {
		t.Fatalf("setup ran on a pending zone: routing %v, records %v", fixture.zones.routingFor, fixture.zones.created)
	}

	// Reads carry the zone too.
	read, err := fixture.service.GetMailDomain(ctx, 7, mailDomain.ID)
	if err != nil || read.Zone == nil || read.Zone.Name != "example.org" {
		t.Fatalf("read = %+v, %v", read, err)
	}
	listed, err := fixture.service.ListMailDomains(ctx, 7)
	if err != nil || len(listed) != 1 || listed[0].Zone == nil || listed[0].Zone.ID != zone.ID {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
}

func TestConnectReusesOwnZone(t *testing.T) {
	fixture := newDomainFixture(t)
	pending := fixture.connect(t, "mail.example.org")

	// A second domain under the same pending apex joins the zone and waits with it.
	second := fixture.connect(t, "news.example.org")
	if second.MailZoneID == nil || *second.MailZoneID != *pending.MailZoneID || second.ZoneID != pending.ZoneID {
		t.Fatalf("second = %+v", second)
	}
	if len(fixture.zones.createdZones) != 1 || len(fixture.zones.scanned) != 1 || len(fixture.zones.routingFor) != 0 {
		t.Fatalf("created %v, scanned %v, routing %v", fixture.zones.createdZones, fixture.zones.scanned, fixture.zones.routingFor)
	}

	// Once the zone is active, a new domain in it is set up at once.
	zone := fixture.mailZones.zones[*pending.MailZoneID]
	zone.Status = domain.MailZoneStatusActive
	active := fixture.connect(t, "example.org")
	if active.Status != domain.MailDomainStatusVerified || active.Zone == nil || active.Zone.Status != domain.MailZoneStatusActive {
		t.Fatalf("active = %+v", active)
	}
	if !slices.Equal(fixture.zones.routingFor, []string{"example.org"}) || len(fixture.zones.createdZones) != 1 {
		t.Fatalf("routing %v, created zones %v", fixture.zones.routingFor, fixture.zones.createdZones)
	}
}

func TestConnectOtherOrgZoneTaken(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.mailZones.store(domain.MailZone{
		OrganizationID: 8, Name: "example.org", CloudflareZoneID: "cf-other", Status: domain.MailZoneStatusActive,
	})
	fixture.zones.zones["example.org"] = "cf-other"

	if _, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "mail.example.org", ""); !errors.Is(err, ErrMailDomainTaken) {
		t.Fatalf("connect = %v", err)
	}
	if len(fixture.domains.domains) != 0 || len(fixture.zones.createdZones) != 0 {
		t.Fatalf("domains %v, created zones %v", fixture.domains.domains, fixture.zones.createdZones)
	}
}

func TestConnectOperatorZoneUnchanged(t *testing.T) {
	fixture := newDomainFixture(t)
	mailDomain := fixture.connect(t, "news.example.com")

	if mailDomain.MailZoneID != nil || mailDomain.Zone != nil || mailDomain.ZoneID != "zone-1" {
		t.Fatalf("domain = %+v", mailDomain)
	}
	if len(fixture.zones.createdZones) != 0 || len(fixture.mailZones.zones) != 0 {
		t.Fatalf("created zones %v, stored %v", fixture.zones.createdZones, fixture.mailZones.zones)
	}
	if mailDomain.Status != domain.MailDomainStatusVerified || !slices.Equal(fixture.zones.routingFor, []string{"news.example.com"}) {
		t.Fatalf("status = %s, routing %v", mailDomain.Status, fixture.zones.routingFor)
	}
}

func TestConnectCreatesZoneOnceUnderRace(t *testing.T) {
	fixture := newDomainFixture(t)
	// Another request stores the same apex between this request's lookup and its insert.
	fixture.mailZones.createErr = ErrMailDomainTaken

	if _, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "mail.example.org", ""); !errors.Is(err, ErrMailDomainTaken) {
		t.Fatalf("connect = %v", err)
	}
	if !slices.Equal(fixture.zones.deletedZones, []string{"cf-zone-1"}) {
		t.Fatalf("deleted zones = %v", fixture.zones.deletedZones)
	}
	if len(fixture.domains.domains) != 0 {
		t.Fatalf("domains = %v", fixture.domains.domains)
	}
}

func TestConnectZoneExistsAtProviderIsTaken(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.zones.createZoneErr = mailtransport.ErrZoneExists
	if _, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "mail.example.org", ""); !errors.Is(err, ErrMailDomainTaken) {
		t.Fatalf("connect = %v", err)
	}
	if len(fixture.mailZones.zones) != 0 || len(fixture.domains.domains) != 0 {
		t.Fatalf("stored zones %v, domains %v", fixture.mailZones.zones, fixture.domains.domains)
	}
}

func TestConnectZoneLimit(t *testing.T) {
	fixture := newDomainFixture(t)
	for _, name := range []string{"one.test", "two.test", "three.test"} {
		fixture.connect(t, "mail."+name)
	}
	if _, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "mail.four.test", ""); !errors.Is(err, ErrMailZoneLimitReached) {
		t.Fatalf("fourth zone = %v", err)
	}
	if _, left := fixture.zones.zones["four.test"]; left || len(fixture.zones.createdZones) != 3 {
		t.Fatalf("provider zones = %v, created %v", fixture.zones.zones, fixture.zones.createdZones)
	}
	if len(fixture.mailZones.zones) != 3 {
		t.Fatalf("stored zones = %d", len(fixture.mailZones.zones))
	}
	// Domains in zones the organization already has are not limited by the zone count.
	fixture.connect(t, "news.one.test")
	fixture.connect(t, "news.example.com")
}

func TestConnectRemovesANewZoneWhenTheDomainCannotBeStored(t *testing.T) {
	fixture := newDomainFixture(t)
	for i := 0; i < domain.MaxMailDomainsPerOrganization; i++ {
		fixture.connect(t, "d"+string(rune('a'+i))+".example.com")
	}
	if _, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "mail.example.org", ""); !errors.Is(err, ErrMailDomainLimitReached) {
		t.Fatalf("over the domain limit = %v", err)
	}
	if len(fixture.mailZones.zones) != 0 || !slices.Equal(fixture.zones.deletedZones, []string{"cf-zone-1"}) {
		t.Fatalf("stored zones %v, deleted %v", fixture.mailZones.zones, fixture.zones.deletedZones)
	}
}

func TestConnectRejectsAnUnknownCapability(t *testing.T) {
	fixture := newDomainFixture(t)
	if _, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "news.example.com", "receive_only"); !errors.Is(err, ErrMailDomainInvalid) {
		t.Fatalf("connect = %v", err)
	}
	if len(fixture.domains.domains) != 0 {
		t.Fatalf("domains = %v", fixture.domains.domains)
	}
}

func TestSendOnlySkipsRouting(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.mailZones.store(domain.MailZone{
		OrganizationID: 7, Name: "example.org", CloudflareZoneID: "cf-own", Status: domain.MailZoneStatusActive,
	})
	fixture.zones.zones["example.org"] = "cf-own"
	// The domain keeps receiving through another mail service.
	fixture.zones.add(mailtransport.DNSRecord{Type: "MX", Name: "example.org", Content: "mxbiz1.qq.com", Priority: 5})

	mailDomain, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "example.org", domain.MailDomainSendOnly)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if mailDomain.Capability != domain.MailDomainSendOnly || mailDomain.ZoneID != "cf-own" {
		t.Fatalf("domain = %+v", mailDomain)
	}
	if len(fixture.zones.routingFor) != 0 {
		t.Fatalf("routing enabled for %v", fixture.zones.routingFor)
	}
	for _, record := range mailDomain.Records {
		if record.Purpose == domain.MailDomainRecordReceiving {
			t.Fatalf("receiving record on a send-only domain: %+v", record)
		}
	}
	if len(mailDomain.Records) == 0 || mailDomain.Status != domain.MailDomainStatusVerified {
		t.Fatalf("status = %s, records = %+v", mailDomain.Status, mailDomain.Records)
	}
}
