package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailboxDirectoryRepository = (*repository)(nil)

func (r *repository) ListMailboxDirectory(ctx context.Context, organizationID uint, filter domain.MailboxDirectoryFilter, sentSince time.Time) ([]*domain.MailboxDirectoryEntry, error) {
	return r.rosterRepo().ListMailboxDirectory(ctx, organizationID, filter, sentSince)
}

func (r *repository) CountMailboxes(ctx context.Context, organizationID uint) (int64, error) {
	return r.rosterRepo().CountMailboxes(ctx, organizationID)
}

func (r *repository) UpdateMailboxDisplayName(ctx context.Context, organizationID, id uint, displayName string) error {
	return r.rosterRepo().UpdateMailboxDisplayName(ctx, organizationID, id, displayName)
}

func (r *repository) MailboxAddressUsed(ctx context.Context, address string) (bool, error) {
	return r.rosterRepo().MailboxAddressUsed(ctx, address)
}

func (r *repository) ListMembersOfMailboxes(ctx context.Context, mailboxIDs []uint) ([]*domain.MailboxMember, error) {
	return r.rosterRepo().ListMembersOfMailboxes(ctx, mailboxIDs)
}
