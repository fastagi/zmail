package roster

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

type memoryContactRepo struct {
	mu       sync.Mutex
	nextID   uint
	contacts map[uint]*domain.MailContact
	lists    map[uint]*domain.MailContactList
}

func newMemoryContactRepo() *memoryContactRepo {
	return &memoryContactRepo{
		contacts: map[uint]*domain.MailContact{},
		lists:    map[uint]*domain.MailContactList{},
	}
}

func cloneContact(c *domain.MailContact) *domain.MailContact {
	cp := *c
	cp.Properties = make(map[string]string, len(c.Properties))
	for k, v := range c.Properties {
		cp.Properties[k] = v
	}
	cp.ListIDs = append([]uint(nil), c.ListIDs...)
	return &cp
}

func (m *memoryContactRepo) ListContacts(_ context.Context, organizationID uint, filter domain.MailContactFilter) ([]*domain.MailContact, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []*domain.MailContact
	for _, c := range m.contacts {
		if c.OrganizationID != organizationID {
			continue
		}
		if filter.Status != "" && c.Status != filter.Status {
			continue
		}
		if filter.ListID != 0 && !slices.Contains(c.ListIDs, filter.ListID) {
			continue
		}
		if filter.Query != "" {
			q := strings.ToLower(filter.Query)
			if !strings.Contains(strings.ToLower(c.Email), q) &&
				!strings.Contains(strings.ToLower(c.FirstName), q) &&
				!strings.Contains(strings.ToLower(c.LastName), q) {
				continue
			}
		}
		matched = append(matched, cloneContact(c))
	}
	slices.SortFunc(matched, func(a, b *domain.MailContact) int {
		if a.ID > b.ID {
			return -1
		}
		if a.ID < b.ID {
			return 1
		}
		return 0
	})
	total := int64(len(matched))
	page := max(filter.Page, 1)
	perPage := max(filter.PerPage, 20)
	start := (page - 1) * perPage
	if start >= len(matched) {
		return []*domain.MailContact{}, total, nil
	}
	end := min(start+perPage, len(matched))
	return matched[start:end], total, nil
}

func (m *memoryContactRepo) FindContact(_ context.Context, organizationID, id uint) (*domain.MailContact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.contacts[id]
	if !ok || c.OrganizationID != organizationID {
		return nil, domain.ErrMailContactNotFound
	}
	return cloneContact(c), nil
}

func (m *memoryContactRepo) CreateContact(_ context.Context, contact *domain.MailContact, limit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := 0
	for _, existing := range m.contacts {
		if existing.OrganizationID != contact.OrganizationID {
			continue
		}
		owned++
		if existing.Email == contact.Email {
			return domain.ErrMailContactExists
		}
	}
	if owned >= limit {
		return domain.ErrMailContactLimitReached
	}
	for _, listID := range contact.ListIDs {
		list, ok := m.lists[listID]
		if !ok || list.OrganizationID != contact.OrganizationID {
			return domain.ErrMailContactListNotFound
		}
	}
	m.nextID++
	now := time.Now().UTC()
	stored := cloneContact(contact)
	stored.ID = m.nextID
	stored.CreatedAt = now
	stored.UpdatedAt = now
	m.contacts[stored.ID] = stored
	*contact = *cloneContact(stored)
	return nil
}

func (m *memoryContactRepo) UpdateContact(_ context.Context, contact *domain.MailContact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.contacts[contact.ID]
	if !ok || existing.OrganizationID != contact.OrganizationID {
		return domain.ErrMailContactNotFound
	}
	for _, listID := range contact.ListIDs {
		list, exists := m.lists[listID]
		if !exists || list.OrganizationID != contact.OrganizationID {
			return domain.ErrMailContactListNotFound
		}
	}
	stored := cloneContact(contact)
	stored.CreatedAt = existing.CreatedAt
	stored.UpdatedAt = time.Now().UTC()
	m.contacts[stored.ID] = stored
	*contact = *cloneContact(stored)
	return nil
}

func (m *memoryContactRepo) DeleteContact(_ context.Context, organizationID, id uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.contacts[id]
	if !ok || existing.OrganizationID != organizationID {
		return domain.ErrMailContactNotFound
	}
	delete(m.contacts, id)
	return nil
}

