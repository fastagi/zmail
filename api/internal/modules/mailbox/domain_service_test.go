package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// fakeZones behaves like a Cloudflare account with the zones it lists: enabling routing adds the
// routing records, enabling sending returns records the zone may or may not have yet.
type fakeZones struct {
	zones      map[string]string // zone name -> zone ID
	records    map[string][]mailtransport.DNSRecord
	sending    map[string]string // domain name -> tag
	sendingDNS []mailtransport.DNSRecord
	rules      map[string]string // rule ID -> address
	routingErr error
	ruleErr    error
	// ruleErrFor fails the routing rule of one address only.
	ruleErrFor  map[string]error
	created     []mailtransport.DNSRecord
	routingFor  []string
	nextRuleNum int
	// Zone management: zones created and deleted through the provider, and the scans asked for.
	createZoneErr error
	createdZones  []string // zone names
	deletedZones  []string // zone IDs
	scanned       []string // zone IDs
	// Zone activation: the provider status per zone ID (pending when unset), GetZone failures per
	// zone ID, the activation checks asked for and how they fail, and how DeleteZone fails.
	zoneStatus       map[string]string
	getZoneErr       map[string]error
	activationChecks []string
	activationErr    error
	deleteZoneErr    error
	// createZoneHook, when set, runs once CreateZone added a zone to the account and before it
	// returns, such as to connect a domain from another request meanwhile.
	createZoneHook func()
	// findZoneHook, when set, runs once at the start of FindZone, such as to let another request
	// create a zone between a lookup in Zmail and the lookup at the provider.
	findZoneHook func()
	// ensureSendingHook, when set, runs at the start of every EnsureSending, such as to hold a
	// setup open.
	ensureSendingHook func()
}

func newFakeZones() *fakeZones {
	return &fakeZones{
		zones:   map[string]string{"example.com": "zone-1"},
		records: map[string][]mailtransport.DNSRecord{},
		sending: map[string]string{},
		rules:   map[string]string{},

		zoneStatus: map[string]string{},
		getZoneErr: map[string]error{},
	}
}

func (z *fakeZones) add(record mailtransport.DNSRecord) {
	name := strings.ToLower(record.Name)
	z.records[name] = append(z.records[name], record)
}

func (z *fakeZones) FindZone(_ context.Context, name string) (mailtransport.Zone, error) {
	if hook := z.findZoneHook; hook != nil {
		z.findZoneHook = nil
		hook()
	}
	labels := strings.Split(name, ".")
	for start := 0; start+2 <= len(labels); start++ {
		candidate := strings.Join(labels[start:], ".")
		if id, ok := z.zones[candidate]; ok {
			return mailtransport.Zone{ID: id, Name: candidate}, nil
		}
	}
	return mailtransport.Zone{}, mailtransport.ErrZoneNotFound
}

func (z *fakeZones) EnableRouting(_ context.Context, _, name string) error {
	if z.routingErr != nil {
		return z.routingErr
	}
	z.routingFor = append(z.routingFor, name)
	if len(z.records[name]) == 0 {
		for i, host := range routingMXHosts {
			z.add(mailtransport.DNSRecord{Type: "MX", Name: name, Content: host, Priority: 10 + i})
		}
		z.add(mailtransport.DNSRecord{Type: "TXT", Name: name, Content: `"` + routingSPF + `"`})
	}
	return nil
}

func (z *fakeZones) EnsureSending(_ context.Context, _, name string) (string, error) {
	if z.ensureSendingHook != nil {
		z.ensureSendingHook()
	}
	if tag, ok := z.sending[name]; ok {
		return tag, nil
	}
	z.sending[name] = "tag-" + name
	return z.sending[name], nil
}

func (z *fakeZones) SendingRecords(context.Context, string, string) ([]mailtransport.DNSRecord, error) {
	return z.sendingDNS, nil
}

func (z *fakeZones) ListRecords(_ context.Context, _, name string) ([]mailtransport.DNSRecord, error) {
	return z.records[strings.ToLower(name)], nil
}

