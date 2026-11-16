package mailbox

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

// fakeContacts is an in-memory domain.MailContactRepository for service tests.
type fakeContacts struct {
	contacts map[uint]*domain.MailContact
	lists    map[uint]*domain.MailContactList
	nextID   uint
	imported []domain.MailContactImportRow
	importTo uint
}

var _ domain.MailContactRepository = (*fakeContacts)(nil)

func newFakeContacts() *fakeContacts {
	return &fakeContacts{contacts: map[uint]*domain.MailContact{}, lists: map[uint]*domain.MailContactList{}}
}

func (f *fakeContacts) id() uint {
	f.nextID++
	return f.nextID
}

func (f *fakeContacts) ListContacts(_ context.Context, organizationID uint, _ domain.MailContactFilter) ([]*domain.MailContact, int64, error) {
	var listed []*domain.MailContact
	for _, contact := range f.contacts {
		if contact.OrganizationID == organizationID {
			listed = append(listed, contact)
		}
	}
	return listed, int64(len(listed)), nil
}

func (f *fakeContacts) FindContact(_ context.Context, organizationID, id uint) (*domain.MailContact, error) {
	contact, ok := f.contacts[id]
	if !ok || contact.OrganizationID != organizationID {
		return nil, ErrMailContactNotFound
	}
	copied := *contact
	return &copied, nil
}

func (f *fakeContacts) checkLists(organizationID uint, ids []uint) error {
	for _, id := range ids {
		if list, ok := f.lists[id]; !ok || list.OrganizationID != organizationID {
			return ErrMailContactListNotFound
		}
	}
	return nil
}

func (f *fakeContacts) CreateContact(_ context.Context, contact *domain.MailContact, _ int) error {
	for _, existing := range f.contacts {
		if existing.OrganizationID == contact.OrganizationID && existing.Email == contact.Email {
			return ErrMailContactExists
		}
	}
	if err := f.checkLists(contact.OrganizationID, contact.ListIDs); err != nil {
		return err
	}
	contact.ID = f.id()
	stored := *contact
	f.contacts[contact.ID] = &stored
	return nil
}

func (f *fakeContacts) UpdateContact(_ context.Context, contact *domain.MailContact) error {
	existing, ok := f.contacts[contact.ID]
	if !ok || existing.OrganizationID != contact.OrganizationID {
		return ErrMailContactNotFound
	}
	if err := f.checkLists(contact.OrganizationID, contact.ListIDs); err != nil {
		return err
	}
	stored := *contact
	f.contacts[contact.ID] = &stored
	return nil
}

func (f *fakeContacts) DeleteContact(_ context.Context, organizationID, id uint) error {
	contact, ok := f.contacts[id]
	if !ok || contact.OrganizationID != organizationID {
		return ErrMailContactNotFound
	}
	delete(f.contacts, id)
	return nil
}

func (f *fakeContacts) ImportContacts(_ context.Context, organizationID uint, rows []domain.MailContactImportRow, listID uint, _ int) (domain.MailContactImportCounts, error) {
	if listID != 0 {
		if err := f.checkLists(organizationID, []uint{listID}); err != nil {
			return domain.MailContactImportCounts{}, err
		}
	}
	f.imported, f.importTo = rows, listID
	return domain.MailContactImportCounts{Created: len(rows)}, nil
}

func (f *fakeContacts) ListContactLists(_ context.Context, organizationID uint) ([]*domain.MailContactList, error) {
	var lists []*domain.MailContactList
	for _, list := range f.lists {
		if list.OrganizationID == organizationID {
			lists = append(lists, list)
		}
	}
	return lists, nil
}

func (f *fakeContacts) CreateContactList(_ context.Context, list *domain.MailContactList, _ int) error {
	for _, existing := range f.lists {
		if existing.OrganizationID == list.OrganizationID && existing.Name == list.Name {
			return ErrMailContactListNameTaken
		}
	}
	list.ID = f.id()
	stored := *list
	f.lists[list.ID] = &stored
	return nil
}

func (f *fakeContacts) RenameContactList(_ context.Context, organizationID, id uint, name string) (*domain.MailContactList, error) {
	list, ok := f.lists[id]
	if !ok || list.OrganizationID != organizationID {
		return nil, ErrMailContactListNotFound
	}
	list.Name = name
	return list, nil
}

