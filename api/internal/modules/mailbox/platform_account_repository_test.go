package mailbox

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	ledgerOrg      = 31
	otherLedgerOrg = 32
	ledgerActor    = 70
)

// newLedgerRepository opens a repository with the ledger tables and two organizations.
func newLedgerRepository(t *testing.T) (*repository, *gorm.DB) {
	t.Helper()
	repo, db := openRepository(t)
	migrateLedger(t, db)
	createOwner(t, db, ledgerOrg)
	createOwner(t, db, otherLedgerOrg)
	return repo, db
}

// migrateLedger adds the ledger tables to a test schema.
func migrateLedger(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&PlatformAccountPO{}, &PlatformAccountEventPO{}); err != nil {
		t.Fatalf("migrate the ledger: %v", err)
	}
}

func ledgerMailbox(t *testing.T, repo *repository, address string) *domain.Mailbox {
	t.Helper()
	mailbox := &domain.Mailbox{OrganizationID: ledgerOrg, Address: address, DisplayName: address, Type: domain.MailboxTypeShared}
	if err := repo.CreateMailbox(context.Background(), mailbox, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("CreateMailbox(%q): %v", address, err)
	}
	return mailbox
}

func createLedgerAccount(t *testing.T, repo *repository, account *domain.PlatformAccount) *domain.PlatformAccount {
	t.Helper()
	if account.Status == "" {
		account.Status = domain.PlatformAccountStatusActive
	}
	if err := repo.CreatePlatformAccount(context.Background(), account, ledgerActor); err != nil {
		t.Fatalf("CreatePlatformAccount(%s %s): %v", account.Platform, account.Handle, err)
	}
	return account
}

func accountHandles(accounts []*domain.PlatformAccount) string {
	handles := make([]string, 0, len(accounts))
	for _, account := range accounts {
		handles = append(handles, account.Handle)
	}
	return strings.Join(handles, ",")
}

func eventKinds(events []*domain.PlatformAccountEvent) string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, string(event.Kind)+":"+event.From+">"+event.To)
	}
	return strings.Join(kinds, ",")
}

func listLedger(t *testing.T, repo *repository, organizationID uint, filter domain.PlatformAccountFilter) ([]*domain.PlatformAccount, int64) {
	t.Helper()
	accounts, total, err := repo.ListPlatformAccounts(context.Background(), organizationID, filter)
	if err != nil {
		t.Fatalf("ListPlatformAccounts(%+v): %v", filter, err)
	}
	return accounts, total
}

func ledgerEvents(t *testing.T, repo *repository, organizationID, accountID uint) []*domain.PlatformAccountEvent {
	t.Helper()
	events, _, err := repo.ListPlatformAccountEvents(context.Background(), organizationID, accountID, 1, 50)
	if err != nil {
		t.Fatalf("ListPlatformAccountEvents(%d): %v", accountID, err)
	}
	return events
}

