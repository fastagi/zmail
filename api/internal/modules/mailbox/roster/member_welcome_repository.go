package roster

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
// welcome sent. The resend columns are owned by 2026_12_27_140000_add_member_welcome_resends.
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

// MemberWelcomeDelivery is a mailbox's welcome record joined with its latest message; the message
// columns are nil once the message is past retention.
type MemberWelcomeDelivery struct {
	MailboxID     uint
	CreatedAt     time.Time
	LastSentAt    *time.Time
	ResendCount   int
	MessageStatus *string
	FailureCode   *string
	SentAt        *time.Time
}

// FindMemberWelcomeSetting reads the organization's saved welcome setting row. Found is false when
// no row has been saved yet.
func (r *Repository) FindMemberWelcomeSetting(ctx context.Context, organizationID uint) (MemberWelcomeSettingPO, bool, error) {
	if r.db == nil {
		return MemberWelcomeSettingPO{}, false, domain.ErrServiceUnavailable
	}
	var po MemberWelcomeSettingPO
	err := r.conn(ctx).Where("organization_id = ?", organizationID).Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MemberWelcomeSettingPO{}, false, nil
	}
	if err != nil {
		return MemberWelcomeSettingPO{}, false, err
	}
	return po, true, nil
}

// SaveMemberWelcomeSetting upserts the organization's welcome setting row.
func (r *Repository) SaveMemberWelcomeSetting(
	ctx context.Context,
	organizationID uint,
	enabled bool,
	language, subject, body string,
	updatedBy uint,
	at time.Time,
) (MemberWelcomeSettingPO, error) {
	if r.db == nil {
		return MemberWelcomeSettingPO{}, domain.ErrServiceUnavailable
	}
	po := MemberWelcomeSettingPO{
		OrganizationID: organizationID, Enabled: enabled, Language: language,
		Subject: subject, Body: body, UpdatedBy: &updatedBy, CreatedAt: at, UpdatedAt: at,
	}
	err := r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "organization_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "language", "subject", "body", "updated_by", "updated_at"}),
	}).Create(&po).Error
	if err != nil {
		return MemberWelcomeSettingPO{}, err
	}
	return po, nil
}

// ClaimMemberWelcome records that the mailbox is being welcomed; false when it already was.
func (r *Repository) ClaimMemberWelcome(ctx context.Context, organizationID, mailboxID uint, recipient string, at time.Time) (bool, error) {
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

// LinkMemberWelcomeMessage points the mailbox's welcome record at its transactional message.
func (r *Repository) LinkMemberWelcomeMessage(ctx context.Context, mailboxID, messageID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MemberWelcomeMailPO{}).Where("mailbox_id = ?", mailboxID).
		Update("transactional_message_id", messageID).Error
}

// LockMemberWelcome locks the mailbox's welcome record for a resend, first creating it with
// recipient and at when the mailbox was never welcomed; created says it did. Call it in a
// transaction: a resend that fails rolls the new record back.
func (r *Repository) LockMemberWelcome(
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

// SaveMemberWelcomeResend stores the record of a resend locked by LockMemberWelcome.
func (r *Repository) SaveMemberWelcomeResend(ctx context.Context, record MemberWelcomeMailPO) error {
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

// ListWelcomeCandidates lists the organization's Zmail personal mailboxes whose owner has not
// left, oldest first, bounded by the mailbox ceiling; mailboxID > 0 narrows it to one.
func (r *Repository) ListWelcomeCandidates(ctx context.Context, organizationID, mailboxID uint) ([]*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).
		Where("organization_id = ? AND type = ? AND origin <> ? AND owner_left_at IS NULL",
			organizationID, string(domain.MailboxTypePersonal), string(domain.MailboxOriginConnected))
	if mailboxID > 0 {
		query = query.Where("id = ?", mailboxID)
	}
	var rows []mailboxRow
	if err := query.Order("id asc").Limit(domain.MailboxesPerOrganizationCeiling).Find(&rows).Error; err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	for i := range rows {
		mailboxes = append(mailboxes, rows[i].toDomain())
	}
	return mailboxes, nil
}

// ListMemberWelcomeDeliveries reads the latest welcome of each welcomed mailbox of the
// organization with its send log state; mailboxID > 0 narrows it to one.
func (r *Repository) ListMemberWelcomeDeliveries(ctx context.Context, organizationID, mailboxID uint) ([]MemberWelcomeDelivery, error) {
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
	var rows []MemberWelcomeDelivery
	if err := query.Limit(domain.MailboxesPerOrganizationCeiling).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