func (z *fakeZones) CreateRecord(_ context.Context, _ string, record mailtransport.DNSRecord) (mailtransport.DNSRecord, error) {
	record.ID = fmt.Sprintf("rec-%d", len(z.created)+1)
	z.created = append(z.created, record)
	z.add(record)
	return record, nil
}

func (z *fakeZones) CreateRoutingRule(_ context.Context, _, address, worker string) (string, error) {
	if z.ruleErr != nil {
		return "", z.ruleErr
	}
	if err := z.ruleErrFor[address]; err != nil {
		return "", err
	}
	if worker != "zmail-email-worker" {
		return "", fmt.Errorf("unexpected worker %q", worker)
	}
	z.nextRuleNum++
	id := fmt.Sprintf("rule-%d", z.nextRuleNum)
	z.rules[id] = address
	return id, nil
}

func (z *fakeZones) DeleteRoutingRule(_ context.Context, _, ruleID string) error {
	delete(z.rules, ruleID)
	return nil
}

// fakeNameServers are the name servers the fake assigns to every zone it creates.
var fakeNameServers = []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}

func (z *fakeZones) CreateZone(_ context.Context, name string) (mailtransport.ZoneDetails, error) {
	if z.createZoneErr != nil {
		return mailtransport.ZoneDetails{}, z.createZoneErr
	}
	if _, exists := z.zones[name]; exists {
		return mailtransport.ZoneDetails{}, mailtransport.ErrZoneExists
	}
	z.createdZones = append(z.createdZones, name)
	id := fmt.Sprintf("cf-zone-%d", len(z.createdZones))
	z.zones[name] = id
	if hook := z.createZoneHook; hook != nil {
		z.createZoneHook = nil
		hook()
	}
	return mailtransport.ZoneDetails{
		ID: id, Name: name, Status: "pending",
		NameServers: fakeNameServers, OriginalNameServers: []string{"ns1.registrar.test", "ns2.registrar.test"},
	}, nil
}

func (z *fakeZones) GetZone(_ context.Context, zoneID string) (mailtransport.ZoneDetails, error) {
	if err := z.getZoneErr[zoneID]; err != nil {
		return mailtransport.ZoneDetails{}, err
	}
	for name, id := range z.zones {
		if id == zoneID {
			status := z.zoneStatus[zoneID]
			if status == "" {
				status = "pending"
			}
			return mailtransport.ZoneDetails{ID: id, Name: name, Status: status, NameServers: fakeNameServers}, nil
		}
	}
	return mailtransport.ZoneDetails{}, &mailtransport.DomainError{Operation: "get zone", Status: 404}
}

func (z *fakeZones) RequestActivationCheck(_ context.Context, zoneID string) error {
	z.activationChecks = append(z.activationChecks, zoneID)
	return z.activationErr
}

func (z *fakeZones) DeleteZone(_ context.Context, zoneID string) error {
	z.deletedZones = append(z.deletedZones, zoneID)
	if z.deleteZoneErr != nil {
		return z.deleteZoneErr
	}
	for name, id := range z.zones {
		if id == zoneID {
			delete(z.zones, name)
		}
	}
	return nil
}

func (z *fakeZones) ScanRecords(_ context.Context, zoneID string) error {
	z.scanned = append(z.scanned, zoneID)
	return nil
}

func (z *fakeZones) ListZoneRecords(context.Context, string) ([]mailtransport.DNSRecord, error) {
	var all []mailtransport.DNSRecord
	for _, records := range z.records {
		all = append(all, records...)
	}
	return all, nil
}

func (z *fakeZones) UpdateRecord(context.Context, string, mailtransport.DNSRecord) error { return nil }

func (z *fakeZones) DeleteRecord(context.Context, string, string) error { return nil }

type fakeDomains struct {
	domains map[uint]*domain.MailDomain
	nextID  uint
	mailbox *fakeRepository
}

func (d *fakeDomains) CreateDomain(_ context.Context, mailDomain *domain.MailDomain, limit int) error {
	owned := 0
	for _, existing := range d.domains {
		if existing.Name == mailDomain.Name {
			return ErrMailDomainTaken
		}
		if existing.OrganizationID == mailDomain.OrganizationID {
			owned++
		}
	}
	if owned >= limit {
		return ErrMailDomainLimitReached
	}
	d.nextID++
	mailDomain.ID = d.nextID
	stored := *mailDomain
	d.domains[mailDomain.ID] = &stored
	return nil
}

