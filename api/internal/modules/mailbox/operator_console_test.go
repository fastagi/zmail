package mailbox

import (
	"context"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestOrganizationMailboxSummaryCountsMailboxesAndListsDomains(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.connect(t, "news.example.com")
	if _, err := fixture.service.CreateMailbox(context.Background(), as(7), sharedInput("team", "news.example.com", "")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	fixture.service.settings.MaxMailboxes = 25

	summary, err := fixture.service.OrganizationMailboxSummary(context.Background(), 7)
	if err != nil {
		t.Fatalf("OrganizationMailboxSummary: %v", err)
	}
	if summary.Mailboxes != 1 || summary.MaxMailboxes != 25 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.Domains) != 1 || summary.Domains[0].Name != "news.example.com" ||
		summary.Domains[0].Status != domain.MailDomainStatusVerified {
		t.Fatalf("domains = %+v", summary.Domains)
	}

	other, err := fixture.service.OrganizationMailboxSummary(context.Background(), 99)
	if err != nil || other.Mailboxes != 0 || len(other.Domains) != 0 {
		t.Fatalf("other organization = %+v, %v", other, err)
	}
}

func TestFindConnectedDomainNormalizesTheName(t *testing.T) {
	fixture := newDomainFixture(t)
	fixture.connect(t, "news.example.com")

	found, err := fixture.service.FindConnectedDomain(context.Background(), "  News.Example.COM. ")
	if err != nil || found.Name != "news.example.com" || found.OrganizationID != 7 {
		t.Fatalf("found = %+v, %v", found, err)
	}
	missing, err := fixture.service.FindConnectedDomain(context.Background(), "missing.example.com")
	if missing != nil || err != nil {
		t.Fatalf("missing domain = %+v, %v; want nil, nil", missing, err)
	}
}