func TestPlatformAccountRepositoryCreateFindList(t *testing.T) {
	repo, _ := newLedgerRepository(t)
	ctx := context.Background()
	box := ledgerMailbox(t, repo, "design@mail.example")
	owner := uint(42)
	medium := createLedgerAccount(t, repo, &domain.PlatformAccount{
		OrganizationID: ledgerOrg, Platform: "Medium", Handle: "@acme", ProfileURL: "https://medium.com/@acme",
		MailboxID: &box.ID, OwnerUserID: &owner, Status: domain.PlatformAccountStatusInvited, Notes: "Blog",
	})
	if medium.ID == 0 || medium.CreatedAt.IsZero() || medium.MailboxAddress != "design@mail.example" {
		t.Fatalf("created account = %+v; want an id, timestamps, and the registration address", medium)
	}
	createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "behance", Handle: "acme"})
	createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Behance", Handle: "Acme-studio"})
	createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: otherLedgerOrg, Platform: "Medium", Handle: "@acme"})

	found, err := repo.FindPlatformAccount(ctx, ledgerOrg, medium.ID)
	if err != nil {
		t.Fatalf("FindPlatformAccount: %v", err)
	}
	if found.Platform != "Medium" || found.ProfileURL != "https://medium.com/@acme" || found.Notes != "Blog" ||
		found.Status != domain.PlatformAccountStatusInvited || found.OwnerUserID == nil || *found.OwnerUserID != owner ||
		found.MailboxID == nil || *found.MailboxID != box.ID || found.MailboxAddress != box.Address {
		t.Fatalf("found = %+v; want the stored account", found)
	}
	_, err = repo.FindPlatformAccount(ctx, otherLedgerOrg, medium.ID)
	expectError(t, "account of another organization", err, ErrPlatformAccountNotFound)

	accounts, total := listLedger(t, repo, ledgerOrg, domain.PlatformAccountFilter{Page: 1, PerPage: 10})
	if got := accountHandles(accounts); got != "acme,Acme-studio,@acme" || total != 3 {
		t.Fatalf("listing = %s (%d); want behance accounts by handle, then medium", got, total)
	}
	page, total := listLedger(t, repo, ledgerOrg, domain.PlatformAccountFilter{Page: 2, PerPage: 2})
	if got := accountHandles(page); got != "@acme" || total != 3 {
		t.Fatalf("page 2 = %s (%d); want @acme of 3", got, total)
	}
	if got := eventKinds(ledgerEvents(t, repo, ledgerOrg, medium.ID)); got != "created:>invited" {
		t.Fatalf("events = %s; want the created event", got)
	}
}

func TestPlatformAccountUniqueIgnoringCase(t *testing.T) {
	repo, _ := newLedgerRepository(t)
	ctx := context.Background()
	createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Medium", Handle: "@acme"})

	again := &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "MEDIUM", Handle: "@ACME", Status: domain.PlatformAccountStatusActive}
	expectError(t, "same platform and handle in other case", repo.CreatePlatformAccount(ctx, again, ledgerActor), ErrPlatformAccountTaken)

	createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: otherLedgerOrg, Platform: "medium", Handle: "@acme"})
	other := createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Medium", Handle: "@other"})
	other.Handle = "@Acme"
	expectError(t, "rename onto a taken handle", repo.UpdatePlatformAccount(ctx, other, ledgerActor), ErrPlatformAccountTaken)

	other.Handle = "@OTHER"
	if err := repo.UpdatePlatformAccount(ctx, other, ledgerActor); err != nil {
		t.Fatalf("renaming an account to its own handle in another case: %v", err)
	}
}

