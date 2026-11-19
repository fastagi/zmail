package mailbox

import (
	"context"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// lockedDomains serializes the domain store a background setup and a request share.
type lockedDomains struct {
	mu *sync.Mutex
	*fakeDomains
}

func (d lockedDomains) FindDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fakeDomains.FindDomain(ctx, organizationID, id)
}

func (d lockedDomains) UpdateDomain(ctx context.Context, mailDomain *domain.MailDomain) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fakeDomains.UpdateDomain(ctx, mailDomain)
}

func (d lockedDomains) SetDomainCapability(ctx context.Context, organizationID, id uint, capability domain.MailDomainCapability) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fakeDomains.SetDomainCapability(ctx, organizationID, id, capability)
}

func (d lockedDomains) ListMailboxesWithoutRoutingRule(ctx context.Context, organizationID uint, name string) ([]*domain.Mailbox, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fakeDomains.ListMailboxesWithoutRoutingRule(ctx, organizationID, name)
}

// lockedProvider serializes the provider calls of a setup.
type lockedProvider struct {
	mu            *sync.Mutex
	beforeSending func()
	*fakeZones
}

func (z lockedProvider) EnableRouting(ctx context.Context, zoneID, name string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.fakeZones.EnableRouting(ctx, zoneID, name)
}

func (z lockedProvider) EnsureSending(ctx context.Context, zoneID, name string) (string, error) {
	// The hook may hold the setup; it runs outside the lock so other calls go on meanwhile.
	if z.beforeSending != nil {
		z.beforeSending()
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.fakeZones.EnsureSending(ctx, zoneID, name)
}

func (z lockedProvider) ListRecords(ctx context.Context, zoneID, name string) ([]mailtransport.DNSRecord, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return slices.Clone(z.fakeZones.records[name]), nil
}

func (z lockedProvider) CreateRecord(ctx context.Context, zoneID string, record mailtransport.DNSRecord) (mailtransport.DNSRecord, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.fakeZones.CreateRecord(ctx, zoneID, record)
}

// A request that makes a send-only domain receive while its setup is finishing must never be lost:
// whatever the interleaving, Email Routing ends up enabled once both calls are done.
func TestReceivingWhileASetupFinishesIsNeverLost(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	sendOnly, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "news.example.com", domain.MailDomainSendOnly)
	if err != nil || sendOnly.Status != domain.MailDomainStatusVerified {
		t.Fatalf("send-only domain = %+v, %v", sendOnly, err)
	}
	var mu sync.Mutex
	fixture.service.domains = lockedDomains{mu: &mu, fakeDomains: fixture.domains}
	fixture.service.domainProvider = lockedProvider{mu: &mu, fakeZones: fixture.zones}
	fixture.service.background = func(run func()) { go run() }

	settingUp := func() bool {
		return fixture.service.markSettingUp(&domain.MailDomain{ID: sendOnly.ID}).SettingUp
	}
	for iteration := range 1000 {
		mu.Lock()
		fixture.domains.domains[sendOnly.ID].Capability = domain.MailDomainSendOnly
		fixture.zones.routingFor = nil
		mu.Unlock()

		if _, err := fixture.service.SetUpMailDomain(ctx, 7, sendOnly.ID); err != nil {
			t.Fatalf("SetUpMailDomain: %v", err)
		}
		// Land the second call anywhere in the running setup, including its very end.
		for spin := rand.IntN(4000); spin > 0; spin-- {
			_ = spin * spin
		}
		if _, err := fixture.service.EnableMailDomainReceiving(ctx, 7, sendOnly.ID); err != nil {
			t.Fatalf("EnableMailDomainReceiving: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for settingUp() {
			if time.Now().After(deadline) {
				t.Fatal("the setup did not finish")
			}
			time.Sleep(10 * time.Microsecond)
		}
		mu.Lock()
		routed := slices.Contains(fixture.zones.routingFor, "news.example.com")
		mu.Unlock()
		if !routed {
			t.Fatalf("iteration %d: receiving was enabled during a setup, but Email Routing never was", iteration)
		}
	}
}

// A request that finds a setup running and asks it to run again must not lose that request to a
// run that ends in between: here the running setup finishes entirely after the request found it
// and before the request is recorded.
func TestASetupThatEndsWhileReceivingIsAskedForRunsAgain(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	sendOnly, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "news.example.com", domain.MailDomainSendOnly)
	if err != nil || sendOnly.Status != domain.MailDomainStatusVerified {
		t.Fatalf("send-only domain = %+v, %v", sendOnly, err)
	}
	var mu sync.Mutex
	fixture.service.domains = lockedDomains{mu: &mu, fakeDomains: fixture.domains}
	provider := lockedProvider{mu: &mu, fakeZones: fixture.zones}
	ended := make(chan struct{}, 4)
	fixture.service.background = func(run func()) {
		go func() {
			run()
			ended <- struct{}{}
		}()
	}
	// The first setup holds inside the provider until the second call has found it running.
	entered, release := make(chan struct{}), make(chan struct{})
	held := false
	provider.beforeSending = func() {
		if !held {
			held = true
			close(entered)
			<-release
		}
	}
	fixture.service.domainProvider = provider
	if _, err := fixture.service.SetUpMailDomain(ctx, 7, sendOnly.ID); err != nil {
		t.Fatalf("SetUpMailDomain: %v", err)
	}
	<-entered
	fixture.service.setupClaimHook = func() {
		close(release)
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Error("the first setup did not end")
		}
	}
	if _, err := fixture.service.EnableMailDomainReceiving(ctx, 7, sendOnly.ID); err != nil {
		t.Fatalf("EnableMailDomainReceiving: %v", err)
	}
	fixture.service.setupClaimHook = nil
	deadline := time.Now().Add(5 * time.Second)
	for fixture.service.markSettingUp(&domain.MailDomain{ID: sendOnly.ID}).SettingUp {
		if time.Now().After(deadline) {
			t.Fatal("the setup did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	stored := fixture.domains.domains[sendOnly.ID]
	if !slices.Contains(fixture.zones.routingFor, "news.example.com") ||
		recordStatus(stored, domain.MailDomainRecordReceiving, "MX", "news.example.com") != domain.MailDomainRecordOK {
		t.Fatalf("routing enabled for %v, stored = %+v; want the setup to run again for receiving",
			fixture.zones.routingFor, stored)
	}
}