func (d *fakeDomains) ListDomains(_ context.Context, organizationID uint) ([]*domain.MailDomain, error) {
	var domains []*domain.MailDomain
	for id := uint(1); id <= d.nextID; id++ {
		if existing, ok := d.domains[id]; ok && existing.OrganizationID == organizationID {
			copied := *existing
			domains = append(domains, &copied)
		}
	}
	return domains, nil
}

func (d *fakeDomains) FindDomain(_ context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	existing, ok := d.domains[id]
	if !ok || existing.OrganizationID != organizationID {
		return nil, ErrMailDomainNotFound
	}
	copied := *existing
	return &copied, nil
}

func (d *fakeDomains) FindDomainByName(_ context.Context, name string) (*domain.MailDomain, error) {
	for _, existing := range d.domains {
		if existing.Name == name {
			copied := *existing
			return &copied, nil
		}
	}
	return nil, ErrMailDomainNotFound
}

// UpdateDomain keeps the stored capability and managed zone, as the repository does.
func (d *fakeDomains) UpdateDomain(_ context.Context, mailDomain *domain.MailDomain) error {
	existing, ok := d.domains[mailDomain.ID]
	if !ok {
		return ErrMailDomainNotFound
	}
	stored := *mailDomain
	stored.Capability, stored.MailZoneID = existing.Capability, existing.MailZoneID
	d.domains[mailDomain.ID] = &stored
	return nil
}

func (d *fakeDomains) DeleteDomain(_ context.Context, organizationID, id uint) error {
	existing, ok := d.domains[id]
	if !ok || existing.OrganizationID != organizationID {
		return ErrMailDomainNotFound
	}
	for _, mailbox := range d.mailbox.mailboxes {
		if strings.HasSuffix(mailbox.Address, "@"+existing.Name) {
			return ErrMailDomainInUse
		}
	}
	for _, template := range d.mailbox.templates {
		if strings.HasSuffix(template.FromAddress, "@"+existing.Name) {
			return ErrMailDomainInUse
		}
	}
	delete(d.domains, id)
	return nil
}

// SetDomainCapability changes only the capability, as the repository does.
func (d *fakeDomains) SetDomainCapability(_ context.Context, organizationID, id uint, capability domain.MailDomainCapability) error {
	existing, ok := d.domains[id]
	if !ok || existing.OrganizationID != organizationID {
		return ErrMailDomainNotFound
	}
	existing.Capability = capability
	return nil
}

func (d *fakeDomains) SetMailboxRoutingRule(_ context.Context, mailboxID uint, ruleID string) error {
	if mailbox, ok := d.mailbox.mailboxes[mailboxID]; ok {
		mailbox.RoutingRuleID = ruleID
	}
	return nil
}

func (d *fakeDomains) ListMailboxesWithoutRoutingRule(_ context.Context, organizationID uint, domainName string) ([]*domain.Mailbox, error) {
	var mailboxes []*domain.Mailbox
	for id := uint(1); id <= d.mailbox.nextID; id++ {
		mailbox, ok := d.mailbox.mailboxes[id]
		if ok && mailbox.OrganizationID == organizationID && mailbox.RoutingRuleID == "" &&
			strings.HasSuffix(mailbox.Address, "@"+domainName) {
			copied := *mailbox
			mailboxes = append(mailboxes, &copied)
		}
	}
	return mailboxes, nil
}

func TestPromotedSharedDomainRoutesRepliesAcrossOrganizations(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	connected := fixture.connect(t, "news.example.com")
	fixture.service.settings.Domain = connected.Name
	for _, organizationID := range []uint{7, 8} {
		domains, err := fixture.service.MailboxDomains(ctx, organizationID)
		if err != nil || len(domains) != 1 || !domains[0].Shared || domains[0].Name != connected.Name {
			t.Fatalf("organization %d domains = %+v, error = %v", organizationID, domains, err)
		}
	}
	mailbox, err := fixture.service.CreateMailbox(ctx, as(8), sharedInput("team", "Team", ""))
	if err != nil {
		t.Fatal(err)
	}
	if mailbox.RoutingRuleID == "" || fixture.zones.rules[mailbox.RoutingRuleID] != "team@news.example.com" {
		t.Fatalf("shared subdomain has no reply route: %+v", mailbox)
	}
	if _, err := fixture.service.GetMailDomain(ctx, 8, connected.ID); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("shared sending must not grant domain management: %v", err)
	}
}

