package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailSignaturePO is one member's signature for one mailbox.
type MailSignaturePO struct {
	MailboxID uint   `gorm:"primaryKey;autoIncrement:false"`
	UserID    uint   `gorm:"primaryKey;autoIncrement:false;index"`
	Body      string `gorm:"type:text;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (MailSignaturePO) TableName() string { return "mail_signatures" }

// FindMailSignature reads a member's signature; a missing row is an empty, unsaved one.
func (r *repository) FindMailSignature(ctx context.Context, mailboxID, userID uint) (domain.MailSignature, error) {
	signature := domain.MailSignature{MailboxID: mailboxID, UserID: userID}
	if r.db == nil {
		return signature, domain.ErrServiceUnavailable
	}
	var po MailSignaturePO
	err := r.conn(ctx).Where("mailbox_id = ? AND user_id = ?", mailboxID, userID).Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return signature, nil
	}
	if err != nil {
		return signature, err
	}
	signature.Body = po.Body
	signature.UpdatedAt = new(po.UpdatedAt.UTC())
	return signature, nil
}

// SaveMailSignature replaces a member's signature; an empty Body removes it.
func (r *repository) SaveMailSignature(ctx context.Context, signature domain.MailSignature, now time.Time) (domain.MailSignature, error) {
	if r.db == nil {
		return signature, domain.ErrServiceUnavailable
	}
	if signature.Body == "" {
		err := r.conn(ctx).Where("mailbox_id = ? AND user_id = ?", signature.MailboxID, signature.UserID).
			Delete(&MailSignaturePO{}).Error
		return domain.MailSignature{MailboxID: signature.MailboxID, UserID: signature.UserID}, err
	}
	po := MailSignaturePO{
		MailboxID: signature.MailboxID, UserID: signature.UserID, Body: signature.Body,
		CreatedAt: now, UpdatedAt: now,
	}
	err := r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "mailbox_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"body", "updated_at"}),
	}).Create(&po).Error
	if err != nil {
		return signature, err
	}
	return r.FindMailSignature(ctx, signature.MailboxID, signature.UserID)
}