func (m *memoryContactRepo) ImportContacts(_ context.Context, organizationID uint, rows []domain.MailContactImportRow, listID uint, limit int) (domain.MailContactImportCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var counts domain.MailContactImportCounts
	if listID != 0 {
		list, ok := m.lists[listID]
		if !ok || list.OrganizationID != organizationID {
			return counts, domain.ErrMailContactListNotFound
		}
	}
	byEmail := map[string]*domain.MailContact{}
	owned := 0
	for _, c := range m.contacts {
		if c.OrganizationID == organizationID {
			byEmail[c.Email] = c
			owned++
		}
	}
	newCount := 0
	for _, row := range rows {
		if _, exists := byEmail[row.Email]; !exists {
			newCount++
		}
	}
	if owned+newCount > limit {
		return counts, domain.ErrMailContactLimitReached
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if existing, exists := byEmail[row.Email]; exists {
			if row.FirstName != "" {
				existing.FirstName = row.FirstName
			}
			if row.LastName != "" {
				existing.LastName = row.LastName
			}
			for k, v := range row.Properties {
				existing.Properties[k] = v
			}
			if listID != 0 && !slices.Contains(existing.ListIDs, listID) {
				existing.ListIDs = append(existing.ListIDs, listID)
				slices.Sort(existing.ListIDs)
			}
			existing.UpdatedAt = now
			counts.Updated++
			continue
		}
		m.nextID++
		c := &domain.MailContact{
			ID:             m.nextID,
			OrganizationID: organizationID,
			Email:          row.Email,
			FirstName:      row.FirstName,
			LastName:       row.LastName,
			Properties:     row.Properties,
			Status:         domain.MailContactSubscribed,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if listID != 0 {
			c.ListIDs = []uint{listID}
		}
		m.contacts[c.ID] = c
		byEmail[c.Email] = c
		counts.Created++
	}
	return counts, nil
}

func (m *memoryContactRepo) ListContactLists(_ context.Context, organizationID uint) ([]*domain.MailContactList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var lists []*domain.MailContactList
	for _, l := range m.lists {
		if l.OrganizationID != organizationID {
			continue
		}
		count := 0
		for _, c := range m.contacts {
			if c.OrganizationID == organizationID && slices.Contains(c.ListIDs, l.ID) {
				count++
			}
		}
		cp := *l
		cp.ContactCount = count
		lists = append(lists, &cp)
	}
	slices.SortFunc(lists, func(a, b *domain.MailContactList) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return lists, nil
}

func (m *memoryContactRepo) CreateContactList(_ context.Context, list *domain.MailContactList, limit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := 0
	for _, existing := range m.lists {
		if existing.OrganizationID != list.OrganizationID {
			continue
		}
		owned++
		if existing.Name == list.Name {
			return domain.ErrMailContactListNameTaken
		}
	}
	if owned >= limit {
		return domain.ErrMailContactListLimit
	}
	m.nextID++
	cp := *list
	cp.ID = m.nextID
	cp.CreatedAt = time.Now().UTC()
	m.lists[cp.ID] = &cp
	*list = cp
	return nil
}

func (m *memoryContactRepo) RenameContactList(_ context.Context, organizationID, id uint, name string) (*domain.MailContactList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.lists[id]
	if !ok || existing.OrganizationID != organizationID {
		return nil, domain.ErrMailContactListNotFound
	}
	for _, other := range m.lists {
		if other.OrganizationID == organizationID && other.ID != id && other.Name == name {
			return nil, domain.ErrMailContactListNameTaken
		}
	}
	existing.Name = name
	cp := *existing
	return &cp, nil
}

func (m *memoryContactRepo) DeleteContactList(_ context.Context, organizationID, id uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.lists[id]
	if !ok || existing.OrganizationID != organizationID {
		return domain.ErrMailContactListNotFound
	}
	delete(m.lists, id)
	for _, c := range m.contacts {
		c.ListIDs = slices.DeleteFunc(c.ListIDs, func(v uint) bool { return v == id })
	}
	return nil
}

func TestServiceContactCRUDAndValidation(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryContactRepo()
	svc := NewService(repo)

	vip, err := svc.CreateContactList(ctx, 1, "  VIP   Customers ")
	if err != nil {
		t.Fatalf("CreateContactList: %v", err)
	}
	if vip.Name != "VIP Customers" {
		t.Fatalf("expected normalized list name, got %q", vip.Name)
	}
	otherOrgList, err := svc.CreateContactList(ctx, 2, "Other")
	if err != nil {
		t.Fatalf("CreateContactList org2: %v", err)
	}

	created, err := svc.CreateContact(ctx, 1, ContactInput{
		Email:      "  ANN@Example.COM ",
		FirstName:  "Ann ",
		LastName:   " Lee",
		Properties: map[string]string{"plan": "pro", "empty": "  "},
		ListIDs:    []uint{vip.ID, vip.ID},
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if created.Email != "ann@example.com" || created.FirstName != "Ann" || created.LastName != "Lee" {
		t.Fatalf("unexpected contact: %+v", created)
	}
	if created.Status != domain.MailContactSubscribed || len(created.Properties) != 1 || created.Properties["plan"] != "pro" {
		t.Fatalf("unexpected status/properties: %+v", created)
	}
	if !slices.Equal(created.ListIDs, []uint{vip.ID}) {
		t.Fatalf("expected deduplicated list IDs [%d], got %v", vip.ID, created.ListIDs)
	}

	// Duplicate email in same org.
	if _, err := svc.CreateContact(ctx, 1, ContactInput{Email: "ann@example.com"}); !errors.Is(err, domain.ErrMailContactExists) {
		t.Fatalf("expected ErrMailContactExists, got %v", err)
	}

	// Foreign list ID rejected.
	if _, err := svc.CreateContact(ctx, 1, ContactInput{Email: "b@example.com", ListIDs: []uint{otherOrgList.ID}}); !errors.Is(err, domain.ErrMailContactListNotFound) {
		t.Fatalf("expected ErrMailContactListNotFound, got %v", err)
	}

	// Invalid inputs rejected.
	for _, invalid := range []ContactInput{
		{Email: "not-an-email"},
		{Email: "Ann <ann@example.com>"},
		{Email: "ok@example.com", Status: domain.MailContactSuppressed},
		{Email: "ok@example.com", Properties: map[string]string{"1bad": "x"}},
		{Email: "ok@example.com", Properties: map[string]string{"email": "x"}},
		{Email: "ok@example.com", ListIDs: []uint{0}},
	} {
		if _, err := svc.CreateContact(ctx, 1, invalid); !errors.Is(err, domain.ErrMailContactInvalid) {
			t.Fatalf("expected ErrMailContactInvalid for %+v, got %v", invalid, err)
		}
	}

	// Update contact & preserve suppressed status if status omitted.
	repo.contacts[created.ID].Status = domain.MailContactSuppressed
	repo.contacts[created.ID].StatusReason = "hard_bounce"
	updated, err := svc.UpdateContact(ctx, 1, created.ID, ContactInput{
		FirstName: "Annie",
		LastName:  "Lee",
	})
	if err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}
	if updated.Status != domain.MailContactSuppressed || updated.StatusReason != "hard_bounce" {
		t.Fatalf("expected suppressed status preserved, got %q (%q)", updated.Status, updated.StatusReason)
	}

	// Cross-org update rejected.
	if _, err := svc.UpdateContact(ctx, 2, created.ID, ContactInput{}); !errors.Is(err, domain.ErrMailContactNotFound) {
		t.Fatalf("expected ErrMailContactNotFound, got %v", err)
	}

	// Unsubscribe sets manual reason.
	unsub, err := svc.UpdateContact(ctx, 1, created.ID, ContactInput{Status: domain.MailContactUnsubscribed})
	if err != nil {
		t.Fatalf("UpdateContact unsubscribe: %v", err)
	}
	if unsub.Status != domain.MailContactUnsubscribed || unsub.StatusReason != statusReasonManual {
		t.Fatalf("expected manual unsubscribe, got %+v", unsub)
	}

	if err := svc.DeleteContact(ctx, 1, created.ID); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if _, err := svc.GetContact(ctx, 1, created.ID); !errors.Is(err, domain.ErrMailContactNotFound) {
		t.Fatalf("expected ErrMailContactNotFound after delete, got %v", err)
	}
}

func TestServiceCSVImport(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryContactRepo()
	svc := NewService(repo)

	list, err := svc.CreateContactList(ctx, 1, "Newsletter")
	if err != nil {
		t.Fatalf("CreateContactList: %v", err)
	}

	csvText := "\uFEFFEmail Address,First Name,Last-Name,company,123_ignored\n" +
		"alice@example.com,Alice,Smith,Acme,x\n" +
		"ALICE@example.com,Alice,Dup,Acme,x\n" +
		"bad-email,Bob,Jones,Beta,y\n" +
		"carol@example.com,Carol,Wu,Gamma,z\n" +
		",,,,\n"

	res, err := svc.ImportContacts(ctx, 1, csvText, list.ID)
	if err != nil {
		t.Fatalf("ImportContacts: %v", err)
	}
	if res.Rows != 4 || res.Created != 2 || res.Updated != 0 || res.SkippedCount != 2 {
		t.Fatalf("unexpected import counts: %+v", res)
	}
	if !slices.Equal(res.IgnoredColumns, []string{"123_ignored"}) {
		t.Fatalf("expected ignored column [123_ignored], got %v", res.IgnoredColumns)
	}
	if len(res.Skipped) != 2 || res.Skipped[0].Reason != SkipDuplicate || res.Skipped[1].Reason != SkipInvalidEmail {
		t.Fatalf("unexpected skips: %+v", res.Skipped)
	}

	// Re-importing updates existing contact without losing existing name.
	res2, err := svc.ImportContacts(ctx, 1, "email,company\nalice@example.com,NewCorp\n", list.ID)
	if err != nil {
		t.Fatalf("second ImportContacts: %v", err)
	}
	if res2.Created != 0 || res2.Updated != 1 {
		t.Fatalf("expected 1 updated, got %+v", res2)
	}

	// Invalid CSV inputs.
	for _, bad := range []string{
		"",
		"first_name,last_name\nAnn,Lee\n",
		"email,E-Mail\na@example.com,b@example.com\n",
	} {
		if _, err := svc.ImportContacts(ctx, 1, bad, 0); !errors.Is(err, domain.ErrMailContactImportInvalid) {
			t.Fatalf("expected ErrMailContactImportInvalid for %q, got %v", bad, err)
		}
	}
}
