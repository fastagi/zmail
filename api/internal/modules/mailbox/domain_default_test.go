package mailbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// defaultsOf lists the names of the listed domains that read as the default.
func defaultsOf(domains []*domain.MailDomain) []string {
	var names []string
	for _, mailDomain := range domains {
		if mailDomain.Default {
			names = append(names, mailDomain.Name)
		}
	}
	return names
}

// verifiedAt connects a domain for organization 7 and marks it verified, connected at the given
// offset from the fixture clock, so tests decide which domain is the oldest.
func (f *domainFixture) verifiedAt(t *testing.T, name string, age time.Duration) *domain.MailDomain {
	t.Helper()
	mailDomain := f.connect(t, name)
	stored := f.domains.domains[mailDomain.ID]
	stored.Status = domain.MailDomainStatusVerified
	stored.CreatedAt = f.now.Add(-age)
	return stored
}

func TestOldestVerifiedDomainIsTheDefaultUntilOneIsChosen(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	fixture.verifiedAt(t, "a.example.com", time.Hour)
	older := fixture.verifiedAt(t, "b.example.com", 2*time.Hour)
	pending := fixture.verifiedAt(t, "c.example.com", 3*time.Hour)
	fixture.domains.domains[pending.ID].Status = domain.MailDomainStatusPending

	listed, err := fixture.service.ListMailDomains(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultsOf(listed); len(got) != 1 || got[0] != older.Name {
		t.Fatalf("defaults = %v, want only the oldest verified domain %s", got, older.Name)
	}
}

func TestChoosingTheDefaultDomainMovesItAndRemovingItFallsBack(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	oldest := fixture.verifiedAt(t, "a.example.com", 3*time.Hour)
	middle := fixture.verifiedAt(t, "b.example.com", 2*time.Hour)
	newest := fixture.verifiedAt(t, "c.example.com", time.Hour)

	chosen, err := fixture.service.SetDefaultMailDomain(ctx, 7, newest.ID)
	if err != nil || !chosen.Default || chosen.ID != newest.ID {
		t.Fatalf("SetDefaultMailDomain = %+v, %v", chosen, err)
	}
	if _, err := fixture.service.SetDefaultMailDomain(ctx, 7, middle.ID); err != nil {
		t.Fatal(err)
	}
	listed, _ := fixture.service.ListMailDomains(ctx, 7)
	if got := defaultsOf(listed); len(got) != 1 || got[0] != middle.Name {
		t.Fatalf("defaults after choosing %s = %v", middle.Name, got)
	}
	// Reading one domain tells whether it is the default too.
	if read, err := fixture.service.GetMailDomain(ctx, 7, oldest.ID); err != nil || read.Default {
		t.Fatalf("GetMailDomain(oldest) = %+v, %v", read, err)
	}

	if err := fixture.service.DeleteMailDomain(ctx, 7, middle.ID); err != nil {
		t.Fatal(err)
	}
	listed, _ = fixture.service.ListMailDomains(ctx, 7)
	if got := defaultsOf(listed); len(got) != 1 || got[0] != oldest.Name {
		t.Fatalf("defaults after removing the default = %v, want %s", got, oldest.Name)
	}
}

// A chosen default that stops verifying gives way to the oldest verified domain, and comes back
// once it verifies again.
func TestAChosenDefaultThatStopsVerifyingGivesWay(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	oldest := fixture.verifiedAt(t, "a.example.com", 2*time.Hour)
	chosen := fixture.verifiedAt(t, "b.example.com", time.Hour)
	if _, err := fixture.service.SetDefaultMailDomain(ctx, 7, chosen.ID); err != nil {
		t.Fatal(err)
	}
	fixture.domains.domains[chosen.ID].Status = domain.MailDomainStatusPending
	listed, _ := fixture.service.ListMailDomains(ctx, 7)
	if got := defaultsOf(listed); len(got) != 1 || got[0] != oldest.Name {
		t.Fatalf("defaults = %v, want %s", got, oldest.Name)
	}
	fixture.domains.domains[chosen.ID].Status = domain.MailDomainStatusVerified
	listed, _ = fixture.service.ListMailDomains(ctx, 7)
	if got := defaultsOf(listed); len(got) != 1 || got[0] != chosen.Name {
		t.Fatalf("defaults = %v, want %s", got, chosen.Name)
	}
}

func TestOnlyAVerifiedDomainOfTheOrganizationCanBeTheDefault(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	pending := fixture.verifiedAt(t, "a.example.com", time.Hour)
	fixture.domains.domains[pending.ID].Status = domain.MailDomainStatusPending

	if _, err := fixture.service.SetDefaultMailDomain(ctx, 7, pending.ID); !errors.Is(err, ErrMailDomainNotVerified) {
		t.Fatalf("pending domain: %v", err)
	}
	if _, err := fixture.service.SetDefaultMailDomain(ctx, 8, pending.ID); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("another organization's domain: %v", err)
	}
	if _, err := fixture.service.SetDefaultMailDomain(ctx, 7, 999); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("unknown domain: %v", err)
	}
	if fixture.domains.domains[pending.ID].MarkedDefault {
		t.Fatal("a refused choice must not be stored")
	}
}

func TestMailboxDomainsMarkTheDefaultForNewMailboxes(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()

	domains, err := fixture.service.MailboxDomains(ctx, 7)
	if err != nil || len(domains) != 1 || !domains[0].Default {
		t.Fatalf("without an own domain the shared one is the default: %+v, %v", domains, err)
	}

	fixture.verifiedAt(t, "a.example.com", 2*time.Hour)
	chosen := fixture.verifiedAt(t, "b.example.com", time.Hour)
	if _, err = fixture.service.SetDefaultMailDomain(ctx, 7, chosen.ID); err != nil {
		t.Fatal(err)
	}
	domains, err = fixture.service.MailboxDomains(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	var defaults []string
	for _, item := range domains {
		if item.Default {
			defaults = append(defaults, item.Name)
		}
	}
	if len(defaults) != 1 || defaults[0] != chosen.Name {
		t.Fatalf("mailbox domain defaults = %v, want %s", defaults, chosen.Name)
	}
}

func TestListedDomainsCountTheirMailboxes(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	used := fixture.verifiedAt(t, "a.example.com", time.Hour)
	fixture.verifiedAt(t, "b.example.com", time.Hour)
	for _, localPart := range []string{"sales", "help"} {
		if _, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput(localPart, "", used.Name)); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := fixture.service.ListMailDomains(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, mailDomain := range listed {
		counts[mailDomain.Name] = mailDomain.MailboxCount
	}
	if counts["a.example.com"] != 2 || counts["b.example.com"] != 0 {
		t.Fatalf("mailbox counts = %v", counts)
	}
}