func (f *fakeContacts) DeleteContactList(_ context.Context, organizationID, id uint) error {
	list, ok := f.lists[id]
	if !ok || list.OrganizationID != organizationID {
		return ErrMailContactListNotFound
	}
	delete(f.lists, id)
	return nil
}

func TestCreateContactNormalizesAndValidates(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	list, err := fixture.service.CreateContactList(ctx, 1, "  VIP   customers ")
	if err != nil || list.Name != "VIP customers" {
		t.Fatalf("CreateContactList = %+v, %v", list, err)
	}

	contact, err := fixture.service.CreateContact(ctx, 1, ContactInput{
		Email: " Ann@Example.COM ", FirstName: "  Ann ", LastName: "Lee",
		Properties: map[string]string{"plan": " pro ", "empty": " "},
		ListIDs:    []uint{list.ID, list.ID},
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if contact.Email != "ann@example.com" || contact.FirstName != "Ann" || contact.Status != domain.MailContactSubscribed ||
		len(contact.Properties) != 1 || contact.Properties["plan"] != "pro" || !slices.Equal(contact.ListIDs, []uint{list.ID}) {
		t.Fatalf("contact = %+v", contact)
	}
	if _, err := fixture.service.CreateContact(ctx, 1, ContactInput{Email: "ann@example.com"}); !errors.Is(err, ErrMailContactExists) {
		t.Fatalf("duplicate error = %v", err)
	}

	for name, input := range map[string]ContactInput{
		"bad email":         {Email: "Ann <ann@example.com>"},
		"long name":         {Email: "a@example.com", FirstName: strings.Repeat("n", 101)},
		"reserved property": {Email: "a@example.com", Properties: map[string]string{"email": "x"}},
		"property name":     {Email: "a@example.com", Properties: map[string]string{"bad-name": "x"}},
		"long property":     {Email: "a@example.com", Properties: map[string]string{"note": strings.Repeat("x", 501)}},
		"suppressed":        {Email: "a@example.com", Status: domain.MailContactSuppressed},
		"zero list":         {Email: "a@example.com", ListIDs: []uint{0}},
	} {
		if _, err := fixture.service.CreateContact(ctx, 1, input); !errors.Is(err, ErrMailContactInvalid) {
			t.Errorf("%s: error = %v; want invalid", name, err)
		}
	}
	other, _ := fixture.service.CreateContactList(ctx, 2, "Other")
	if _, err := fixture.service.CreateContact(ctx, 1, ContactInput{Email: "b@example.com", ListIDs: []uint{other.ID}}); !errors.Is(err, ErrMailContactListNotFound) {
		t.Fatalf("another organization's list error = %v", err)
	}
}

func TestUpdateContactKeepsAddressAndHandlesStatus(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	contact, err := fixture.service.CreateContact(ctx, 1, ContactInput{Email: "ann@example.com"})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	updated, err := fixture.service.UpdateContact(ctx, 1, contact.ID, ContactInput{
		Email: "other@example.com", FirstName: "Ann", Status: domain.MailContactUnsubscribed,
	})
	if err != nil || updated.Email != "ann@example.com" || updated.Status != domain.MailContactUnsubscribed || updated.StatusReason != "manual" {
		t.Fatalf("UpdateContact = %+v, %v", updated, err)
	}
	// Leaving the status out keeps it.
	kept, err := fixture.service.UpdateContact(ctx, 1, contact.ID, ContactInput{FirstName: "Ann"})
	if err != nil || kept.Status != domain.MailContactUnsubscribed || kept.StatusReason != "manual" {
		t.Fatalf("status kept = %+v, %v", kept, err)
	}
	// A suppressed contact keeps its reason until a member names another status.
	fixture.contacts.contacts[contact.ID].Status = domain.MailContactSuppressed
	fixture.contacts.contacts[contact.ID].StatusReason = "bounced"
	stillSuppressed, err := fixture.service.UpdateContact(ctx, 1, contact.ID, ContactInput{LastName: "Lee"})
	if err != nil || stillSuppressed.Status != domain.MailContactSuppressed || stillSuppressed.StatusReason != "bounced" {
		t.Fatalf("suppressed kept = %+v, %v", stillSuppressed, err)
	}
	resubscribed, err := fixture.service.UpdateContact(ctx, 1, contact.ID, ContactInput{Status: domain.MailContactSubscribed})
	if err != nil || resubscribed.Status != domain.MailContactSubscribed || resubscribed.StatusReason != "" {
		t.Fatalf("resubscribed = %+v, %v", resubscribed, err)
	}
	if _, err := fixture.service.UpdateContact(ctx, 2, contact.ID, ContactInput{}); !errors.Is(err, ErrMailContactNotFound) {
		t.Fatalf("update from another organization error = %v", err)
	}
}

func TestImportContactsReadsColumnsAndReportsSkips(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	list, _ := fixture.service.CreateContactList(ctx, 1, "Newsletter")
	csvText := "\uFEFFEmail,First Name,last-name,Plan,Phone #,email_2\r\n" +
		"Ann@Example.com,Ann,Lee,pro,2026-01-01,x\r\n" +
		"\r\n" +
		"not-an-address,Bob,,,,\r\n" +
		"ann@example.com,Ann again,,,,\r\n" +
		`"bob@example.com","Bob, Jr.",Smith,"multi` + "\n" + `line",,` + "\r\n" +
		"carol@example.com," + strings.Repeat("c", 101) + ",,,,\r\n"

	result, err := fixture.service.ImportContacts(ctx, 1, csvText, list.ID)
	if err != nil {
		t.Fatalf("ImportContacts: %v", err)
	}
	if result.Rows != 5 || result.SkippedCount != 3 || !slices.Equal(result.IgnoredColumns, []string{"Phone #"}) {
		t.Fatalf("result = %+v", result)
	}
	wantSkips := []ContactImportSkip{{Line: 4, Reason: SkipInvalidEmail}, {Line: 5, Reason: SkipDuplicate}, {Line: 8, Reason: SkipInvalidName}}
	if !slices.Equal(result.Skipped, wantSkips) {
		t.Fatalf("skipped = %+v; want %+v", result.Skipped, wantSkips)
	}
	imported := fixture.contacts.imported
	if len(imported) != 2 || fixture.contacts.importTo != list.ID {
		t.Fatalf("imported = %+v to list %d", imported, fixture.contacts.importTo)
	}
	if imported[0].Email != "ann@example.com" || imported[0].FirstName != "Ann" || imported[0].LastName != "Lee" ||
		imported[0].Properties["plan"] != "pro" || imported[0].Properties["email_2"] != "x" {
		t.Fatalf("first row = %+v", imported[0])
	}
	if imported[1].FirstName != "Bob, Jr." || imported[1].Properties["plan"] != "multi\nline" {
		t.Fatalf("quoted row = %+v", imported[1])
	}
}

func TestImportContactsRejectsUnusableFiles(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	tooMany := "email\n" + strings.Repeat("a@example.com\n", MaxContactImportRows+1)
	for name, text := range map[string]string{
		"empty":            "",
		"no email column":  "name,plan\nAnn,pro\n",
		"duplicate column": "email,Email\na@example.com,b@example.com\n",
		"broken quotes":    "email\n\"a@example.com\n",
		"not utf-8":        "email\n\xff@example.com\n",
		"too many rows":    tooMany,
		"too large":        "email\n" + strings.Repeat("x", MaxContactImportBytes),
	} {
		if _, err := fixture.service.ImportContacts(ctx, 1, text, 0); !errors.Is(err, ErrMailContactImportInvalid) {
			t.Errorf("%s: error = %v; want invalid import", name, err)
		}
	}
	if _, err := fixture.service.ImportContacts(ctx, 1, "email\na@example.com\n", 999); !errors.Is(err, ErrMailContactListNotFound) {
		t.Fatalf("missing list error = %v", err)
	}
}

func TestContactListNamesAreValidated(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	for _, name := range []string{"", "   ", strings.Repeat("n", 101)} {
		if _, err := fixture.service.CreateContactList(ctx, 1, name); !errors.Is(err, ErrMailContactInvalid) {
			t.Errorf("name %q error = %v", name, err)
		}
	}
	list, _ := fixture.service.CreateContactList(ctx, 1, "A")
	if _, err := fixture.service.RenameContactList(ctx, 2, list.ID, "B"); !errors.Is(err, ErrMailContactListNotFound) {
		t.Fatalf("rename from another organization error = %v", err)
	}
	if _, _, err := fixture.service.ListContacts(ctx, 1, domain.MailContactFilter{Status: "bounced", Page: 1, PerPage: 10}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("bad status filter error = %v", err)
	}
}