func TestPromotedSharedDomainFailsClosedBeforeCreatingMailbox(t *testing.T) {
	for _, pending := range []bool{true, false} {
		t.Run(fmt.Sprintf("pending=%t", pending), func(t *testing.T) {
			fixture := newDomainFixture(t)
			connected := fixture.connect(t, "news.example.com")
			fixture.service.settings.Domain = connected.Name
			want := ErrMailDomainNotVerified
			if pending {
				fixture.domains.domains[connected.ID].Status = domain.MailDomainStatusPending
			} else {
				fixture.zones.ruleErr = &mailtransport.DomainError{Operation: "create rule", Status: 503}
				want = ErrMailDomainProviderFailed
			}
			_, err := fixture.service.CreateMailbox(context.Background(), as(8), sharedInput("team", "", ""))
			if !errors.Is(err, want) || len(fixture.repo.mailboxes) != 0 || len(fixture.zones.rules) != 0 {
				t.Fatalf("error = %v; mailboxes = %d; routes = %d", err, len(fixture.repo.mailboxes), len(fixture.zones.rules))
			}
		})
	}
}

type domainFixture struct {
	*serviceFixture
	zones     *fakeZones
	domains   *fakeDomains
	mailZones *fakeMailZones
}

func newDomainFixture(t *testing.T) *domainFixture {
	t.Helper()
	base := newServiceFixture(t)
	fixture := &domainFixture{
		serviceFixture: base,
		zones:          newFakeZones(),
		domains:        &fakeDomains{domains: map[uint]*domain.MailDomain{}, mailbox: base.repo},
	}
	fixture.mailZones = newFakeMailZones(fixture.domains)
	fixture.mailZones.now = base.service.now
	base.service.domains = fixture.domains
	base.service.zones = fixture.mailZones
	base.service.settings.MaxZonesPerOrganization = 3
	base.service.domainProvider = fixture.zones
	// Setups run in the background in production; here they finish before the call returns.
	base.service.background = func(run func()) { run() }
	base.service.settings.CustomDomainsEnabled = true
	base.service.settings.EmailWorker = "zmail-email-worker"
	fixture.zones.sendingDNS = []mailtransport.DNSRecord{
		{Type: "TXT", Name: "cf-bounce.news.example.com", Content: "v=spf1 include:_spf.mx.cloudflare.net ~all"},
		{Type: "MX", Name: "cf-bounce.news.example.com", Content: "route1.mx.cloudflare.net", Priority: 5},
		{Type: "TXT", Name: "cf1._domainkey.news.example.com", Content: "v=DKIM1; k=rsa; p=MIIB"},
	}
	return fixture
}

// connect connects a domain for organization 7, the organization every domain test acts as.
func (f *domainFixture) connect(t *testing.T, name string) *domain.MailDomain {
	t.Helper()
	mailDomain, err := f.service.ConnectMailDomain(context.Background(), 7, testCreator, name, "")
	if err != nil {
		t.Fatalf("ConnectMailDomain(%q): %v", name, err)
	}
	return mailDomain
}

func recordStatus(mailDomain *domain.MailDomain, purpose domain.MailDomainRecordPurpose, recordType, name string) domain.MailDomainRecordStatus {
	for _, record := range mailDomain.Records {
		if record.Purpose == purpose && record.Type == recordType && record.Name == name {
			return record.Status
		}
	}
	return ""
}

