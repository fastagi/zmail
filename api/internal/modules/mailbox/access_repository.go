package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

func (r *repository) FindMailboxMember(ctx context.Context, mailboxID, membershipID uint) (*domain.MailboxMember, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailboxMemberPO
	err := r.conn(ctx).Where("mailbox_id = ? AND membership_id = ?", mailboxID, membershipID).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) ListMailboxMembers(ctx context.Context, mailboxID uint) ([]*domain.MailboxMember, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailboxMemberPO
	err := r.conn(ctx).Where("mailbox_id = ?", mailboxID).
		Order("user_id ASC").Limit(domain.MaxMailboxMembers).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	members := make([]*domain.MailboxMember, 0, len(rows))
	for i := range rows {
		members = append(members, rows[i].toDomain())
	}
	return members, nil
}

// ListMembershipMailboxMembers is bounded by the organization's mailbox limit: a membership holds
// at most one row per mailbox.
func (r *repository) ListMembershipMailboxMembers(ctx context.Context, membershipID uint) ([]*domain.MailboxMember, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailboxMemberPO
	err := r.conn(ctx).Where("membership_id = ?", membershipID).
		Order("mailbox_id ASC").Limit(domain.MaxMailboxesPerOrganization).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	members := make([]*domain.MailboxMember, 0, len(rows))
	for i := range rows {
		members = append(members, rows[i].toDomain())
	}
	return members, nil
}

// UpsertMailboxMember locks the mailbox row, so concurrent additions are counted one after
// another and never pass the member limit together.
func (r *repository) UpsertMailboxMember(ctx context.Context, member *domain.MailboxMember, limit int) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	created := false
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var locked MailboxPO
		lock := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", member.MailboxID).First(&locked)
		if lock.Error != nil {
			if errors.Is(lock.Error, gorm.ErrRecordNotFound) {
				return ErrMailboxNotFound
			}
			return lock.Error
		}
		var existing MailboxMemberPO
		err := tx.Where("mailbox_id = ? AND membership_id = ?", member.MailboxID, member.MembershipID).First(&existing).Error
		now := time.Now().UTC()
		switch {
		case err == nil:
			existing.Permission, existing.GrantedBy, existing.UpdatedAt = string(member.Permission), member.GrantedBy, now
			update := tx.Model(&MailboxMemberPO{}).
				Where("mailbox_id = ? AND membership_id = ?", member.MailboxID, member.MembershipID).
				Updates(map[string]any{"permission": existing.Permission, "granted_by": existing.GrantedBy, "updated_at": now})
			if update.Error != nil {
				return update.Error
			}
			*member = *existing.toDomain()
			return nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}
		var count int64
		if err := tx.Model(&MailboxMemberPO{}).Where("mailbox_id = ?", member.MailboxID).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(limit) {
			return ErrMailboxMemberLimit
		}
		po := &MailboxMemberPO{
			MailboxID:    member.MailboxID,
			MembershipID: member.MembershipID,
			UserID:       member.UserID,
			Permission:   string(member.Permission),
			GrantedBy:    member.GrantedBy,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := tx.Create(po).Error; err != nil {
			return err
		}
		created = true
		*member = *po.toDomain()
		return nil
	})
	return created, err
}

func (r *repository) DeleteMailboxMember(ctx context.Context, mailboxID, userID uint) (*domain.MailboxMember, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailboxMemberPO
	result := r.conn(ctx).Clauses(clause.Returning{}).
		Where("mailbox_id = ? AND user_id = ?", mailboxID, userID).
		Delete(&rows)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 || len(rows) == 0 {
		return nil, ErrMailboxMemberNotFound
	}
	return rows[0].toDomain(), nil
}

func (r *repository) AddMailboxAccessEvent(ctx context.Context, event *domain.MailboxAccessEvent) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	detail := event.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	po := &MailboxAccessEventPO{
		OrganizationID: event.OrganizationID,
		MailboxID:      event.MailboxID,
		Type:           string(event.Type),
		ActorUserID:    event.ActorUserID,
		SubjectUserID:  event.SubjectUserID,
		Detail:         string(encoded),
		CreatedAt:      event.CreatedAt,
	}
	if err := r.conn(ctx).Create(po).Error; err != nil {
		return err
	}
	event.ID = po.ID
	return nil
}

func (r *repository) ListMailboxAccessEvents(ctx context.Context, mailboxID uint, page, size int) ([]*domain.MailboxAccessEvent, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 15
	}
	query := r.conn(ctx).Model(&MailboxAccessEventPO{}).Where("mailbox_id = ?", mailboxID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MailboxAccessEventPO
	if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	events := make([]*domain.MailboxAccessEvent, 0, len(rows))
	for i := range rows {
		events = append(events, rows[i].toDomain())
	}
	return events, total, nil
}
