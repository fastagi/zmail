package mailbox

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/fastmail/api/internal/domain"
	infradatabase "github.com/fastagi/fastmail/api/internal/infra/database"
)

type repository struct {
	db *gorm.DB
}

var _ domain.MailboxRepository = (*repository)(nil)

// NewRepository creates a new repository.
func NewRepository(db *gorm.DB) *repository {
	return &repository{db: db}
}

// CreateMailbox inserts a mailbox while holding a per-user advisory lock, so two concurrent
// requests cannot both pass the ownership limit.
func (r *repository) CreateMailbox(ctx context.Context, mailbox *domain.Mailbox, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mailbox_owner'), ?)`, int32(mailbox.UserID)).Error; err != nil { //nolint:gosec // lock key only needs to be stable per user
			return err
		}
		var owned int64
		if err := tx.Model(&MailboxPO{}).Where("user_id = ?", mailbox.UserID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return ErrMailboxLimitReached
		}
		po := &MailboxPO{UserID: mailbox.UserID, Address: mailbox.Address, DisplayName: mailbox.DisplayName}
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrMailboxAddressTaken
			}
			return err
		}
		mailbox.ID = po.ID
		mailbox.CreatedAt = po.CreatedAt
		mailbox.UpdatedAt = po.UpdatedAt
		return nil
	})
}

func (r *repository) ListMailboxes(ctx context.Context, userID uint) ([]*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailboxPO
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("id asc").
		Limit(domain.MaxMailboxesPerUser).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	for i := range rows {
		mailboxes = append(mailboxes, rows[i].toDomain())
	}
	return mailboxes, nil
}

func (r *repository) FindMailbox(ctx context.Context, userID, id uint) (*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailboxPO
	if err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailboxNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) FindMailboxByAddress(ctx context.Context, address string) (*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailboxPO
	if err := r.db.WithContext(ctx).Where("address = ?", strings.ToLower(address)).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailboxNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

// DeleteMailbox soft-deletes the mailbox, which keeps its address reserved, and removes its
// messages in the same transaction.
func (r *repository) DeleteMailbox(ctx context.Context, userID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&MailboxPO{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMailboxNotFound
		}
		return tx.Where("mailbox_id = ?", id).Delete(&MailMessagePO{}).Error
	})
}

func (r *repository) DeleteMailboxesForUser(ctx context.Context, userID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	db := infradatabase.ResolveContextDB(ctx, r.db)
	owned := db.Model(&MailboxPO{}).Select("id").Where("user_id = ?", userID)
	if err := db.Where("mailbox_id IN (?)", owned).Delete(&MailMessagePO{}).Error; err != nil {
		return err
	}
	// A soft delete keeps every released address reserved.
	return db.Where("user_id = ?", userID).Delete(&MailboxPO{}).Error
}

func (r *repository) CreateMessage(ctx context.Context, message *domain.MailMessage) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := newMailMessagePO(message)
	if err := r.db.WithContext(ctx).Create(po).Error; err != nil {
		return err
	}
	message.ID = po.ID
	message.CreatedAt = po.CreatedAt
	message.UpdatedAt = po.UpdatedAt
	return nil
}

func (r *repository) CreateInboundMessage(ctx context.Context, message *domain.MailMessage) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	if message.DedupeKey == "" {
		return false, ErrMailMessageInvalid
	}
	po := newMailMessagePO(message)
	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:     []clause.Column{{Name: "mailbox_id"}, {Name: "dedupe_key"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "dedupe_key IS NOT NULL"}}},
			DoNothing:   true,
		}).
		Create(po)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		message.ID = po.ID
		message.CreatedAt = po.CreatedAt
		message.UpdatedAt = po.UpdatedAt
		return true, nil
	}

	var existing MailMessagePO
	err := r.db.WithContext(ctx).
		Where("mailbox_id = ? AND dedupe_key = ?", message.MailboxID, message.DedupeKey).
		First(&existing).Error
	if err != nil {
		return false, err
	}
	*message = *existing.toDomain()
	return false, nil
}

func (r *repository) UpdateMessageDelivery(
	ctx context.Context,
	id uint,
	status domain.MailStatus,
	messageID string,
	failureCode string,
) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.db.WithContext(ctx).Model(&MailMessagePO{}).Where("id = ?", id).Updates(map[string]any{
		"status":       string(status),
		"message_id":   messageID,
		"failure_code": failureCode,
	}).Error
}

func (r *repository) ListMessages(
	ctx context.Context,
	mailboxID uint,
	filter domain.MailMessageFilter,
) ([]*domain.MailMessage, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	page, perPage := filter.Page, filter.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 15
	}

	query := r.db.WithContext(ctx).Model(&MailMessagePO{}).Where("mailbox_id = ?", mailboxID)
	if filter.Direction != "" {
		query = query.Where("direction = ?", string(filter.Direction))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MailMessagePO
	// A listing never needs the bodies; the detail endpoint returns them.
	err := query.
		Omit("text_body", "html_body").
		Order("id desc").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	messages := make([]*domain.MailMessage, 0, len(rows))
	for i := range rows {
		messages = append(messages, rows[i].toDomain())
	}
	return messages, total, nil
}

func (r *repository) FindMessage(ctx context.Context, mailboxID, id uint) (*domain.MailMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailMessagePO
	if err := r.db.WithContext(ctx).Where("id = ? AND mailbox_id = ?", id, mailboxID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailMessageNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) SetMessageRead(ctx context.Context, mailboxID, id uint, readAt *time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.db.WithContext(ctx).Model(&MailMessagePO{}).
		Where("id = ? AND mailbox_id = ?", id, mailboxID).
		Update("read_at", readAt)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailMessageNotFound
	}
	return nil
}

func (r *repository) DeleteMessage(ctx context.Context, mailboxID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.db.WithContext(ctx).Where("id = ? AND mailbox_id = ?", id, mailboxID).Delete(&MailMessagePO{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailMessageNotFound
	}
	return nil
}

// CountOutboundSince counts messages the user tried to send since the given time, including
// failed attempts, across every mailbox the user owns or has deleted.
func (r *repository) CountOutboundSince(ctx context.Context, userID uint, since time.Time) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&MailMessagePO{}).
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id").
		Where("mailboxes.user_id = ? AND mail_messages.direction = ? AND mail_messages.created_at >= ?",
			userID, string(domain.MailDirectionOutbound), since).
		Count(&count).Error
	return count, err
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "23505"
}
