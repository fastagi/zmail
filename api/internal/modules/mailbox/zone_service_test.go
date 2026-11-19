package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

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
	// now stamps CreatedAt on stored zones, as the database does.
	now func() time.Time
	// activations counts the pending-to-active transitions ActivateZone made.
	activations int
}

func newFakeMailZones(domains *fakeDomains) *fakeMailZones {
	return &fakeMailZones{zones: map[uint]*domain.MailZone{}, domains: domains}
}

// store adds a zone directly, as an earlier connect would have.
func (z *fakeMailZones) store(zone domain.MailZone) *domain.MailZone {
	z.nextID++
	zone.ID = z.nextID
	if zone.CreatedAt.IsZero() && z.now != nil {
		zone.CreatedAt = z.now()
	}
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

func (z *fakeMailZones) ActivateZone(_ context.Context, id uint, at time.Time) (bool, error) {
	zone, ok := z.zones[id]
	if !ok || zone.Status != domain.MailZoneStatusPending {
		return false, nil
	}
	zone.Status = domain.MailZoneStatusActive
	zone.ActivatedAt = &at
	zone.LastCheckedAt = &at
	z.activations++
	return true, nil
}

func (z *fakeMailZones) TouchZone(_ context.Context, id uint, at time.Time) error {
	zone, ok := z.zones[id]
	if !ok {
		return ErrMailZoneNotFound
	}
	zone.LastCheckedAt = &at
	return nil
}

func (z *fakeMailZones) DeleteZone(ctx context.Context, organizationID, id uint, removeAtProvider func(context.Context, *domain.MailZone) error) error {
	zone, ok := z.zones[id]
	if !ok || zone.OrganizationID != organizationID {
		return ErrMailZoneNotFound
	}
	for _, mailDomain := range z.domains.domains {
		if mailDomain.MailZoneID != nil && *mailDomain.MailZoneID == id {
			return ErrMailZoneInUse
		}
	}
	// The repository commits only after the provider call succeeds.
	locked := *zone
	if err := removeAtProvider(ctx, &locked); err != nil {
		return err
	}
	delete(z.zones, id)
	return nil
}

func (z *fakeMailZones) DeletePendingZone(ctx context.Context, id uint, removeAtProvider func(context.Context) error) error {
	zone, ok := z.zones[id]
	if !ok || zone.Status != domain.MailZoneStatusPending {
		return ErrMailZoneNotFound
	}
	for _, mailDomain := range z.domains.domains {
		if mailDomain.MailZoneID != nil && *mailDomain.MailZoneID == id && mailDomain.Status == domain.MailDomainStatusVerified {
			return ErrMailZoneInUse
		}
	}
	// The repository commits only after the provider call succeeds.
	if err := removeAtProvider(ctx); err != nil {
		return err
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

func TestSendOnlyMailboxesGetNoRoutingRule(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	fixture.mailZones.store(domain.MailZone{
		OrganizationID: 7, Name: "example.org", CloudflareZoneID: "cf-own", Status: domain.MailZoneStatusActive,
	})
	fixture.zones.zones["example.org"] = "cf-own"
	sendOnly, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "example.org", domain.MailDomainSendOnly)
	if err != nil || sendOnly.Status != domain.MailDomainStatusVerified {
		t.Fatalf("send-only domain = %+v, %v", sendOnly, err)
	}
	// Any call to Email Routing fails the test.
	fixture.zones.ruleErr = errors.New("email routing called for a send-only domain")

	mailbox, err := fixture.service.CreateMailbox(ctx, 7, testCreator, "hello", "", "example.org")
	if err != nil || mailbox.Address != "hello@example.org" || mailbox.RoutingRuleID != "" {
		t.Fatalf("mailbox = %+v, %v", mailbox, err)
	}
	if len(fixture.zones.rules) != 0 {
		t.Fatalf("rules = %v", fixture.zones.rules)
	}
	if deleteErr := fixture.service.DeleteMailbox(ctx, 7, mailbox.ID); deleteErr != nil {
		t.Fatalf("DeleteMailbox: %v", deleteErr)
	}

	// A domain that receives still routes each address to the Email Worker.
	fixture.zones.ruleErr = nil
	fixture.connect(t, "news.example.com")
	routed, err := fixture.service.CreateMailbox(ctx, 7, testCreator, "hello", "", "news.example.com")
	if err != nil || routed.RoutingRuleID == "" || fixture.zones.rules[routed.RoutingRuleID] != "hello@news.example.com" {
		t.Fatalf("routed mailbox = %+v, %v, rules = %v", routed, err, fixture.zones.rules)
	}
}

func TestDiscardKeepsAZoneThatIsNoLongerPending(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	zone := fixture.mailZones.store(domain.MailZone{
		OrganizationID: 7, Name: "example.org", CloudflareZoneID: "cf-own", Status: domain.MailZoneStatusPending,
	})
	// The zone became active before the failed connect cleaned up.
	fixture.mailZones.zones[zone.ID].Status = domain.MailZoneStatusActive
	fixture.service.discardManagedZone(ctx, zone)
	if _, kept := fixture.mailZones.zones[zone.ID]; !kept || len(fixture.zones.deletedZones) != 0 {
		t.Fatalf("active zone discarded: stored %v, deleted %v", fixture.mailZones.zones, fixture.zones.deletedZones)
	}

	// A concurrent connect that joined the pending zone keeps it as well.
	fixture.mailZones.zones[zone.ID].Status = domain.MailZoneStatusPending
	zoneID := zone.ID
	fixture.domains.domains[99] = &domain.MailDomain{ID: 99, OrganizationID: 7, Name: "mail.example.org", MailZoneID: &zoneID}
	fixture.service.discardManagedZone(ctx, zone)
	if _, kept := fixture.mailZones.zones[zone.ID]; !kept || len(fixture.zones.deletedZones) != 0 {
		t.Fatalf("zone in use discarded: stored %v, deleted %v", fixture.mailZones.zones, fixture.zones.deletedZones)
	}
}

// pendingZoneOf returns the stored managed zone of a domain connected into a new zone.
func (f *domainFixture) pendingZoneOf(t *testing.T, mailDomain *domain.MailDomain) *domain.MailZone {
	t.Helper()
	if mailDomain.MailZoneID == nil {
		t.Fatalf("domain %s has no managed zone", mailDomain.Name)
	}
	zone, ok := f.mailZones.zones[*mailDomain.MailZoneID]
	if !ok {
		t.Fatalf("zone of %s is not stored", mailDomain.Name)
	}
	return zone
}

func TestPendingZoneActivatesAndSetsUp(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	mailDomain := fixture.connect(t, "mail.example.org")
	sendOnly, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "news.example.org", domain.MailDomainSendOnly)
	if err != nil {
		t.Fatalf("connect send-only: %v", err)
	}
	zone := fixture.pendingZoneOf(t, mailDomain)

	// Still pending at the provider: the zone is checked, nothing is set up.
	processed, err := fixture.service.CheckPendingZones(ctx, 100)
	if err != nil || processed != 1 {
		t.Fatalf("CheckPendingZones = %d, %v", processed, err)
	}
	zone = fixture.mailZones.zones[zone.ID]
	if zone.Status != domain.MailZoneStatusPending || zone.LastCheckedAt == nil || !zone.LastCheckedAt.Equal(fixture.now) {
		t.Fatalf("zone after a pending check = %+v", zone)
	}
	if len(fixture.zones.routingFor) != 0 || len(fixture.zones.created) != 0 {
		t.Fatalf("setup ran on a pending zone: routing %v, records %v", fixture.zones.routingFor, fixture.zones.created)
	}

	fixture.zones.zoneStatus[zone.CloudflareZoneID] = "active"
	processed, err = fixture.service.CheckPendingZones(ctx, 100)
	if err != nil || processed != 1 {
		t.Fatalf("CheckPendingZones = %d, %v", processed, err)
	}
	zone = fixture.mailZones.zones[zone.ID]
	if zone.Status != domain.MailZoneStatusActive || zone.ActivatedAt == nil || !zone.ActivatedAt.Equal(fixture.now) {
		t.Fatalf("zone = %+v", zone)
	}
	for _, id := range []uint{mailDomain.ID, sendOnly.ID} {
		if stored := fixture.domains.domains[id]; stored.Status != domain.MailDomainStatusVerified {
			t.Fatalf("domain %s = %s, %q", stored.Name, stored.Status, stored.LastError)
		}
	}
	// The send-only domain is set up without Email Routing.
	if !slices.Equal(fixture.zones.routingFor, []string{"mail.example.org"}) {
		t.Fatalf("routing = %v", fixture.zones.routingFor)
	}

	// An active zone is no longer polled.
	if processed, err = fixture.service.CheckPendingZones(ctx, 100); err != nil || processed != 0 {
		t.Fatalf("CheckPendingZones after activation = %d, %v", processed, err)
	}
}

func TestPendingZoneExpiresAfter14Days(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	expired := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.org"))
	fresh := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.net"))
	gone := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.dev"))
	expired.CreatedAt = fixture.now.Add(-15 * 24 * time.Hour)
	fresh.CreatedAt = fixture.now.Add(-13 * 24 * time.Hour)
	gone.CreatedAt = fixture.now.Add(-20 * 24 * time.Hour)
	// The zone of example.dev was already removed at the provider, which no longer knows it.
	fixture.zones.getZoneErr[gone.CloudflareZoneID] = &mailtransport.DomainError{Operation: "get zone", Status: http.StatusNotFound}

	processed, err := fixture.service.CheckPendingZones(ctx, 100)
	if err != nil || processed != 3 {
		t.Fatalf("CheckPendingZones = %d, %v", processed, err)
	}
	if !slices.Contains(fixture.zones.deletedZones, expired.CloudflareZoneID) {
		t.Fatalf("deleted zones = %v", fixture.zones.deletedZones)
	}
	if slices.Contains(fixture.zones.deletedZones, fresh.CloudflareZoneID) {
		t.Fatalf("a 13-day-old zone was deleted: %v", fixture.zones.deletedZones)
	}
	for _, removed := range []*domain.MailZone{expired, gone} {
		if _, left := fixture.mailZones.zones[removed.ID]; left {
			t.Fatalf("expired zone %s is still stored", removed.Name)
		}
	}
	kept, ok := fixture.mailZones.zones[fresh.ID]
	if !ok || kept.Status != domain.MailZoneStatusPending || kept.LastCheckedAt == nil {
		t.Fatalf("fresh zone = %+v", kept)
	}
	if len(fixture.domains.domains) != 1 {
		t.Fatalf("domains left = %v", fixture.domains.domains)
	}
	for _, mailDomain := range fixture.domains.domains {
		if mailDomain.Name != "mail.example.net" {
			t.Fatalf("domain left = %s", mailDomain.Name)
		}
	}
}

func TestPendingZoneExpiryToleratesAZoneAlreadyGone(t *testing.T) {
	fixture := newDomainFixture(t)
	zone := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.org"))
	zone.CreatedAt = fixture.now.Add(-15 * 24 * time.Hour)
	fixture.zones.deleteZoneErr = &mailtransport.DomainError{Operation: "delete zone", Status: http.StatusNotFound}

	if processed, err := fixture.service.CheckPendingZones(context.Background(), 100); err != nil || processed != 1 {
		t.Fatalf("CheckPendingZones = %d, %v", processed, err)
	}
	if _, left := fixture.mailZones.zones[zone.ID]; left || len(fixture.domains.domains) != 0 {
		t.Fatalf("zones %v, domains %v", fixture.mailZones.zones, fixture.domains.domains)
	}

	// Any other provider failure keeps the rows, so the next pass tries again.
	retry := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.net"))
	retry.CreatedAt = fixture.now.Add(-15 * 24 * time.Hour)
	fixture.zones.deleteZoneErr = &mailtransport.DomainError{Operation: "delete zone", Status: http.StatusInternalServerError}
	if _, err := fixture.service.CheckPendingZones(context.Background(), 100); err != nil {
		t.Fatalf("CheckPendingZones: %v", err)
	}
	if _, kept := fixture.mailZones.zones[retry.ID]; !kept {
		t.Fatal("zone removed although the provider still holds it")
	}
}

func TestActiveZoneNeverExpires(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	active := fixture.mailZones.store(domain.MailZone{
		OrganizationID: 7, Name: "example.org", CloudflareZoneID: "cf-own", Status: domain.MailZoneStatusActive,
		CreatedAt: fixture.now.Add(-60 * 24 * time.Hour),
	})
	fixture.zones.zones["example.org"] = "cf-own"
	fixture.connect(t, "mail.example.org")

	if processed, err := fixture.service.CheckPendingZones(ctx, 100); err != nil || processed != 0 {
		t.Fatalf("CheckPendingZones = %d, %v", processed, err)
	}
	if _, kept := fixture.mailZones.zones[active.ID]; !kept || len(fixture.zones.deletedZones) != 0 {
		t.Fatalf("active zone removed: zones %v, deleted %v", fixture.mailZones.zones, fixture.zones.deletedZones)
	}

	// A zone that became active at the provider just before its deadline is activated, not deleted.
	late := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.net"))
	late.CreatedAt = fixture.now.Add(-15 * 24 * time.Hour)
	fixture.zones.zoneStatus[late.CloudflareZoneID] = "active"
	if _, err := fixture.service.CheckPendingZones(ctx, 100); err != nil {
		t.Fatalf("CheckPendingZones: %v", err)
	}
	if stored, kept := fixture.mailZones.zones[late.ID]; !kept || stored.Status != domain.MailZoneStatusActive {
		t.Fatalf("late zone = %+v, kept %v", stored, kept)
	}
	if len(fixture.zones.deletedZones) != 0 {
		t.Fatalf("deleted zones = %v", fixture.zones.deletedZones)
	}
}

func TestCheckPendingZoneToleratesActivationLimit(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	mailDomain := fixture.connect(t, "mail.example.org")
	zone := fixture.pendingZoneOf(t, mailDomain)
	fixture.zones.activationErr = fmt.Errorf("%w: HTTP 429", mailtransport.ErrActivationCheckLimited)

	checked, err := fixture.service.CheckMailDomain(ctx, 7, mailDomain.ID)
	if err != nil {
		t.Fatalf("CheckMailDomain: %v", err)
	}
	if !checked.ZoneCheckLimited || checked.Status != domain.MailDomainStatusPending || len(checked.Records) != 0 {
		t.Fatalf("checked = %+v", checked)
	}
	if checked.Zone == nil || checked.Zone.Status != domain.MailZoneStatusPending || checked.Zone.LastCheckedAt == nil {
		t.Fatalf("zone = %+v", checked.Zone)
	}
	if !slices.Equal(fixture.zones.activationChecks, []string{zone.CloudflareZoneID}) {
		t.Fatalf("activation checks = %v", fixture.zones.activationChecks)
	}
	if len(fixture.zones.routingFor) != 0 {
		t.Fatalf("routing = %v", fixture.zones.routingFor)
	}

	// Once the provider serves the zone, the check activates it and sets the domain up.
	fixture.zones.activationErr = nil
	fixture.zones.zoneStatus[zone.CloudflareZoneID] = "active"
	checked, err = fixture.service.CheckMailDomain(ctx, 7, mailDomain.ID)
	if err != nil {
		t.Fatalf("CheckMailDomain: %v", err)
	}
	if checked.ZoneCheckLimited || checked.Status != domain.MailDomainStatusVerified ||
		checked.Zone == nil || checked.Zone.Status != domain.MailZoneStatusActive {
		t.Fatalf("checked = %+v / zone %+v", checked, checked.Zone)
	}
	if !slices.Equal(fixture.zones.routingFor, []string{"mail.example.org"}) {
		t.Fatalf("routing = %v", fixture.zones.routingFor)
	}

	// Any other refusal of the activation check fails the check.
	pending := fixture.connect(t, "mail.example.net")
	fixture.zones.activationErr = &mailtransport.DomainError{Operation: "activation check", Status: http.StatusInternalServerError}
	if _, err := fixture.service.CheckMailDomain(ctx, 7, pending.ID); !errors.Is(err, ErrMailDomainProviderFailed) {
		t.Fatalf("CheckMailDomain = %v", err)
	}
}

func TestSetUpOnPendingZoneRefused(t *testing.T) {
	fixture := newDomainFixture(t)
	mailDomain := fixture.connect(t, "mail.example.org")

	if _, err := fixture.service.SetUpMailDomain(context.Background(), 7, mailDomain.ID); !errors.Is(err, ErrMailZonePending) {
		t.Fatalf("SetUpMailDomain = %v", err)
	}
	if len(fixture.zones.routingFor) != 0 || len(fixture.zones.created) != 0 {
		t.Fatalf("setup ran: routing %v, records %v", fixture.zones.routingFor, fixture.zones.created)
	}
}

func TestCheckPendingZonesContinuesAfterFailure(t *testing.T) {
	fixture := newDomainFixture(t)
	failing := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.org"))
	activating := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.net"))
	fixture.zones.getZoneErr[failing.CloudflareZoneID] = &mailtransport.DomainError{Operation: "get zone", Status: http.StatusBadGateway}
	fixture.zones.zoneStatus[activating.CloudflareZoneID] = "active"

	processed, err := fixture.service.CheckPendingZones(context.Background(), 100)
	if err != nil || processed != 2 {
		t.Fatalf("CheckPendingZones = %d, %v", processed, err)
	}
	if stored := fixture.mailZones.zones[failing.ID]; stored.Status != domain.MailZoneStatusPending || stored.LastCheckedAt == nil {
		t.Fatalf("failing zone = %+v", stored)
	}
	if stored := fixture.mailZones.zones[activating.ID]; stored.Status != domain.MailZoneStatusActive {
		t.Fatalf("activating zone = %+v", stored)
	}
}

func TestZoneActivationIsMonotonic(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	mailDomain := fixture.connect(t, "mail.example.org")
	zone := fixture.pendingZoneOf(t, mailDomain)
	stale := *zone

	// A user check activates the zone after the poller listed it as pending.
	fixture.zones.zoneStatus[zone.CloudflareZoneID] = "active"
	if _, err := fixture.service.CheckMailDomain(ctx, 7, mailDomain.ID); err != nil {
		t.Fatalf("CheckMailDomain: %v", err)
	}
	if fixture.mailZones.activations != 1 || !slices.Equal(fixture.zones.routingFor, []string{"mail.example.org"}) {
		t.Fatalf("activations %d, routing %v", fixture.mailZones.activations, fixture.zones.routingFor)
	}

	// The poller's pass on its stale copy neither activates nor sets up a second time, even while
	// the domain is still pending (its setup running in the API process, or failed).
	fixture.domains.domains[mailDomain.ID].Status = domain.MailDomainStatusPending
	staleActive := stale
	if err := fixture.service.checkPendingZone(ctx, &staleActive); err != nil {
		t.Fatalf("checkPendingZone: %v", err)
	}
	if fixture.mailZones.activations != 1 || len(fixture.zones.routingFor) != 1 {
		t.Fatalf("second activation: %d, routing %v", fixture.mailZones.activations, fixture.zones.routingFor)
	}

	// Nor does a pass that saw the provider still pending, or failing, move the zone back.
	fixture.zones.zoneStatus[zone.CloudflareZoneID] = "pending"
	stalePending := stale
	if err := fixture.service.checkPendingZone(ctx, &stalePending); err != nil {
		t.Fatalf("checkPendingZone: %v", err)
	}
	fixture.zones.getZoneErr[zone.CloudflareZoneID] = &mailtransport.DomainError{Operation: "get zone", Status: http.StatusBadGateway}
	staleFailing := stale
	if err := fixture.service.checkPendingZone(ctx, &staleFailing); err == nil {
		t.Fatal("checkPendingZone hid the provider failure")
	}
	if stored := fixture.mailZones.zones[zone.ID]; stored.Status != domain.MailZoneStatusActive || stored.ActivatedAt == nil {
		t.Fatalf("zone reverted: %+v", stored)
	}
}

func TestExpiryAfterActivationKeepsTheZone(t *testing.T) {
	fixture := newDomainFixture(t)
	zone := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.org"))
	zone.CreatedAt = fixture.now.Add(-15 * 24 * time.Hour)
	stale := *zone
	// The zone became active after the poller listed it; the poller's provider read was older.
	fixture.mailZones.zones[zone.ID].Status = domain.MailZoneStatusActive

	if err := fixture.service.checkPendingZone(context.Background(), &stale); err != nil {
		t.Fatalf("checkPendingZone: %v", err)
	}
	if len(fixture.zones.deletedZones) != 0 {
		t.Fatalf("active zone deleted at the provider: %v", fixture.zones.deletedZones)
	}
	if stored, kept := fixture.mailZones.zones[zone.ID]; !kept || stored.Status != domain.MailZoneStatusActive || len(fixture.domains.domains) != 1 {
		t.Fatalf("zone %+v kept %v, domains %v", stored, kept, fixture.domains.domains)
	}
}

func TestPendingZoneMissingAtProviderExpiresAtOnce(t *testing.T) {
	fixture := newDomainFixture(t)
	zone := fixture.pendingZoneOf(t, fixture.connect(t, "mail.example.org"))
	fixture.zones.getZoneErr[zone.CloudflareZoneID] = &mailtransport.DomainError{Operation: "get zone", Status: http.StatusNotFound}

	if processed, err := fixture.service.CheckPendingZones(context.Background(), 100); err != nil || processed != 1 {
		t.Fatalf("CheckPendingZones = %d, %v", processed, err)
	}
	if _, left := fixture.mailZones.zones[zone.ID]; left || len(fixture.domains.domains) != 0 {
		t.Fatalf("zones %v, domains %v", fixture.mailZones.zones, fixture.domains.domains)
	}
}
