package broadcast

import (
	"context"
	"errors"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/database"
)

// ContactErrors holds the sentinel errors injected into ContactRepository.
type ContactErrors struct {
	NotFound      error
	Exists        error
	LimitReached  error
	ListNotFound  error
	ListLimit     error
	ListNameTaken error
}

// ContactRepository implements domain.MailContactRepository against PostgreSQL.
type ContactRepository struct {
	db   *gorm.DB
	errs ContactErrors
}

var _ domain.MailContactRepository = (*ContactRepository)(nil)

// NewContactRepository builds a contact persistence repository.
func NewContactRepository(db *gorm.DB, errs ContactErrors) *ContactRepository {
	return &ContactRepository{db: db, errs: errs}
}

func (r *ContactRepository) conn(ctx context.Context) *gorm.DB {
	return database.ResolveContextDB(ctx, r.db).WithContext(ctx)
}

func lockContactOwner(tx *gorm.DB, organizationID uint) error {
	return tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mail_contact_owner'), ?)`, int32(organizationID)).Error //nolint:gosec // lock key only needs to be stable per organization
}

func (r *ContactRepository) ListContacts(ctx context.Context, organizationID uint, filter domain.MailContactFilter) ([]*domain.MailContact, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).Model(&MailContactPO{}).Where("mail_contacts.organization_id = ?", organizationID)
	if filter.Query != "" {
		pattern := "%" + escapeLike(filter.Query) + "%"
		query = query.Where(
			`(mail_contacts.email ILIKE ? ESCAPE '\' OR mail_contacts.first_name ILIKE ? ESCAPE '\' OR mail_contacts.last_name ILIKE ? ESCAPE '\')`,
			pattern, pattern, pattern)
	}
	if filter.Status != "" {
		query = query.Where("mail_contacts.status = ?", string(filter.Status))
	}
	if filter.ListID != 0 {
		query = query.Where(
			"EXISTS (SELECT 1 FROM mail_contact_list_members m WHERE m.contact_id = mail_contacts.id AND m.list_id = ?)",
			filter.ListID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MailContactPO
	err := query.Order("mail_contacts.id DESC").
		Offset((filter.Page - 1) * filter.PerPage).Limit(filter.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	contacts := make([]*domain.MailContact, 0, len(rows))
	for i := range rows {
		contacts = append(contacts, rows[i].toDomain())
	}
	if err := r.fillListIDs(r.conn(ctx), contacts); err != nil {
		return nil, 0, err
	}
	return contacts, total, nil
}

// fillListIDs loads the list memberships of the given contacts.
func (r *ContactRepository) fillListIDs(tx *gorm.DB, contacts []*domain.MailContact) error {
	if len(contacts) == 0 {
		return nil
	}
	byID := make(map[uint]*domain.MailContact, len(contacts))
	ids := make([]uint, 0, len(contacts))
	for _, contact := range contacts {
		byID[contact.ID] = contact
		ids = append(ids, contact.ID)
	}
	var members []MailContactListMemberPO
	if err := tx.Where("contact_id IN ?", ids).Order("list_id ASC").Find(&members).Error; err != nil {
		return err
	}
	for _, member := range members {
		if contact, ok := byID[member.ContactID]; ok {
			contact.ListIDs = append(contact.ListIDs, member.ListID)
		}
	}
	return nil
}

func (r *ContactRepository) FindContact(ctx context.Context, organizationID, id uint) (*domain.MailContact, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return r.findContact(r.conn(ctx), organizationID, id)
}

func (r *ContactRepository) findContact(tx *gorm.DB, organizationID, id uint) (*domain.MailContact, error) {
	var po MailContactPO
	if err := tx.Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, r.errs.NotFound
		}
		return nil, err
	}
	contact := po.toDomain()
	if err := r.fillListIDs(tx, []*domain.MailContact{contact}); err != nil {
		return nil, err
	}
	return contact, nil
}

// checkLists confirms every list exists in the organization.
func (r *ContactRepository) checkLists(tx *gorm.DB, organizationID uint, listIDs []uint) error {
	if len(listIDs) == 0 {
		return nil
	}
	var found int64
	err := tx.Model(&MailContactListPO{}).
		Where("organization_id = ? AND id IN ?", organizationID, listIDs).
		Count(&found).Error
	if err != nil {
		return err
	}
	if found != int64(len(listIDs)) {
		return r.errs.ListNotFound
	}
	return nil
}

