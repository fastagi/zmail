package mailbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func openDomainRepository(t *testing.T) *repository {
	t.Helper()
	repo, db := openRepository(t)
	if err := db.AutoMigrate(&MailDomainPO{}); err != nil {
		t.Fatalf("migrate mail domains: %v", err)
	}
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	return repo
}

func TestRepositoryKeepsADomainWithOneOrganization(t *testing.T) {
	repo := openDomainRepository(t)
	ctx := context.Background()
	first := &domain.MailDomain{OrganizationID: 1, Name: "news.example.com", ZoneID: "z", ZoneName: "example.com", CreatedBy: 1}
	if err := repo.CreateDomain(ctx, first, 2); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	if first.ID == 0 || first.Status != domain.MailDomainStatusPending || first.Records == nil {
		t.Fatalf("created = %+v", first)
	}
	if err := repo.CreateDomain(ctx, &domain.MailDomain{OrganizationID: 2, Name: "news.example.com", ZoneID: "z", ZoneName: "example.com", CreatedBy: 2}, 2); !errors.Is(err, ErrMailDomainTaken) {
		t.Fatalf("same name in another organization = %v", err)
	}
	if err := repo.CreateDomain(ctx, &domain.MailDomain{OrganizationID: 1, Name: "b.example.com", ZoneID: "z", ZoneName: "example.com", CreatedBy: 1}, 2); err != nil {
		t.Fatalf("second domain: %v", err)
	}
	if err := repo.CreateDomain(ctx, &domain.MailDomain{OrganizationID: 1, Name: "c.example.com", ZoneID: "z", ZoneName: "example.com", CreatedBy: 1}, 2); !errors.Is(err, ErrMailDomainLimitReached) {
		t.Fatalf("over the limit = %v", err)
	}
	if _, err := repo.FindDomain(ctx, 2, first.ID); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("read across organizations = %v", err)
	}
	listed, err := repo.ListDomains(ctx, 1)
	if err != nil || len(listed) != 2 || listed[0].Name != "b.example.com" {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
}

func TestRepositoryStoresADomainCheck(t *testing.T) {
	repo := openDomainRepository(t)
	ctx := context.Background()
	mailDomain := &domain.MailDomain{OrganizationID: 1, Name: "news.example.com", ZoneID: "z", ZoneName: "example.com", CreatedBy: 1}
	if err := repo.CreateDomain(ctx, mailDomain, 10); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	checked := time.Now().UTC().Truncate(time.Second)
	mailDomain.Status = domain.MailDomainStatusVerified
	mailDomain.SendingTag = "tag"
	mailDomain.CheckedAt, mailDomain.VerifiedAt = &checked, &checked
	mailDomain.Records = []domain.MailDomainRecord{{Purpose: domain.MailDomainRecordDMARC, Type: "TXT", Name: "_dmarc.news.example.com", Content: "v=DMARC1; p=none", Status: domain.MailDomainRecordOK}}
	if err := repo.UpdateDomain(ctx, mailDomain); err != nil {
		t.Fatalf("UpdateDomain: %v", err)
	}
	found, err := repo.FindDomainByName(ctx, "NEWS.example.com")
	if err != nil || found.Status != domain.MailDomainStatusVerified || found.SendingTag != "tag" ||
		len(found.Records) != 1 || found.Records[0].Content != "v=DMARC1; p=none" || found.VerifiedAt == nil {
		t.Fatalf("found = %+v, %v", found, err)
	}
	mailDomain.OrganizationID = 2
	if err := repo.UpdateDomain(ctx, mailDomain); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("update across organizations = %v", err)
	}
}

func TestRepositoryDeletesOnlyADomainWithoutLiveMailboxes(t *testing.T) {
	repo := openDomainRepository(t)
	ctx := context.Background()
	mailDomain := &domain.MailDomain{OrganizationID: 1, Name: "news.example.com", ZoneID: "z", ZoneName: "example.com", CreatedBy: 1}
	if err := repo.CreateDomain(ctx, mailDomain, 10); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	mailbox := createMailbox(t, repo, "hello@news.example.com")
	// An address on a look-alike domain does not hold this one.
	createMailbox(t, repo, "hello@xnews.example.com")
	if err := repo.SetMailboxRoutingRule(ctx, mailbox.ID, "rule-1"); err != nil {
		t.Fatalf("SetMailboxRoutingRule: %v", err)
	}
	stored, err := repo.FindMailbox(ctx, 1, mailbox.ID)
	if err != nil || stored.RoutingRuleID != "rule-1" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}

	if err := repo.DeleteDomain(ctx, 1, mailDomain.ID); !errors.Is(err, ErrMailDomainInUse) {
		t.Fatalf("delete in use = %v", err)
	}
	if err := repo.DeleteMailbox(ctx, 1, mailbox.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	if err := repo.DeleteDomain(ctx, 2, mailDomain.ID); !errors.Is(err, ErrMailDomainNotFound) {
		t.Fatalf("delete across organizations = %v", err)
	}
	if err := repo.DeleteDomain(ctx, 1, mailDomain.ID); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}
}

func TestRepositoryListsMailboxesWithoutARoutingRule(t *testing.T) {
	repo := openDomainRepository(t)
	ctx := context.Background()
	unrouted := createMailbox(t, repo, "hello@news.example.com")
	routed := createMailbox(t, repo, "sales@news.example.com")
	if err := repo.SetMailboxRoutingRule(ctx, routed.ID, "rule-1"); err != nil {
		t.Fatalf("SetMailboxRoutingRule: %v", err)
	}
	deleted := createMailbox(t, repo, "gone@news.example.com")
	if err := repo.DeleteMailbox(ctx, 1, deleted.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	// Neither a look-alike domain nor another organization counts.
	createMailbox(t, repo, "hello@xnews.example.com")

	listed, err := repo.ListMailboxesWithoutRoutingRule(ctx, 1, "news.example.com")
	if err != nil || len(listed) != 1 || listed[0].ID != unrouted.ID {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	if other, err := repo.ListMailboxesWithoutRoutingRule(ctx, 2, "news.example.com"); err != nil || len(other) != 0 {
		t.Fatalf("other organization = %+v, %v", other, err)
	}
}
