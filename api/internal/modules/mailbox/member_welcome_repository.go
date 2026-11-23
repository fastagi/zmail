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

// MemberWelcomeMailPO records a mailbox's welcome mail: one row per mailbox, pointing at the latest
// welcome sent. The resend columns are owned by 2026_12_27_100000_add_member_welcome_resends.
type MemberWelcomeMailPO struct {
	ID                     uint `gorm:"primaryKey"`
	OrganizationID         uint
	MailboxID              uint
	Recipient              string
	TransactionalMessageID *uint
	CreatedAt              time.Time
	// LastSentAt is when the latest welcome was stored; nil means CreatedAt.
	LastSentAt            *time.Time
	ResendCount           int `gorm:"not null;default:0"`
	ResendWindowStartedAt *time.Time
	ResendWindowCount     int `gorm:"not null;default:0"`
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
	// LockMemberWelcome locks the mailbox's welcome record for a resend, first creating it with
	// recipient and at when the mailbox was never welcomed; created says it did. Call it in a
	// transaction: a resend that fails rolls the new record back.
	LockMemberWelcome(ctx context.Context, organizationID, mailboxID uint, recipient string, at time.Time) (MemberWelcomeMailPO, bool, error)
	// SaveMemberWelcomeResend stores the record of a resend locked by LockMemberWelcome.
	SaveMemberWelcomeResend(ctx context.Context, record MemberWelcomeMailPO) error
	// ListWelcomeCandidates lists the organization's Zmail personal mailboxes whose owner has not
	// left, oldest first, bounded by the mailbox ceiling; mailboxID > 0 narrows it to one.
	ListWelcomeCandidates(ctx context.Context, organizationID, mailboxID uint) ([]*domain.Mailbox, error)
	// ListMemberWelcomeDeliveries reads the latest welcome of each welcomed mailbox of the
	// organization with its send log state; mailboxID > 0 narrows it to one.
	ListMemberWelcomeDeliveries(ctx context.Context, organizationID, mailboxID uint) ([]memberWelcomeDelivery, error)
}

// memberWelcomeDelivery is a mailbox's welcome record joined with its latest message; the message
// columns are nil once the message is past retention.
type memberWelcomeDelivery struct {
	MailboxID     uint
	CreatedAt     time.Time
	LastSentAt    *time.Time
	ResendCount   int
	MessageStatus *string
	FailureCode   *string
	SentAt        *time.Time
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

func (r *repository) LockMemberWelcome(
	ctx context.Context,
	organizationID, mailboxID uint,
	recipient string,
	at time.Time,
) (MemberWelcomeMailPO, bool, error) {
	if r.db == nil {
		return MemberWelcomeMailPO{}, false, domain.ErrServiceUnavailable
	}
	inserted := r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "mailbox_id"}},
		DoNothing: true,
	}).Create(&MemberWelcomeMailPO{OrganizationID: organizationID, MailboxID: mailboxID, Recipient: recipient, CreatedAt: at})
	if inserted.Error != nil {
		return MemberWelcomeMailPO{}, false, inserted.Error
	}
	var record MemberWelcomeMailPO
	err := r.conn(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("mailbox_id = ? AND organization_id = ?", mailboxID, organizationID).Take(&record).Error
	if err != nil {
		return MemberWelcomeMailPO{}, false, err
	}
	return record, inserted.RowsAffected == 1, nil
}

func (r *repository) SaveMemberWelcomeResend(ctx context.Context, record MemberWelcomeMailPO) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MemberWelcomeMailPO{}).Where("id = ?", record.ID).Updates(map[string]any{
		"recipient":                record.Recipient,
		"transactional_message_id": record.TransactionalMessageID,
		"last_sent_at":             record.LastSentAt,
		"resend_count":             record.ResendCount,
		"resend_window_started_at": record.ResendWindowStartedAt,
		"resend_window_count":      record.ResendWindowCount,
	}).Error
}

func (r *repository) ListWelcomeCandidates(ctx context.Context, organizationID, mailboxID uint) ([]*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).
		Where("organization_id = ? AND type = ? AND origin <> ? AND owner_left_at IS NULL",
			organizationID, string(domain.MailboxTypePersonal), string(domain.MailboxOriginConnected))
	if mailboxID > 0 {
		query = query.Where("id = ?", mailboxID)
	}
	var rows []MailboxPO
	if err := query.Order("id asc").Limit(domain.MailboxesPerOrganizationCeiling).Find(&rows).Error; err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	for i := range rows {
		mailboxes = append(mailboxes, rows[i].toDomain())
	}
	return mailboxes, nil
}

func (r *repository) ListMemberWelcomeDeliveries(ctx context.Context, organizationID, mailboxID uint) ([]memberWelcomeDelivery, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).Table("member_welcome_mails AS w").
		Select(`w.mailbox_id, w.created_at, w.last_sent_at, w.resend_count,
			t.status AS message_status, t.failure_code, t.sent_at`).
		Joins("JOIN mailboxes AS m ON m.id = w.mailbox_id AND m.deleted_at IS NULL").
		Joins("LEFT JOIN transactional_messages AS t ON t.id = w.transactional_message_id").
		Where("w.organization_id = ?", organizationID)
	if mailboxID > 0 {
		query = query.Where("w.mailbox_id = ?", mailboxID)
	}
	var rows []memberWelcomeDelivery
	if err := query.Limit(domain.MailboxesPerOrganizationCeiling).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (po MemberWelcomeSettingPO) toDomain() MemberWelcomeSetting {
	updatedAt := po.UpdatedAt
	return MemberWelcomeSetting{
		Enabled: po.Enabled, Language: MemberWelcomeLanguage(po.Language), Subject: po.Subject, Body: po.Body,
		UpdatedAt: &updatedAt,
	}
}