func addMembers(tx *gorm.DB, listID uint, contactIDs []uint) error {
	for start := 0; start < len(contactIDs); start += ImportBatch {
		end := min(start+ImportBatch, len(contactIDs))
		members := make([]MailContactListMemberPO, 0, end-start)
		for _, contactID := range contactIDs[start:end] {
			members = append(members, MailContactListMemberPO{ListID: listID, ContactID: contactID})
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&members).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *ContactRepository) setMemberships(tx *gorm.DB, contactID uint, listIDs []uint) error {
	if err := tx.Where("contact_id = ?", contactID).Delete(&MailContactListMemberPO{}).Error; err != nil {
		return err
	}
	for _, listID := range listIDs {
		if err := addMembers(tx, listID, []uint{contactID}); err != nil {
			return err
		}
	}
	return nil
}

func (r *ContactRepository) CreateContact(ctx context.Context, contact *domain.MailContact, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockContactOwner(tx, contact.OrganizationID); err != nil {
			return err
		}
		var owned int64
		if err := tx.Model(&MailContactPO{}).Where("organization_id = ?", contact.OrganizationID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return r.errs.LimitReached
		}
		if err := r.checkLists(tx, contact.OrganizationID, contact.ListIDs); err != nil {
			return err
		}
		po := &MailContactPO{
			OrganizationID: contact.OrganizationID,
			Email:          contact.Email,
			FirstName:      contact.FirstName,
			LastName:       contact.LastName,
			Properties:     encodeProperties(contact.Properties),
			Status:         string(contact.Status),
			StatusReason:   contact.StatusReason,
		}
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return r.errs.Exists
			}
			return err
		}
		if err := r.setMemberships(tx, po.ID, contact.ListIDs); err != nil {
			return err
		}
		stored, err := r.findContact(tx, contact.OrganizationID, po.ID)
		if err != nil {
			return err
		}
		*contact = *stored
		return nil
	})
}

func (r *ContactRepository) UpdateContact(ctx context.Context, contact *domain.MailContact) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&MailContactPO{}).
			Where("id = ? AND organization_id = ?", contact.ID, contact.OrganizationID).
			Updates(map[string]any{
				"first_name":    contact.FirstName,
				"last_name":     contact.LastName,
				"properties":    encodeProperties(contact.Properties),
				"status":        string(contact.Status),
				"status_reason": contact.StatusReason,
				"updated_at":    gorm.Expr("now()"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return r.errs.NotFound
		}
		// Checked after the contact, so another organization's contact is reported as missing.
		if err := r.checkLists(tx, contact.OrganizationID, contact.ListIDs); err != nil {
			return err
		}
		if err := r.setMemberships(tx, contact.ID, contact.ListIDs); err != nil {
			return err
		}
		stored, err := r.findContact(tx, contact.OrganizationID, contact.ID)
		if err != nil {
			return err
		}
		*contact = *stored
		return nil
	})
}

func (r *ContactRepository) DeleteContact(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND organization_id = ?", id, organizationID).Delete(&MailContactPO{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return r.errs.NotFound
		}
		// The schema cascades too; deleting here keeps memberships consistent without it.
		return tx.Where("contact_id = ?", id).Delete(&MailContactListMemberPO{}).Error
	})
}

func (r *ContactRepository) ImportContacts(
	ctx context.Context,
	organizationID uint,
	rows []domain.MailContactImportRow,
	listID uint,
	limit int,
) (domain.MailContactImportCounts, error) {
	var counts domain.MailContactImportCounts
	if r.db == nil {
		return counts, domain.ErrServiceUnavailable
	}
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockContactOwner(tx, organizationID); err != nil {
			return err
		}
		if listID != 0 {
			if err := r.checkLists(tx, organizationID, []uint{listID}); err != nil {
				return err
			}
		}
		existing, err := existingContactIDs(tx, organizationID, rows)
		if err != nil {
			return err
		}
		var owned int64
		if err := tx.Model(&MailContactPO{}).Where("organization_id = ?", organizationID).Count(&owned).Error; err != nil {
			return err
		}
		if owned+int64(len(rows)-len(existing)) > int64(limit) {
			return r.errs.LimitReached
		}

		contactIDs := make([]uint, 0, len(rows))
		var created []*MailContactPO
		for _, row := range rows {
			id, found := existing[row.Email]
			if !found {
				created = append(created, &MailContactPO{
					OrganizationID: organizationID,
					Email:          row.Email,
					FirstName:      row.FirstName,
					LastName:       row.LastName,
					Properties:     encodeProperties(row.Properties),
					Status:         string(domain.MailContactSubscribed),
				})
				continue
			}
			// An empty cell keeps the stored value; properties are merged, and the status of an
			// existing contact is never changed by an import.
			err := tx.Model(&MailContactPO{}).Where("id = ?", id).Updates(map[string]any{
				"first_name": gorm.Expr("CASE WHEN ? = '' THEN first_name ELSE ? END", row.FirstName, row.FirstName),
				"last_name":  gorm.Expr("CASE WHEN ? = '' THEN last_name ELSE ? END", row.LastName, row.LastName),
				"properties": gorm.Expr("properties || ?::jsonb", encodeProperties(row.Properties)),
				"updated_at": gorm.Expr("now()"),
			}).Error
			if err != nil {
				return err
			}
			contactIDs = append(contactIDs, id)
			counts.Updated++
		}
		if len(created) > 0 {
			if err := tx.CreateInBatches(created, ImportBatch).Error; err != nil {
				return err
			}
			for _, po := range created {
				contactIDs = append(contactIDs, po.ID)
			}
			counts.Created = len(created)
		}
		if listID != 0 {
			return addMembers(tx, listID, contactIDs)
		}
		return nil
	})
	return counts, err
}

