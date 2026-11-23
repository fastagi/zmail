package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailboxRosterRepository = (*repository)(nil)

// ClaimReservedMailboxes gives the user every live personal mailbox of the organization reserved
// for the email and clears the reservation in the same statement, so a mailbox is claimed once.
func (r *repository) ClaimReservedMailboxes(ctx context.Context, organizationID, userID uint, email string) ([]*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if organizationID == 0 || userID == 0 || email == "" {
		return nil, domain.ErrInvalidInput
	}
	var rows []MailboxPO
	err := r.conn(ctx).Raw(`UPDATE mailboxes SET owner_user_id = ?, pending_owner_email = NULL, updated_at = now()
		WHERE organization_id = ? AND pending_owner_email = ? AND type = 'personal'
		  AND owner_user_id IS NULL AND deleted_at IS NULL
		RETURNING *`, userID, organizationID, email).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	for i := range rows {
		mailboxes = append(mailboxes, rows[i].toDomain())
	}
	return mailboxes, nil
}

// OwnsPersonalMailbox reports whether the user owns a live Zmail personal mailbox in the
// organization; a connected outside account does not count.
func (r *repository) OwnsPersonalMailbox(ctx context.Context, organizationID, userID uint) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&MailboxPO{}).
		Where("organization_id = ? AND owner_user_id = ? AND type = 'personal' AND origin = 'zmail'", organizationID, userID).
		Limit(1).Count(&count).Error
	return count > 0, err
}