func TestPlatformAccountRepositorySearchAndFilters(t *testing.T) {
	repo, db := newLedgerRepository(t)
	design := ledgerMailbox(t, repo, "design-desk@mail.example")
	sales := ledgerMailbox(t, repo, "sales@mail.example")
	role := uint(9)
	if err := db.AutoMigrate(&MailRolePO{}); err != nil {
		t.Fatalf("migrate roles: %v", err)
	}
	if err := db.Exec(`INSERT INTO mail_roles (id, organization_id, name, color, description, created_at, updated_at) VALUES (9, ?, 'Design', 'gray', '', now(), now())`, ledgerOrg).Error; err != nil {
		t.Fatalf("seed a role: %v", err)
	}
	owner, other := uint(42), uint(43)
	createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Medium", Handle: "blog", MailboxID: &design.ID, RoleID: &role, OwnerUserID: &owner})
	createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Behance", Handle: "portfolio", MailboxID: &sales.ID, OwnerUserID: &other, Status: domain.PlatformAccountStatusRetired})
	createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Dribbble", Handle: "shots_100%", OwnerUserID: &owner})

	for name, test := range map[string]struct {
		filter domain.PlatformAccountFilter
		want   string
	}{
		"platform ignoring case":   {domain.PlatformAccountFilter{Query: "MEDI"}, "blog"},
		"handle":                   {domain.PlatformAccountFilter{Query: "folio"}, "portfolio"},
		"registration address":     {domain.PlatformAccountFilter{Query: "design-desk@"}, "blog"},
		"wildcards are literal":    {domain.PlatformAccountFilter{Query: "_100%"}, "shots_100%"},
		"percent alone is literal": {domain.PlatformAccountFilter{Query: "%"}, "shots_100%"},
		"role":                     {domain.PlatformAccountFilter{RoleID: role}, "blog"},
		"mailbox":                  {domain.PlatformAccountFilter{MailboxID: sales.ID}, "portfolio"},
		"owner":                    {domain.PlatformAccountFilter{OwnerUserID: owner}, "shots_100%,blog"},
		"status":                   {domain.PlatformAccountFilter{Status: domain.PlatformAccountStatusRetired}, "portfolio"},
		"visible mailboxes":        {domain.PlatformAccountFilter{Visible: []uint{design.ID}}, "blog"},
		"visible owner":            {domain.PlatformAccountFilter{Visible: []uint{}, VisibleOwner: other}, "portfolio"},
		"nothing visible":          {domain.PlatformAccountFilter{Visible: []uint{}, VisibleOwner: 999}, ""},
		"address limited":          {domain.PlatformAccountFilter{Query: "design-desk@", AddressMailboxIDs: []uint{sales.ID}}, ""},
		"address allowed":          {domain.PlatformAccountFilter{Query: "sales@", AddressMailboxIDs: []uint{sales.ID}}, "portfolio"},
		"platform beats the limit": {domain.PlatformAccountFilter{Query: "medium", AddressMailboxIDs: []uint{}}, "blog"},
		"filters combine with AND": {domain.PlatformAccountFilter{OwnerUserID: owner, Query: "blog"}, "blog"},
		"no match across filters":  {domain.PlatformAccountFilter{OwnerUserID: other, Query: "blog"}, ""},
	} {
		test.filter.Page, test.filter.PerPage = 1, 20
		accounts, total := listLedger(t, repo, ledgerOrg, test.filter)
		if got := accountHandles(accounts); got != test.want || total != int64(len(accounts)) {
			t.Errorf("%s: listing = %q (%d); want %q", name, got, total, test.want)
		}
	}
}

func TestPlatformAccountRepositoryUpdateRecordsEvents(t *testing.T) {
	repo, _ := newLedgerRepository(t)
	ctx := context.Background()
	first := ledgerMailbox(t, repo, "first@mail.example")
	second := ledgerMailbox(t, repo, "second@mail.example")
	owner, successor := uint(42), uint(43)
	account := createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Medium", Handle: "blog", MailboxID: &first.ID, OwnerUserID: &owner})

	account.Notes = "only notes"
	if err := repo.UpdatePlatformAccount(ctx, account, ledgerActor); err != nil {
		t.Fatalf("UpdatePlatformAccount notes: %v", err)
	}
	account.Status = domain.PlatformAccountStatusNeedsReview
	account.OwnerUserID = &successor
	account.MailboxID = &second.ID
	if err := repo.UpdatePlatformAccount(ctx, account, ledgerActor); err != nil {
		t.Fatalf("UpdatePlatformAccount: %v", err)
	}
	if account.MailboxAddress != "second@mail.example" || account.Notes != "only notes" {
		t.Fatalf("updated account = %+v; want the stored values read back", account)
	}
	account.OwnerUserID, account.MailboxID = nil, nil
	if err := repo.UpdatePlatformAccount(ctx, account, ledgerActor); err != nil {
		t.Fatalf("UpdatePlatformAccount clear: %v", err)
	}
	want := "mailbox_changed:" + uintText(second.ID) + ">,owner_changed:43>," +
		"mailbox_changed:" + uintText(first.ID) + ">" + uintText(second.ID) + ",owner_changed:42>43,status_changed:active>needs_review,created:>active"
	events := ledgerEvents(t, repo, ledgerOrg, account.ID)
	if got := eventKinds(events); got != want {
		t.Fatalf("events = %s; want %s (newest first, nothing for notes)", got, want)
	}
	if events[0].ActorUserID == nil || *events[0].ActorUserID != ledgerActor {
		t.Fatalf("event actor = %v; want %d", events[0].ActorUserID, ledgerActor)
	}

	missing := &domain.PlatformAccount{ID: account.ID, OrganizationID: otherLedgerOrg, Platform: "x", Handle: "y", Status: domain.PlatformAccountStatusActive}
	expectError(t, "update in another organization", repo.UpdatePlatformAccount(ctx, missing, ledgerActor), ErrPlatformAccountNotFound)
	_, _, err := repo.ListPlatformAccountEvents(ctx, otherLedgerOrg, account.ID, 1, 10)
	expectError(t, "events in another organization", err, ErrPlatformAccountNotFound)
}

