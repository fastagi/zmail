package mailbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func openZoneRepository(t *testing.T) *repository {
	t.Helper()
	repo, db := openRepository(t)
	if err := db.AutoMigrate(&MailZonePO{}, &MailDomainPO{}); err != nil {
		t.Fatalf("migrate mail zones: %v", err)
	}
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	return repo
}

func newZone(organizationID uint, name string) *domain.MailZone {
	return &domain.MailZone{
		OrganizationID:   organizationID,
		Name:             name,
		CloudflareZoneID: "cf-" + name,
		NameServers:      []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"},
		CreatedBy:        organizationID,
	}
}

func TestZoneRepositoryLimit(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	for i := range 3 {
		zone := newZone(1, fmt.Sprintf("example%d.com", i))
		if err := repo.CreateZone(ctx, zone, 3); err != nil {
			t.Fatalf("CreateZone %d: %v", i, err)
		}
		if zone.ID == 0 || zone.Status != domain.MailZoneStatusPending || len(zone.NameServers) != 2 || zone.OriginalNameServers == nil {
			t.Fatalf("created = %+v", zone)
		}
	}
	if err := repo.CreateZone(ctx, newZone(1, "example3.com"), 3); !errors.Is(err, ErrMailZoneLimitReached) {
		t.Fatalf("fourth zone = %v", err)
	}
	listed, err := repo.ListZones(ctx, 1)
	if err != nil || len(listed) != 3 || listed[0].Name != "example0.com" {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
}

func TestZoneRepositoryNameUnique(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	first := newZone(1, "example.com")
	if err := repo.CreateZone(ctx, first, 3); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	if err := repo.CreateZone(ctx, newZone(2, "example.com"), 3); !errors.Is(err, ErrMailDomainTaken) {
		t.Fatalf("same name in another organization = %v", err)
	}
	sameCloudflareZone := newZone(2, "other.com")
	sameCloudflareZone.CloudflareZoneID = first.CloudflareZoneID
	if err := repo.CreateZone(ctx, sameCloudflareZone, 3); !errors.Is(err, ErrMailDomainTaken) {
		t.Fatalf("same cloudflare zone = %v", err)
	}
	if _, err := repo.FindZone(ctx, 2, first.ID); !errors.Is(err, ErrMailZoneNotFound) {
		t.Fatalf("read across organizations = %v", err)
	}
	found, err := repo.FindZoneByName(ctx, "EXAMPLE.com")
	if err != nil || found.ID != first.ID {
		t.Fatalf("found = %+v, %v", found, err)
	}
	if _, err := repo.FindZoneByName(ctx, "missing.com"); !errors.Is(err, ErrMailZoneNotFound) {
		t.Fatalf("missing zone = %v", err)
	}
}

func TestZoneRepositoryConcurrentCreate(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			zone := newZone(uint(i+1), "example.com")
			zone.CloudflareZoneID = fmt.Sprintf("cf-%d", i)
			results[i] = repo.CreateZone(ctx, zone, 3)
		}(i)
	}
	wg.Wait()
	succeeded := 0
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, ErrMailDomainTaken):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded = %d, results = %v", succeeded, results)
	}
}

