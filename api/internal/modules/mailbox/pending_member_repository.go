package mailbox

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailboxPendingMemberRepository = (*repository)(nil)

// MailboxPendingMemberPO is the persistent object for a pending shared-mailbox member grant.
type MailboxPendingMemberPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;index:idx_mailbox_pending_members_org_email,priority:1"`
	MailboxID      uint   `gorm:"not null;uniqueIndex:idx_mailbox_pending_members_mailbox_email,priority:1"`
	Email          string `gorm:"size:320;not null;uniqueIndex:idx_mailbox_pending_members_mailbox_email,priority:2;index:idx_mailbox_pending_members_org_email,priority:2"`
	Permission     string `gorm:"size:16;not null"`
	GrantedBy      uint   `gorm:"not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailboxPendingMemberPO) TableName() string {
	return "mailbox_pending_members"
}

func (po *MailboxPendingMemberPO) toDomain() *domain.MailboxPendingMember {
	return &domain.MailboxPendingMember{
		ID:             po.ID,
		OrganizationID: po.OrganizationID,
		MailboxID:      po.MailboxID,
		Email:          po.Email,
		Permission:     domain.MailboxPermission(po.Permission),
		GrantedBy:      po.GrantedBy,
		CreatedAt:      po.CreatedAt,
		UpdatedAt:      po.UpdatedAt,
	}
}

func (r *repository) UpsertPendingMailboxMember(ctx context.Context, pending *domain.MailboxPendingMember) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	now := time.Now().UTC()
	email := strings.ToLower(strings.TrimSpace(pending.Email))
	po := &MailboxPendingMemberPO{
		OrganizationID: pending.OrganizationID,
		MailboxID:      pending.MailboxID,
		Email:          email,
		Permission:     string(pending.Permission),
		GrantedBy:      pending.GrantedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err := r.conn(ctx).
		Clauses(
			clause.OnConflict{
				Columns: []clause.Column{{Name: "mailbox_id"}, {Name: "email"}},
				DoUpdates: clause.Assignments(map[string]any{
					"permission": po.Permission,
					"granted_by": po.GrantedBy,
					"updated_at": now,
				}),
			},
			clause.Returning{},
		).
		Create(po).Error
	if err != nil {
		return err
	}
	*pending = *po.toDomain()
	return nil
}

func (r *repository) ClaimPendingMailboxMembers(ctx context.Context, organizationID uint, email string) ([]*domain.MailboxPendingMember, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	if organizationID == 0 || normalized == "" {
		return nil, nil
	}
	var rows []MailboxPendingMemberPO
	err := r.conn(ctx).
		Clauses(clause.Returning{}).
		Where("organization_id = ? AND email = ?", organizationID, normalized).
		Delete(&rows).Error
	if err != nil {
		return nil, err
	}
	claimed := make([]*domain.MailboxPendingMember, 0, len(rows))
	for i := range rows {
		claimed = append(claimed, rows[i].toDomain())
	}
	return claimed, nil
}