func TestConnectingADomainSetsUpReceivingSendingAndDMARC(t *testing.T) {
	fixture := newDomainFixture(t)
	mailDomain := fixture.connect(t, "  News.Example.com. ")

	if mailDomain.Name != "news.example.com" || mailDomain.ZoneID != "zone-1" || mailDomain.ZoneName != "example.com" {
		t.Fatalf("domain = %+v", mailDomain)
	}
	if mailDomain.Status != domain.MailDomainStatusVerified || mailDomain.VerifiedAt == nil || mailDomain.LastError != "" {
		t.Fatalf("status = %s, verified_at = %v, error = %q", mailDomain.Status, mailDomain.VerifiedAt, mailDomain.LastError)
	}
	if len(fixture.zones.routingFor) != 1 || fixture.zones.routingFor[0] != "news.example.com" {
		t.Fatalf("routing enabled for %v", fixture.zones.routingFor)
	}
	// The three sending records and DMARC were missing and are added; receiving records come from
	// Email Routing and are never written by Zmail.
	if len(fixture.zones.created) != 4 {
		t.Fatalf("created %d records: %+v", len(fixture.zones.created), fixture.zones.created)
	}
	for _, record := range fixture.zones.created {
		if record.Name == "news.example.com" {
			t.Fatalf("receiving record written by Zmail: %+v", record)
		}
	}
	if status := recordStatus(mailDomain, domain.MailDomainRecordDMARC, "TXT", "_dmarc.news.example.com"); status != domain.MailDomainRecordOK {
		t.Fatalf("dmarc = %s", status)
	}
	if len(mailDomain.Records) != 8 {
		t.Fatalf("records = %d: %+v", len(mailDomain.Records), mailDomain.Records)
	}

	// A second setup finds everything in place and writes nothing.
	before := len(fixture.zones.created)
	again, err := fixture.service.SetUpMailDomain(context.Background(), 7, mailDomain.ID)
	if err != nil || again.Status != domain.MailDomainStatusVerified || len(fixture.zones.created) != before {
		t.Fatalf("second setup = %+v, %v, created %d", again, err, len(fixture.zones.created)-before)
	}
}

func TestDomainSetupKeepsExistingRecordsAndReportsConflicts(t *testing.T) {
	fixture := newDomainFixture(t)
	// The domain already sends through another service and has a strict DMARC policy.
	fixture.zones.add(mailtransport.DNSRecord{Type: "MX", Name: "news.example.com", Content: "mx.other-mail.test", Priority: 10})
	fixture.zones.add(mailtransport.DNSRecord{Type: "TXT", Name: "news.example.com", Content: `"v=spf1 include:other-mail.test -all"`})
	fixture.zones.add(mailtransport.DNSRecord{Type: "TXT", Name: "_dmarc.news.example.com", Content: `"v=DMARC1; p=reject"`})

	mailDomain := fixture.connect(t, "news.example.com")

	if mailDomain.Status != domain.MailDomainStatusPending {
		t.Fatalf("status = %s", mailDomain.Status)
	}
	if status := recordStatus(mailDomain, domain.MailDomainRecordReceiving, "TXT", "news.example.com"); status != domain.MailDomainRecordConflict {
		t.Fatalf("spf = %s", status)
	}
	if status := recordStatus(mailDomain, domain.MailDomainRecordReceiving, "MX", "news.example.com"); status != domain.MailDomainRecordConflict {
		t.Fatalf("mx = %s", status)
	}
	for _, record := range mailDomain.Records {
		if record.Purpose == domain.MailDomainRecordDMARC && (record.Status != domain.MailDomainRecordOK || record.Content != `"v=DMARC1; p=reject"`) {
			t.Fatalf("existing DMARC not kept: %+v", record)
		}
	}
	for _, record := range fixture.zones.created {
		if record.Name == "news.example.com" || record.Name == "_dmarc.news.example.com" {
			t.Fatalf("existing record overwritten: %+v", record)
		}
	}
}

func TestDomainSetupFailureKeepsTheDomainPendingWithTheReason(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.zones.routingErr = &mailtransport.DomainError{Operation: "enable email routing", Message: "Authentication error"}

	mailDomain := fixture.connect(t, "news.example.com")
	if mailDomain.Status != domain.MailDomainStatusPending || !strings.Contains(mailDomain.LastError, "Authentication error") {
		t.Fatalf("domain = %s / %q", mailDomain.Status, mailDomain.LastError)
	}

	fixture.zones.routingErr = nil
	fixed, err := fixture.service.SetUpMailDomain(context.Background(), 7, mailDomain.ID)
	if err != nil || fixed.Status != domain.MailDomainStatusVerified || fixed.LastError != "" {
		t.Fatalf("after retry = %+v, %v", fixed, err)
	}
}

