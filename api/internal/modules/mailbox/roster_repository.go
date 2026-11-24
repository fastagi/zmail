package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/roster"
)

var _ domain.MailboxRosterRepository = (*repository)(nil)

func (r *repository) rosterRepo() *roster.Repository {
	return roster.NewRepository(r.db, ErrMailboxNotFound)
}

func (r *repository) ClaimReservedMailboxes(ctx context.Context, organizationID, userID uint, email string) ([]*domain.Mailbox, error) {
	return r.rosterRepo().ClaimReservedMailboxes(ctx, organizationID, userID, email)
}

func (r *repository) OwnsPersonalMailbox(ctx context.Context, organizationID, userID uint) (bool, error) {
	return r.rosterRepo().OwnsPersonalMailbox(ctx, organizationID, userID)
}
