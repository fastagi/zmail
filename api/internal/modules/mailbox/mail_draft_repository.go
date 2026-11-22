package mailbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailDraftPO is one member's saved, unsent message.
type MailDraftPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;index:idx_mail_drafts_owner,priority:1"`
	UserID         uint   `gorm:"not null;index:idx_mail_drafts_owner,priority:2;index"`
	MailboxID      uint   `gorm:"not null;index"`
	ToRecipients   string `gorm:"type:text;not null"`
	CcRecipients   string `gorm:"type:text;not null"`
	Subject        string `gorm:"type:text;not null"`
	Body           string `gorm:"type:text;not null"`
	InReplyTo      string `gorm:"type:text;not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailDraftPO) TableName() string { return "mail_drafts" }

func (po MailDraftPO) toDomain() domain.MailDraft {
	return domain.MailDraft{
		ID: po.ID, OrganizationID: po.OrganizationID, UserID: po.UserID, MailboxID: po.MailboxID,
		To: po.ToRecipients, Cc: po.CcRecipients, Subject: po.Subject, Body: po.Body, InReplyTo: po.InReplyTo,
		CreatedAt: po.CreatedAt.UTC(), UpdatedAt: po.UpdatedAt.UTC(),
	}
}

func mailDraftPO(draft domain.MailDraft) MailDraftPO {
	return MailDraftPO{
		ID: draft.ID, OrganizationID: draft.OrganizationID, UserID: draft.UserID, MailboxID: draft.MailboxID,
		ToRecipients: draft.To, CcRecipients: draft.Cc, Subject: draft.Subject, Body: draft.Body, InReplyTo: draft.InReplyTo,
		CreatedAt: draft.CreatedAt, UpdatedAt: draft.UpdatedAt,
	}
}

func (r *repository) ownDrafts(ctx context.Context, organizationID, userID uint) *gorm.DB {
	return r.conn(ctx).Model(&MailDraftPO{}).Where("organization_id = ? AND user_id = ?", organizationID, userID)
}

// ListMailDrafts lists a member's drafts in an organization, the latest change first.
func (r *repository) ListMailDrafts(ctx context.Context, organizationID, userID uint, limit int) ([]domain.MailDraft, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailDraftPO
	columns := "id, organization_id, user_id, mailbox_id, to_recipients, cc_recipients, subject, " +
		fmt.Sprintf("left(body, %d) AS body, in_reply_to, created_at, updated_at", domain.MailDraftListBodyLength)
	if err := r.ownDrafts(ctx, organizationID, userID).Select(columns).
		Order("updated_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	drafts := make([]domain.MailDraft, 0, len(rows))
	for _, row := range rows {
		drafts = append(drafts, row.toDomain())
	}
	return drafts, nil
}

// FindMailDraft reads one of a member's drafts; another's is not found.
func (r *repository) FindMailDraft(ctx context.Context, organizationID, userID, id uint) (domain.MailDraft, error) {
	if r.db == nil {
		return domain.MailDraft{}, domain.ErrServiceUnavailable
	}
	var po MailDraftPO
	err := r.ownDrafts(ctx, organizationID, userID).Where("id = ?", id).Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.MailDraft{}, ErrMailDraftNotFound
	}
	if err != nil {
		return domain.MailDraft{}, err
	}
	return po.toDomain(), nil
}

// CreateMailDraft stores a new draft and returns it with its id. A transaction-scoped advisory
// lock per writer serializes the count and insert, so concurrent saves cannot pass the limit.
func (r *repository) CreateMailDraft(ctx context.Context, draft domain.MailDraft, limit int) (domain.MailDraft, error) {
	if r.db == nil {
		return domain.MailDraft{}, domain.ErrServiceUnavailable
	}
	po := mailDraftPO(draft)
	po.ID = 0
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		owner := fmt.Sprintf("mail_drafts:%d:%d", draft.OrganizationID, draft.UserID)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", owner).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&MailDraftPO{}).
			Where("organization_id = ? AND user_id = ?", draft.OrganizationID, draft.UserID).
			Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(limit) {
			return domain.ErrMailDraftLimit
		}
		return tx.Create(&po).Error
	})
	if err != nil {
		return domain.MailDraft{}, err
	}
	return po.toDomain(), nil
}

// UpdateMailDraft replaces what a draft holds; another member's is not found.
func (r *repository) UpdateMailDraft(ctx context.Context, draft domain.MailDraft) (domain.MailDraft, error) {
	if r.db == nil {
		return domain.MailDraft{}, domain.ErrServiceUnavailable
	}
	result := r.ownDrafts(ctx, draft.OrganizationID, draft.UserID).Where("id = ?", draft.ID).Updates(map[string]any{
		"mailbox_id": draft.MailboxID, "to_recipients": draft.To, "cc_recipients": draft.Cc,
		"subject": draft.Subject, "body": draft.Body, "in_reply_to": draft.InReplyTo, "updated_at": draft.UpdatedAt,
	})
	if result.Error != nil {
		return domain.MailDraft{}, result.Error
	}
	if result.RowsAffected == 0 {
		return domain.MailDraft{}, ErrMailDraftNotFound
	}
	return r.FindMailDraft(ctx, draft.OrganizationID, draft.UserID, draft.ID)
}

// DeleteMailDraft removes one of a member's drafts; another's is not found.
func (r *repository) DeleteMailDraft(ctx context.Context, organizationID, userID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.ownDrafts(ctx, organizationID, userID).Where("id = ?", id).Delete(&MailDraftPO{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailDraftNotFound
	}
	return nil
}