func TestCheckingADomainNoticesARemovedRecord(t *testing.T) {
	fixture := newDomainFixture(t)
	mailDomain := fixture.connect(t, "news.example.com")
	fixture.zones.records["_dmarc.news.example.com"] = nil

	checked, err := fixture.service.CheckMailDomain(context.Background(), 7, mailDomain.ID)
	if err != nil || checked.Status != domain.MailDomainStatusPending || checked.VerifiedAt == nil {
		t.Fatalf("checked = %+v, %v", checked, err)
	}
	if len(fixture.zones.records["_dmarc.news.example.com"]) != 0 {
		t.Fatal("a check wrote DNS")
	}
}

func TestConnectingADomainIsRefusedWhenItCannotBelongToTheOrganization(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	for _, name := range []string{"", "localhost", "not a domain", "-bad.example.com", "x.localhost", strings.Repeat("a", 250) + ".com"} {
		if _, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, name, ""); !errors.Is(err, ErrMailDomainInvalid) {
			t.Fatalf("ConnectMailDomain(%q) = %v", name, err)
		}
	}
	if _, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "mail.example", ""); !errors.Is(err, ErrMailDomainTaken) {
		t.Fatalf("shared domain = %v", err)
	}
	fixture.connect(t, "news.example.com")
	if _, err := fixture.service.ConnectMailDomain(ctx, 8, testCreator, "news.example.com", ""); !errors.Is(err, ErrMailDomainTaken) {
		t.Fatalf("domain of another organization = %v", err)
	}
	if _, err := fixture.service.GetMailDomain(ctx, 8, 1); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("read across organizations = %v", err)
	}
	for i := 1; i < domain.MaxMailDomainsPerOrganization; i++ {
		fixture.connect(t, fmt.Sprintf("d%d.example.com", i))
	}
	if _, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "one-more.example.com", ""); !errors.Is(err, ErrMailDomainLimitReached) {
		t.Fatalf("over the limit = %v", err)
	}
}

func TestDomainsAreUnavailableUntilEnabled(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.service.settings.CustomDomainsEnabled = false
	ctx := context.Background()
	if _, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "news.example.com", ""); !errors.Is(err, ErrMailDomainsDisabled) {
		t.Fatalf("connect = %v", err)
	}
	if _, err := fixture.service.ListMailDomains(ctx, 7); !errors.Is(err, ErrMailDomainsDisabled) {
		t.Fatalf("list = %v", err)
	}
	if _, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput("hello", "", "news.example.com")); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("mailbox = %v", err)
	}
	domains, err := fixture.service.MailboxDomains(ctx, 7)
	if err != nil || len(domains) != 1 || !domains[0].Shared {
		t.Fatalf("mailbox domains = %+v, %v", domains, err)
	}
}

