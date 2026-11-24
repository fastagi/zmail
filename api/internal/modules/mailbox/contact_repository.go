package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/broadcast"
)

var _ domain.MailContactRepository = (*repository)(nil)

func (r *repository) contactRepo() *broadcast.ContactRepository {
	return broadcast.NewContactRepository(r.db, broadcast.ContactErrors{
		NotFound:      ErrMailContactNotFound,
		Exists:        ErrMailContactExists,
		LimitReached:  ErrMailContactLimitReached,
		ListNotFound:  ErrMailContactListNotFound,
		ListLimit:     ErrMailContactListLimit,
		ListNameTaken: ErrMailContactListNameTaken,
	})
}

func (r *repository) ListContacts(ctx context.Context, organizationID uint, filter domain.MailContactFilter) ([]*domain.MailContact, int64, error) {
	return r.contactRepo().ListContacts(ctx, organizationID, filter)
}

func (r *repository) FindContact(ctx context.Context, organizationID, id uint) (*domain.MailContact, error) {
	return r.contactRepo().FindContact(ctx, organizationID, id)
}

func (r *repository) CreateContact(ctx context.Context, contact *domain.MailContact, limit int) error {
	return r.contactRepo().CreateContact(ctx, contact, limit)
}

func (r *repository) UpdateContact(ctx context.Context, contact *domain.MailContact) error {
	return r.contactRepo().UpdateContact(ctx, contact)
}

func (r *repository) DeleteContact(ctx context.Context, organizationID, id uint) error {
	return r.contactRepo().DeleteContact(ctx, organizationID, id)
}

func (r *repository) ImportContacts(
	ctx context.Context,
	organizationID uint,
	rows []domain.MailContactImportRow,
	listID uint,
	limit int,
) (domain.MailContactImportCounts, error) {
	return r.contactRepo().ImportContacts(ctx, organizationID, rows, listID, limit)
}

func (r *repository) ListContactLists(ctx context.Context, organizationID uint) ([]*domain.MailContactList, error) {
	return r.contactRepo().ListContactLists(ctx, organizationID)
}

func (r *repository) CreateContactList(ctx context.Context, list *domain.MailContactList, limit int) error {
	return r.contactRepo().CreateContactList(ctx, list, limit)
}

func (r *repository) RenameContactList(ctx context.Context, organizationID, id uint, name string) (*domain.MailContactList, error) {
	return r.contactRepo().RenameContactList(ctx, organizationID, id, name)
}

func (r *repository) DeleteContactList(ctx context.Context, organizationID, id uint) error {
	return r.contactRepo().DeleteContactList(ctx, organizationID, id)
}