func TestPlatformAccountRepositoryHandover(t *testing.T) {
	repo, _ := newLedgerRepository(t)
	ctx := context.Background()
	leaver, successor, bystander := uint(42), uint(43), uint(44)
	one := createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Medium", Handle: "one", OwnerUserID: &leaver})
	two := createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Behance", Handle: "two", OwnerUserID: &leaver})
	kept := createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "X", Handle: "kept", OwnerUserID: &bystander})
	foreign := createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: otherLedgerOrg, Platform: "Medium", Handle: "one", OwnerUserID: &leaver})

	moved, err := repo.HandOverPlatformAccounts(ctx, ledgerOrg, leaver, successor, ledgerActor)
	if err != nil {
		t.Fatalf("HandOverPlatformAccounts: %v", err)
	}
	if len(moved) != 2 || moved[0] != one.ID || moved[1] != two.ID {
		t.Fatalf("moved = %v; want [%d %d]", moved, one.ID, two.ID)
	}
	for _, id := range moved {
		stored, findErr := repo.FindPlatformAccount(ctx, ledgerOrg, id)
		if findErr != nil || stored.OwnerUserID == nil || *stored.OwnerUserID != successor {
			t.Fatalf("account %d after handover = %+v, %v; want owner %d", id, stored, findErr, successor)
		}
		if got := eventKinds(ledgerEvents(t, repo, ledgerOrg, id)); got != "owner_changed:42>43,created:>active" {
			t.Fatalf("account %d events = %s", id, got)
		}
	}
	for _, untouched := range []struct {
		org   uint
		id    uint
		owner uint
	}{{ledgerOrg, kept.ID, bystander}, {otherLedgerOrg, foreign.ID, leaver}} {
		stored, findErr := repo.FindPlatformAccount(ctx, untouched.org, untouched.id)
		if findErr != nil || *stored.OwnerUserID != untouched.owner {
			t.Fatalf("account %d = %+v, %v; want it untouched", untouched.id, stored, findErr)
		}
	}

	again, err := repo.HandOverPlatformAccounts(ctx, ledgerOrg, leaver, successor, ledgerActor)
	if err != nil || len(again) != 0 {
		t.Fatalf("second handover = %v, %v; want nothing moved", again, err)
	}
}

func TestPlatformAccountRepositoryDeleteKeepsEvent(t *testing.T) {
	repo, db := newLedgerRepository(t)
	ctx := context.Background()
	account := createLedgerAccount(t, repo, &domain.PlatformAccount{OrganizationID: ledgerOrg, Platform: "Medium", Handle: "gone"})

	expectError(t, "delete in another organization", repo.DeletePlatformAccount(ctx, otherLedgerOrg, account.ID, ledgerActor), ErrPlatformAccountNotFound)
	if err := repo.DeletePlatformAccount(ctx, ledgerOrg, account.ID, ledgerActor); err != nil {
		t.Fatalf("DeletePlatformAccount: %v", err)
	}
	_, err := repo.FindPlatformAccount(ctx, ledgerOrg, account.ID)
	expectError(t, "find after delete", err, ErrPlatformAccountNotFound)
	expectError(t, "delete again", repo.DeletePlatformAccount(ctx, ledgerOrg, account.ID, ledgerActor), ErrPlatformAccountNotFound)

	var kinds []string
	if err := db.Model(&PlatformAccountEventPO{}).Where("account_id = ?", account.ID).Order("id").Pluck("kind", &kinds).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if strings.Join(kinds, ",") != "created,deleted" {
		t.Fatalf("stored events = %v; want created then deleted", kinds)
	}
}