func TestMailboxesOnAnOrganizationDomainGetARoutingRule(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	mailDomain := fixture.connect(t, "news.example.com")

	domains, err := fixture.service.MailboxDomains(ctx, 7)
	if err != nil || len(domains) != 2 || domains[1].Name != "news.example.com" || domains[1].Shared {
		t.Fatalf("mailbox domains = %+v, %v", domains, err)
	}
	if others, _ := fixture.service.MailboxDomains(ctx, 8); len(others) != 1 {
		t.Fatalf("another organization sees %+v", others)
	}

	mailbox, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput("Hello", "", "NEWS.example.com"))
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if mailbox.Address != "hello@news.example.com" || mailbox.RoutingRuleID == "" ||
		fixture.zones.rules[mailbox.RoutingRuleID] != "hello@news.example.com" {
		t.Fatalf("mailbox = %+v, rules = %v", mailbox, fixture.zones.rules)
	}
	// Shared-domain mailboxes keep relying on the operator's routing.
	shared, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput("team", "", ""))
	if err != nil || shared.RoutingRuleID != "" || shared.Address != "team@mail.example" {
		t.Fatalf("shared mailbox = %+v, %v", shared, err)
	}

	// A taken address never gets a rule.
	if _, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput("hello", "", "news.example.com")); !errors.Is(err, ErrMailboxAddressTaken) {
		t.Fatalf("taken = %v", err)
	}
	if len(fixture.zones.rules) != 1 {
		t.Fatalf("rules = %v", fixture.zones.rules)
	}
	if _, err := fixture.service.CreateMailbox(ctx, as(8), sharedInput("x", "", "news.example.com")); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("another organization's domain = %v", err)
	}

	if err := fixture.service.DeleteMailDomain(ctx, 7, mailDomain.ID); !errors.Is(err, ErrMailDomainInUse) {
		t.Fatalf("delete domain in use = %v", err)
	}
	if err := fixture.service.DeleteMailbox(ctx, as(7), mailbox.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	if len(fixture.zones.rules) != 0 {
		t.Fatalf("rule kept after delete: %v", fixture.zones.rules)
	}
	if err := fixture.service.DeleteMailDomain(ctx, 7, mailDomain.ID); err != nil {
		t.Fatalf("DeleteMailDomain: %v", err)
	}
}

func TestMailboxesWaitForAVerifiedDomain(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.zones.routingErr = errors.New("unavailable")
	fixture.connect(t, "news.example.com")
	if _, err := fixture.service.CreateMailbox(context.Background(), as(7), sharedInput("hello", "", "news.example.com")); !errors.Is(err, ErrMailDomainNotVerified) {
		t.Fatalf("pending domain = %v", err)
	}
	if len(fixture.zones.rules) != 0 {
		t.Fatalf("rules = %v", fixture.zones.rules)
	}
}

func TestAMailboxThatCannotBeStoredLosesItsRoutingRule(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.connect(t, "news.example.com")
	for i := 0; i < config.DefaultMailboxMaxPerOrganization; i++ {
		fixture.mailbox(t, 7, fmt.Sprintf("box%d", i))
	}
	if _, err := fixture.service.CreateMailbox(context.Background(), as(7), sharedInput("hello", "", "news.example.com")); !errors.Is(err, ErrMailboxLimitReached) {
		t.Fatalf("over the limit = %v", err)
	}
	if len(fixture.zones.rules) != 0 {
		t.Fatalf("rule kept for an address that was not stored: %v", fixture.zones.rules)
	}
}

func TestARoutingRuleFailureCreatesNoMailbox(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.connect(t, "news.example.com")
	fixture.zones.ruleErr = &mailtransport.DomainError{Operation: "create routing rule", Message: "rule limit"}
	if _, err := fixture.service.CreateMailbox(context.Background(), as(7), sharedInput("hello", "", "news.example.com")); !errors.Is(err, ErrMailDomainProviderFailed) {
		t.Fatalf("rule failure = %v", err)
	}
	if _, err := fixture.repo.FindMailboxByAddress(context.Background(), "hello@news.example.com"); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("mailbox stored without its rule: %v", err)
	}
}

func TestRecordComparisonIgnoresHowTheProviderPrintsAValue(t *testing.T) {
	required := domain.MailDomainRecord{Purpose: domain.MailDomainRecordSending, Type: "TXT", Name: "k._domainkey.example.com", Content: "v=DKIM1; k=rsa; p=ABCDEF"}
	split := []mailtransport.DNSRecord{{Type: "TXT", Content: `"v=DKIM1; k=rsa; p=ABC" "DEF"`}}
	if got := compareRecord(required, split); got.Status != domain.MailDomainRecordOK {
		t.Fatalf("chunked TXT = %+v", got)
	}
	stale := []mailtransport.DNSRecord{{Type: "TXT", Content: `"v=DKIM1; k=rsa; p=OLD"`}}
	if got := compareRecord(required, stale); got.Status != domain.MailDomainRecordConflict || got.Found == "" {
		t.Fatalf("different DKIM = %+v", got)
	}
	unrelated := []mailtransport.DNSRecord{{Type: "TXT", Content: `"google-site-verification=abc"`}}
	if got := compareRecord(required, unrelated); got.Status != domain.MailDomainRecordMissing {
		t.Fatalf("unrelated TXT = %+v", got)
	}
	mx := domain.MailDomainRecord{Purpose: domain.MailDomainRecordReceiving, Type: "MX", Content: "route1.mx.cloudflare.net"}
	if got := compareRecord(mx, []mailtransport.DNSRecord{{Type: "MX", Content: "Route1.MX.Cloudflare.net."}}); got.Status != domain.MailDomainRecordOK {
		t.Fatalf("MX with case and dot = %+v", got)
	}
	spf := domain.MailDomainRecord{Purpose: domain.MailDomainRecordReceiving, Type: "TXT", Content: routingSPF}
	merged := []mailtransport.DNSRecord{{Type: "TXT", Content: "v=spf1 include:_spf.google.com include:_spf.mx.cloudflare.net ~all"}}
	if got := compareRecord(spf, merged); got.Status != domain.MailDomainRecordOK {
		t.Fatalf("SPF with an extra include = %+v", got)
	}
}