// existingContactIDs maps the addresses of the rows that already are contacts to their ids.
func existingContactIDs(tx *gorm.DB, organizationID uint, rows []domain.MailContactImportRow) (map[string]uint, error) {
	existing := map[string]uint{}
	for start := 0; start < len(rows); start += ImportBatch {
		end := min(start+ImportBatch, len(rows))
		emails := make([]string, 0, end-start)
		for _, row := range rows[start:end] {
			emails = append(emails, row.Email)
		}
		var found []MailContactPO
		err := tx.Select("id", "email").
			Where("organization_id = ? AND email IN ?", organizationID, emails).
			Find(&found).Error
		if err != nil {
			return nil, err
		}
		for _, po := range found {
			existing[po.Email] = po.ID
		}
	}
	return existing, nil
}

type contactListRow struct {
	MailContactListPO
	ContactCount int
}

func (row contactListRow) toDomain() *domain.MailContactList {
	return &domain.MailContactList{
		ID:             row.ID,
		OrganizationID: row.OrganizationID,
		Name:           row.Name,
		ContactCount:   row.ContactCount,
		CreatedAt:      row.CreatedAt,
	}
}

func (r *ContactRepository) ListContactLists(ctx context.Context, organizationID uint) ([]*domain.MailContactList, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []contactListRow
	err := r.conn(ctx).Table("mail_contact_lists").
		Select(`mail_contact_lists.*, COUNT(mail_contact_list_members.contact_id) AS contact_count`).
		Joins("LEFT JOIN mail_contact_list_members ON mail_contact_list_members.list_id = mail_contact_lists.id").
		Where("mail_contact_lists.organization_id = ?", organizationID).
		Group("mail_contact_lists.id").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].ID < rows[j].ID
	})
	lists := make([]*domain.MailContactList, 0, len(rows))
	for _, row := range rows {
		lists = append(lists, row.toDomain())
	}
	return lists, nil
}

func (r *ContactRepository) CreateContactList(ctx context.Context, list *domain.MailContactList, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockContactOwner(tx, list.OrganizationID); err != nil {
			return err
		}
		var owned int64
		if err := tx.Model(&MailContactListPO{}).Where("organization_id = ?", list.OrganizationID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return r.errs.ListLimit
		}
		po := &MailContactListPO{OrganizationID: list.OrganizationID, Name: list.Name}
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return r.errs.ListNameTaken
			}
			return err
		}
		list.ID, list.CreatedAt, list.ContactCount = po.ID, po.CreatedAt, 0
		return nil
	})
}

func (r *ContactRepository) RenameContactList(ctx context.Context, organizationID, id uint, name string) (*domain.MailContactList, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailContactListPO{}).
		Where("id = ? AND organization_id = ?", id, organizationID).
		Updates(map[string]any{"name": name, "updated_at": gorm.Expr("now()")})
	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			return nil, r.errs.ListNameTaken
		}
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, r.errs.ListNotFound
	}
	lists, err := r.ListContactLists(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	for _, list := range lists {
		if list.ID == id {
			return list, nil
		}
	}
	return nil, r.errs.ListNotFound
}

func (r *ContactRepository) DeleteContactList(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND organization_id = ?", id, organizationID).Delete(&MailContactListPO{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return r.errs.ListNotFound
		}
		return tx.Where("list_id = ?", id).Delete(&MailContactListMemberPO{}).Error
	})
}
