package mailbox

import (
	"context"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailboxDirectoryRepository = (*repository)(nil)

// ListMailboxDirectory reads the page first, then two grouped counts over the page's ids only, so
// its cost follows the page size rather than the organization's mailbox count. The send count is
// served by idx_mail_messages_mailbox_send_attempted.
func (r *repository) ListMailboxDirectory(ctx context.Context, organizationID uint, filter domain.MailboxDirectoryFilter, sentSince time.Time) ([]*domain.MailboxDirectoryEntry, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).Where("organization_id = ?", organizationID)
	if filter.Type != "" {
		query = query.Where("type = ?", string(filter.Type))
	}
	if filter.Query != "" {
		pattern := "%" + escapeLike(filter.Query) + "%"
		query = query.Where(`(address ILIKE ? ESCAPE '\' OR display_name ILIKE ? ESCAPE '\')`, pattern, pattern)
	}
	if filter.BeforeID != 0 {
		query = query.Where("id < ?", filter.BeforeID)
	}
	var rows []MailboxPO
	if err := query.Order("id DESC").Limit(filter.Limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	entries := make([]*domain.MailboxDirectoryEntry, 0, len(rows))
	ids := make([]uint, 0, len(rows))
	for i := range rows {
		entries = append(entries, &domain.MailboxDirectoryEntry{Mailbox: rows[i].toDomain()})
		ids = append(ids, rows[i].ID)
	}
	if len(ids) == 0 {
		return entries, nil
	}

	var memberCounts []struct {
		MailboxID uint
		Count     int
	}
	if err := r.conn(ctx).Model(&MailboxMemberPO{}).
		Select("mailbox_id, COUNT(*) AS count").
		Where("mailbox_id IN ?", ids).
		Group("mailbox_id").
		Scan(&memberCounts).Error; err != nil {
		return nil, err
	}
	var sentCounts []struct {
		MailboxID uint
		Count     int64
	}
	if err := r.conn(ctx).Model(&MailMessagePO{}).
		Select("mailbox_id, COUNT(*) AS count").
		Where("mailbox_id IN ? AND direction = ? AND send_lane = ? AND send_attempted_at >= ?",
			ids, string(domain.MailDirectionOutbound), string(domain.SendLanePersonal), sentSince).
		Group("mailbox_id").
		Scan(&sentCounts).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]*domain.MailboxDirectoryEntry, len(entries))
	for _, entry := range entries {
		byID[entry.Mailbox.ID] = entry
	}
	for _, count := range memberCounts {
		byID[count.MailboxID].MemberCount = count.Count
	}
	for _, count := range sentCounts {
		byID[count.MailboxID].SentLast24h = count.Count
	}
	return entries, nil
}

func (r *repository) CountMailboxes(ctx context.Context, organizationID uint) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&MailboxPO{}).Where("organization_id = ?", organizationID).Count(&count).Error
	return count, err
}

// UpdateMailboxDisplayName sets the display name of a live mailbox of the organization.
func (r *repository) UpdateMailboxDisplayName(ctx context.Context, organizationID, id uint, displayName string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailboxPO{}).
		Where("id = ? AND organization_id = ?", id, organizationID).
		Updates(map[string]any{"display_name": strings.TrimSpace(displayName), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailboxNotFound
	}
	return nil
}

// MailboxAddressUsed counts deleted mailboxes too: a deleted mailbox keeps its address reserved.
func (r *repository) MailboxAddressUsed(ctx context.Context, address string) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	var count int64
	if err := r.conn(ctx).Unscoped().Model(&MailboxPO{}).
		Where("address = ? AND type <> ?", strings.ToLower(strings.TrimSpace(address)), string(domain.MailboxTypeExternal)).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// mailboxMembersChunk bounds the ids of one members query.
const mailboxMembersChunk = 1000

// ListMembersOfMailboxes reads members in chunks of mailbox ids; at most
// MailboxesPerOrganizationCeiling mailboxes of MaxMailboxMembers members each.
func (r *repository) ListMembersOfMailboxes(ctx context.Context, mailboxIDs []uint) ([]*domain.MailboxMember, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	members := make([]*domain.MailboxMember, 0, len(mailboxIDs))
	for start := 0; start < len(mailboxIDs); start += mailboxMembersChunk {
		chunk := mailboxIDs[start:min(start+mailboxMembersChunk, len(mailboxIDs))]
		var rows []MailboxMemberPO
		if err := r.conn(ctx).Where("mailbox_id IN ?", chunk).Order("mailbox_id ASC, user_id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for i := range rows {
			members = append(members, rows[i].toDomain())
		}
	}
	return members, nil
}