func TestADMARCRecordAmongTheSendingRecordsIsShownOnce(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.zones.sendingDNS = append(fixture.zones.sendingDNS, mailtransport.DNSRecord{
		Type: "TXT", Name: "_dmarc.news.example.com", Content: `"v=DMARC1; p=reject;"`,
	})
	mailDomain := fixture.connect(t, "news.example.com")

	dmarc := 0
	for _, record := range mailDomain.Records {
		if record.Name == "_dmarc.news.example.com" {
			dmarc++
			if record.Purpose != domain.MailDomainRecordDMARC {
				t.Fatalf("dmarc row purpose = %s", record.Purpose)
			}
		}
	}
	if dmarc != 1 || mailDomain.Status != domain.MailDomainStatusVerified {
		t.Fatalf("dmarc rows = %d, status = %s: %+v", dmarc, mailDomain.Status, mailDomain.Records)
	}
}

func TestADomainSetupRunsInTheBackgroundOncePerDomain(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	var pending []func()
	fixture.service.background = func(run func()) { pending = append(pending, run) }

	connected, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "news.example.com", "")
	if err != nil || !connected.SettingUp || connected.Status != domain.MailDomainStatusPending || len(connected.Records) != 0 {
		t.Fatalf("connected = %+v, %v; want a pending domain whose setup is running", connected, err)
	}
	// A second setup and a check while the first runs change nothing and start nothing.
	again, err := fixture.service.SetUpMailDomain(ctx, 7, connected.ID)
	if err != nil || !again.SettingUp || len(pending) != 1 {
		t.Fatalf("second setup = %+v, %v, runs = %d", again, err, len(pending))
	}
	if checked, checkErr := fixture.service.CheckMailDomain(ctx, 7, connected.ID); checkErr != nil || !checked.SettingUp || checked.CheckedAt != nil {
		t.Fatalf("check during setup = %+v, %v", checked, checkErr)
	}

	pending[0]()
	done, err := fixture.service.GetMailDomain(ctx, 7, connected.ID)
	if err != nil || done.SettingUp || done.Status != domain.MailDomainStatusVerified || len(done.Records) == 0 {
		t.Fatalf("after the setup = %+v, %v", done, err)
	}
	listed, _ := fixture.service.ListMailDomains(ctx, 7)
	if len(listed) != 1 || listed[0].SettingUp {
		t.Fatalf("listed = %+v", listed)
	}
}

func TestRoleAddressesAreReservedOnlyOnTheSharedDomain(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	fixture.connect(t, "news.example.com")

	// The organization operates its own domain, so it may claim its role addresses.
	for _, localPart := range []string{"support", "admin", "postmaster"} {
		mailbox, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput(localPart, "", "news.example.com"))
		if err != nil || mailbox.Address != localPart+"@news.example.com" {
			t.Fatalf("CreateMailbox(%s@news.example.com) = %+v, %v", localPart, mailbox, err)
		}
	}
	// The shared domain belongs to the operator, so its role addresses stay reserved.
	if _, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput("support", "", "")); !errors.Is(err, ErrMailboxAddressReserved) {
		t.Fatalf("shared-domain support = %v", err)
	}
}