func TestZoneRepositoryUpdatesAndListsPendingZones(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	checked := newZone(1, "checked.com")
	unchecked := newZone(1, "unchecked.com")
	active := newZone(2, "active.com")
	for _, zone := range []*domain.MailZone{checked, unchecked, active} {
		if err := repo.CreateZone(ctx, zone, 3); err != nil {
			t.Fatalf("CreateZone(%s): %v", zone.Name, err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	checked.LastCheckedAt = &now
	checked.OriginalNameServers = []string{"ns1.registrar.example"}
	if err := repo.UpdateZone(ctx, checked); err != nil {
		t.Fatalf("UpdateZone: %v", err)
	}
	active.Status = domain.MailZoneStatusActive
	active.ActivatedAt = &now
	if err := repo.UpdateZone(ctx, active); err != nil {
		t.Fatalf("UpdateZone active: %v", err)
	}
	pending, err := repo.ListPendingZones(ctx, 10)
	if err != nil || len(pending) != 2 || pending[0].Name != "unchecked.com" || pending[1].Name != "checked.com" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if len(pending[1].OriginalNameServers) != 1 || pending[1].LastCheckedAt == nil {
		t.Fatalf("updated zone = %+v", pending[1])
	}
	stored, err := repo.FindZone(ctx, 2, active.ID)
	if err != nil || stored.Status != domain.MailZoneStatusActive || stored.ActivatedAt == nil {
		t.Fatalf("active = %+v, %v", stored, err)
	}
	active.OrganizationID = 1
	if err := repo.UpdateZone(ctx, active); !errors.Is(err, ErrMailZoneNotFound) {
		t.Fatalf("update across organizations = %v", err)
	}
}

func TestZoneRepositoryDeleteInUse(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	zone := newZone(1, "example.com")
	if err := repo.CreateZone(ctx, zone, 3); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	mailDomain := &domain.MailDomain{OrganizationID: 1, Name: "example.com", ZoneID: zone.CloudflareZoneID, ZoneName: zone.Name, MailZoneID: &zone.ID, CreatedBy: 1}
	if err := repo.CreateDomain(ctx, mailDomain, 10); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	if err := repo.DeleteZone(ctx, 1, zone.ID, failAtProvider(t)); !errors.Is(err, ErrMailZoneInUse) {
		t.Fatalf("delete in use = %v", err)
	}
	if err := repo.DeleteDomain(ctx, 1, mailDomain.ID); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}
	if err := repo.DeleteZone(ctx, 2, zone.ID, failAtProvider(t)); !errors.Is(err, ErrMailZoneNotFound) {
		t.Fatalf("delete across organizations = %v", err)
	}
	// A provider refusal keeps the row, so the removal can be tried again.
	refused := errors.New("provider refused")
	if err := repo.DeleteZone(ctx, 1, zone.ID, func(context.Context, *domain.MailZone) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("DeleteZone with a provider refusal = %v", err)
	}
	if _, err := repo.FindZone(ctx, 1, zone.ID); err != nil {
		t.Fatalf("zone removed although the provider refused: %v", err)
	}
	var seen *domain.MailZone
	if err := repo.DeleteZone(ctx, 1, zone.ID, func(_ context.Context, locked *domain.MailZone) error {
		seen = locked
		return nil
	}); err != nil {
		t.Fatalf("DeleteZone: %v", err)
	}
	if seen == nil || seen.ID != zone.ID || seen.Status != domain.MailZoneStatusPending || seen.CloudflareZoneID != zone.CloudflareZoneID {
		t.Fatalf("provider removal saw %+v", seen)
	}
	if _, err := repo.FindZone(ctx, 1, zone.ID); !errors.Is(err, ErrMailZoneNotFound) {
		t.Fatalf("deleted zone = %v", err)
	}
}

func TestDeletePendingZoneRemovesDomains(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	zone := newZone(1, "example.com")
	if err := repo.CreateZone(ctx, zone, 3); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	for _, name := range []string{"example.com", "news.example.com"} {
		mailDomain := &domain.MailDomain{OrganizationID: 1, Name: name, ZoneID: zone.CloudflareZoneID, ZoneName: zone.Name, MailZoneID: &zone.ID, CreatedBy: 1}
		if err := repo.CreateDomain(ctx, mailDomain, 10); err != nil {
			t.Fatalf("CreateDomain(%s): %v", name, err)
		}
	}
	inZone, err := repo.ListDomainsInZone(ctx, zone.ID)
	if err != nil || len(inZone) != 2 {
		t.Fatalf("in zone = %+v, %v", inZone, err)
	}
	if err = repo.DeletePendingZone(ctx, zone.ID, removedAtProvider); err != nil {
		t.Fatalf("DeletePendingZone: %v", err)
	}
	if _, err = repo.FindZone(ctx, 1, zone.ID); !errors.Is(err, ErrMailZoneNotFound) {
		t.Fatalf("deleted zone = %v", err)
	}
	left, err := repo.ListDomains(ctx, 1)
	if err != nil || len(left) != 0 {
		t.Fatalf("domains left = %+v, %v", left, err)
	}
}

func TestDomainCapabilityRoundTrip(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	zone := newZone(1, "example.com")
	if err := repo.CreateZone(ctx, zone, 3); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	sendOnly := &domain.MailDomain{OrganizationID: 1, Name: "example.com", ZoneID: zone.CloudflareZoneID, ZoneName: zone.Name, MailZoneID: &zone.ID, Capability: domain.MailDomainSendOnly, CreatedBy: 1}
	if err := repo.CreateDomain(ctx, sendOnly, 10); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	found, err := repo.FindDomain(ctx, 1, sendOnly.ID)
	if err != nil || found.Capability != domain.MailDomainSendOnly || found.MailZoneID == nil || *found.MailZoneID != zone.ID {
		t.Fatalf("send only = %+v, %v", found, err)
	}
	// The receiving upgrade sets the capability on its own; a setup or check that read the domain
	// before the upgrade and stores its result afterwards keeps the new capability and zone.
	stale := *found
	if err = repo.SetDomainCapability(ctx, 1, sendOnly.ID, domain.MailDomainSendReceive); err != nil {
		t.Fatalf("SetDomainCapability: %v", err)
	}
	stale.Status = domain.MailDomainStatusVerified
	stale.MailZoneID = nil
	if err = repo.UpdateDomain(ctx, &stale); err != nil {
		t.Fatalf("UpdateDomain: %v", err)
	}
	updated, err := repo.FindDomain(ctx, 1, sendOnly.ID)
	if err != nil || updated.Capability != domain.MailDomainSendReceive || updated.Status != domain.MailDomainStatusVerified ||
		updated.MailZoneID == nil || *updated.MailZoneID != zone.ID {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	if err = repo.SetDomainCapability(ctx, 2, sendOnly.ID, domain.MailDomainSendOnly); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("set capability across organizations = %v", err)
	}

	plain := &domain.MailDomain{OrganizationID: 1, Name: "operator.example", ZoneID: "z", ZoneName: "operator.example", CreatedBy: 1}
	if err = repo.CreateDomain(ctx, plain, 10); err != nil {
		t.Fatalf("CreateDomain plain: %v", err)
	}
	stored, err := repo.FindDomainByName(ctx, "operator.example")
	if err != nil || stored.Capability != domain.MailDomainSendReceive || stored.MailZoneID != nil {
		t.Fatalf("plain = %+v, %v", stored, err)
	}
}

func TestDeletePendingZoneRefusesAVerifiedDomain(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	zone := newZone(1, "example.com")
	if err := repo.CreateZone(ctx, zone, 3); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	mailDomain := &domain.MailDomain{OrganizationID: 1, Name: "example.com", ZoneID: zone.CloudflareZoneID, ZoneName: zone.Name, MailZoneID: &zone.ID, CreatedBy: 1}
	if err := repo.CreateDomain(ctx, mailDomain, 10); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	mailDomain.Status = domain.MailDomainStatusVerified
	if err := repo.UpdateDomain(ctx, mailDomain); err != nil {
		t.Fatalf("UpdateDomain: %v", err)
	}
	if err := repo.DeletePendingZone(ctx, zone.ID, removedAtProvider); !errors.Is(err, ErrMailZoneInUse) {
		t.Fatalf("DeletePendingZone = %v", err)
	}
	if _, err := repo.FindZone(ctx, 1, zone.ID); err != nil {
		t.Fatalf("zone removed: %v", err)
	}
	if _, err := repo.FindDomain(ctx, 1, mailDomain.ID); err != nil {
		t.Fatalf("domain removed: %v", err)
	}
}

func removedAtProvider(context.Context) error { return nil }

// failAtProvider fails the test when a zone removal reaches the provider.
func failAtProvider(t *testing.T) func(context.Context, *domain.MailZone) error {
	return func(context.Context, *domain.MailZone) error {
		t.Error("the provider removal ran")
		return nil
	}
}

func TestActivateZoneHasOneWinner(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	zone := newZone(1, "example.com")
	if err := repo.CreateZone(ctx, zone, 3); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	at := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	if err := repo.TouchZone(ctx, zone.ID, at); err != nil {
		t.Fatalf("TouchZone: %v", err)
	}
	touched, err := repo.FindZone(ctx, 1, zone.ID)
	if err != nil || touched.Status != domain.MailZoneStatusPending || touched.LastCheckedAt == nil || !touched.LastCheckedAt.Equal(at) {
		t.Fatalf("touched = %+v, %v", touched, err)
	}
	won, err := repo.ActivateZone(ctx, zone.ID, at.Add(time.Minute))
	if err != nil || !won {
		t.Fatalf("first ActivateZone = %v, %v", won, err)
	}
	won, err = repo.ActivateZone(ctx, zone.ID, at.Add(2*time.Minute))
	if err != nil || won {
		t.Fatalf("second ActivateZone = %v, %v", won, err)
	}
	// A later check that saw the zone pending only records the time.
	if err = repo.TouchZone(ctx, zone.ID, at.Add(3*time.Minute)); err != nil {
		t.Fatalf("TouchZone: %v", err)
	}
	active, err := repo.FindZone(ctx, 1, zone.ID)
	if err != nil || active.Status != domain.MailZoneStatusActive || active.ActivatedAt == nil ||
		!active.ActivatedAt.Equal(at.Add(time.Minute)) || !active.LastCheckedAt.Equal(at.Add(3*time.Minute)) {
		t.Fatalf("active = %+v, %v", active, err)
	}
	if err = repo.TouchZone(ctx, 999, at); !errors.Is(err, ErrMailZoneNotFound) {
		t.Fatalf("TouchZone missing = %v", err)
	}
}

func TestDeletePendingZoneCommitsOnlyAfterTheProvider(t *testing.T) {
	repo := openZoneRepository(t)
	ctx := context.Background()
	zone := newZone(1, "example.com")
	if err := repo.CreateZone(ctx, zone, 3); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	mailDomain := &domain.MailDomain{OrganizationID: 1, Name: "mail.example.com", ZoneID: zone.CloudflareZoneID, ZoneName: zone.Name, MailZoneID: &zone.ID, CreatedBy: 1}
	if err := repo.CreateDomain(ctx, mailDomain, 10); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	providerDown := errors.New("provider down")
	if err := repo.DeletePendingZone(ctx, zone.ID, func(context.Context) error { return providerDown }); !errors.Is(err, providerDown) {
		t.Fatalf("DeletePendingZone = %v", err)
	}
	if _, err := repo.FindZone(ctx, 1, zone.ID); err != nil {
		t.Fatalf("zone removed although the provider failed: %v", err)
	}
	if _, err := repo.FindDomain(ctx, 1, mailDomain.ID); err != nil {
		t.Fatalf("domain removed although the provider failed: %v", err)
	}

	// An active zone is never handed to the provider for removal.
	if won, err := repo.ActivateZone(ctx, zone.ID, time.Now()); err != nil || !won {
		t.Fatalf("ActivateZone = %v, %v", won, err)
	}
	called := false
	err := repo.DeletePendingZone(ctx, zone.ID, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, ErrMailZoneNotFound) || called {
		t.Fatalf("DeletePendingZone on an active zone = %v, provider called %v", err, called)
	}
}
