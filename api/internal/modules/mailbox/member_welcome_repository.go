package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MemberWelcomeSettingPO is an organization's welcome mail setting; a missing row is on, in
// Chinese, with the standard text. The schema is owned by 2026_12_26_100000_add_member_welcome_mail.
type MemberWelcomeSettingPO struct {
	OrganizationID uint   `gorm:"primaryKey;autoIncrement:false"`
	Enabled        bool   `gorm:"not null"` // no default tag: GORM would drop an explicit false
	Language       string `gorm:"not null"`
	Subject        string `gorm:"not null"`
	Body           string `gorm:"not null"`
	UpdatedBy      *uint
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (MemberWelcomeSettingPO) TableName() string { return "member_welcome_settings" }

// MemberWelcomeMailPO records the one welcome mail of a mailbox.
type MemberWelcomeMailPO struct {
	ID                     uint `gorm:"primaryKey"`
	OrganizationID         uint
	MailboxID              uint
	Recipient              string
	TransactionalMessageID *uint
	CreatedAt              time.Time
}

func (MemberWelcomeMailPO) TableName() string { return "member_welcome_mails" }

// memberWelcomeStore stores the welcome mail setting and the record of each mailbox's welcome.
type memberWelcomeStore interface {
	FindMemberWelcomeSetting(ctx context.Context, organizationID uint) (MemberWelcomeSetting, error)
	SaveMemberWelcomeSetting(ctx context.Context, organizationID uint, input MemberWelcomeInput, updatedBy uint, at time.Time) (MemberWelcomeSetting, error)
	// ClaimMemberWelcome records that the mailbox is being welcomed; false when it already was.
	ClaimMemberWelcome(ctx context.Context, organizationID, mailboxID uint, recipient string, at time.Time) (bool, error)
	// LinkMemberWelcomeMessage points the mailbox's welcome record at its transactional message.
	LinkMemberWelcomeMessage(ctx context.Context, mailboxID, messageID uint) error
}

var _ memberWelcomeStore = (*repository)(nil)

func (r *repository) FindMemberWelcomeSetting(ctx context.Context, organizationID uint) (MemberWelcomeSetting, error) {
	setting := defaultMemberWelcomeSetting()
	if r.db == nil {
		return setting, domain.ErrServiceUnavailable
	}
	var po MemberWelcomeSettingPO
	err := r.conn(ctx).Where("organization_id = ?", organizationID).Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return setting, nil
	}
	if err != nil {
		return setting, err
	}
	return po.toDomain(), nil
}

func (r *repository) SaveMemberWelcomeSetting(
	ctx context.Context,
	organizationID uint,
	input MemberWelcomeInput,
	updatedBy uint,
	at time.Time,
) (MemberWelcomeSetting, error) {
	if r.db == nil {
		return MemberWelcomeSetting{}, domain.ErrServiceUnavailable
	}
	po := MemberWelcomeSettingPO{
		OrganizationID: organizationID, Enabled: input.Enabled, Language: string(input.Language),
		Subject: input.Subject, Body: input.Body, UpdatedBy: &updatedBy, CreatedAt: at, UpdatedAt: at,
	}
	err := r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "organization_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "language", "subject", "body", "updated_by", "updated_at"}),
	}).Create(&po).Error
	if err != nil {
		return MemberWelcomeSetting{}, err
	}
	return po.toDomain(), nil
}

func (r *repository) ClaimMemberWelcome(ctx context.Context, organizationID, mailboxID uint, recipient string, at time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "mailbox_id"}},
		DoNothing: true,
	}).Create(&MemberWelcomeMailPO{OrganizationID: organizationID, MailboxID: mailboxID, Recipient: recipient, CreatedAt: at})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (r *repository) LinkMemberWelcomeMessage(ctx context.Context, mailboxID, messageID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MemberWelcomeMailPO{}).Where("mailbox_id = ?", mailboxID).
		Update("transactional_message_id", messageID).Error
}

func (po MemberWelcomeSettingPO) toDomain() MemberWelcomeSetting {
	updatedAt := po.UpdatedAt
	return MemberWelcomeSetting{
		Enabled: po.Enabled, Language: MemberWelcomeLanguage(po.Language), Subject: po.Subject, Body: po.Body,
		UpdatedAt: &updatedAt,
	}
}
