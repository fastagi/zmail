package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
	infradatabase "github.com/fastagi/zmail/api/internal/infra/database"
)

type repository struct {
	db *gorm.DB
}

var _ domain.MailboxRepository = (*repository)(nil)

// NewRepository creates a new repository.
func NewRepository(db *gorm.DB) *repository {
	return &repository{db: db}
}

// conn returns the transaction bound to ctx, or the pool.
func (r *repository) conn(ctx context.Context) *gorm.DB {
	return infradatabase.ResolveContextDB(ctx, r.db)
}

func (r *repository) InTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	if _, bound := infradatabase.TransactionFromContext(ctx); bound {
		return fn(ctx)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(infradatabase.ContextWithTransaction(ctx, tx))
	})
}

// CreateMailbox inserts a mailbox while holding a per-organization advisory lock, so two concurrent
// requests cannot both pass the ownership limit.
func (r *repository) CreateMailbox(ctx context.Context, mailbox *domain.Mailbox, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mailbox_owner'), ?)`, int32(mailbox.OrganizationID)).Error; err != nil { //nolint:gosec // lock key only needs to be stable per user
			return err
		}
		var owned int64
		if err := tx.Model(&MailboxPO{}).Where("organization_id = ?", mailbox.OrganizationID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return ErrMailboxLimitReached
		}
		organizationID := mailbox.OrganizationID
		po := &MailboxPO{
			OrganizationID: &organizationID,
			CreatedBy:      mailbox.CreatedBy,
			Address:        mailbox.Address,
			DisplayName:    mailbox.DisplayName,
			RoutingRuleID:  mailbox.RoutingRuleID,
			Type:           string(mailbox.Type),
			OwnerUserID:    mailbox.OwnerUserID,
		}
		if po.Type == "" {
			po.Type = string(domain.MailboxTypeShared)
		}
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

func (r *repository) ListMailboxes(ctx context.Context, organizationID, readerMembershipID uint) ([]*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailboxPO
	err := r.conn(ctx).
		Where("organization_id = ?", organizationID).
		Order("id asc").
		Limit(domain.MaxMailboxesPerOrganization).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	ids := make([]uint, 0, len(rows))
	for i := range rows {
		mailboxes = append(mailboxes, rows[i].toDomain())
		ids = append(ids, rows[i].ID)
	}
	if len(ids) == 0 {
		return mailboxes, nil
	}

	// One grouped query for every mailbox, served by the (mailbox_id, direction) index: inbound
	// mail after the seal boundary without a read row of the reader.
	var counts []struct {
		MailboxID uint
		Unread    int
	}
	err = r.conn(ctx).Table("mail_messages").
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id").
		Select("mail_messages.mailbox_id, COUNT(*) AS unread").
		Where("mail_messages.mailbox_id IN ? AND mail_messages.direction = ?", ids, string(domain.MailDirectionInbound)).
		Where(unsealedMessage).
		Where("NOT EXISTS (SELECT 1 FROM mail_message_reads WHERE mail_message_reads.message_id = mail_messages.id AND mail_message_reads.membership_id = ?)",
			readerMembershipID).
		Group("mail_messages.mailbox_id").
		Scan(&counts).Error
	if err != nil {
		return nil, err
	}
	unread := make(map[uint]int, len(counts))
	for _, count := range counts {
		unread[count.MailboxID] = count.Unread
	}
	for _, mailbox := range mailboxes {
		mailbox.UnreadCount = unread[mailbox.ID]
	}
	return mailboxes, nil
}

func (r *repository) FindMailbox(ctx context.Context, organizationID, id uint) (*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailboxPO
	if err := r.conn(ctx).Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
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
	if err := r.conn(ctx).Where("address = ?", strings.ToLower(address)).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailboxNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

// DeleteMailbox soft-deletes the mailbox, which keeps its address reserved, and removes its
// messages in the same transaction.
func (r *repository) DeleteMailbox(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND organization_id = ?", id, organizationID).Delete(&MailboxPO{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMailboxNotFound
		}
		if err := tx.Where("mailbox_id = ?", id).Delete(&MailboxMemberPO{}).Error; err != nil {
			return err
		}
		attached := tx.Model(&MailMessagePO{}).Select("id").Where("mailbox_id = ?", id)
		if err := tx.Where("message_id IN (?)", attached).Delete(&MailAttachmentPO{}).Error; err != nil {
			return err
		}
		if err := tx.Where("message_id IN (?)", attached).Delete(&MailMessageReadPO{}).Error; err != nil {
			return err
		}
		return tx.Where("mailbox_id = ?", id).Delete(&MailMessagePO{}).Error
	})
}

func (r *repository) CreateMessage(ctx context.Context, message *domain.MailMessage) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	po := newMailMessagePO(message)
	created := false
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx
		if message.IdempotencyKey != "" {
			query = query.Clauses(clause.OnConflict{
				Columns:     []clause.Column{{Name: "mailbox_id"}, {Name: "idempotency_key"}},
				TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "idempotency_key IS NOT NULL"}}},
				DoNothing:   true,
			})
		}
		result := query.Create(po)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		created = true
		return createAttachments(tx, po.ID, message.Attachments)
	})
	if err != nil {
		return false, err
	}
	if !created {
		existing, err := r.FindMessageByIdempotencyKey(ctx, message.MailboxID, message.IdempotencyKey)
		if err != nil {
			return false, err
		}
		*message = *existing
		return false, nil
	}
	message.ID = po.ID
	message.AttachmentCount = po.AttachmentCount
	message.CreatedAt = po.CreatedAt
	message.UpdatedAt = po.UpdatedAt
	return true, nil
}

// createAttachments stores the files of a new message and fills in their identifiers.
func createAttachments(tx *gorm.DB, messageID uint, attachments []domain.MailAttachment) error {
	for i := range attachments {
		attachment := &attachments[i]
		po := &MailAttachmentPO{
			MessageID:   messageID,
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
			Size:        len(attachment.Content),
			Content:     attachment.Content,
		}
		if po.Content == nil {
			po.Content = []byte{}
		}
		if err := tx.Create(po).Error; err != nil {
			return err
		}
		attachment.ID, attachment.MessageID, attachment.Size = po.ID, messageID, po.Size
	}
	return nil
}

func (r *repository) FindMessageByIdempotencyKey(ctx context.Context, mailboxID uint, key string) (*domain.MailMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailMessagePO
	err := r.conn(ctx).Where("mailbox_id = ? AND idempotency_key = ?", mailboxID, key).First(&po).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailMessageNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) FindOutboundMessageForOrganization(ctx context.Context, organizationID, id uint) (*domain.MailMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailMessagePO
	err := r.conn(ctx).
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id AND mailboxes.deleted_at IS NULL").
		Where("mail_messages.id = ? AND mailboxes.organization_id = ? AND mail_messages.direction = ?",
			id, organizationID, string(domain.MailDirectionOutbound)).
		Where(unsealedMessage).
		First(&po).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailMessageNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) CreateInboundMessage(ctx context.Context, message *domain.MailMessage) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	if message.DedupeKey == "" {
		return false, ErrMailMessageInvalid
	}
	po := newMailMessagePO(message)
	created := false
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.
			Clauses(clause.OnConflict{
				Columns:     []clause.Column{{Name: "mailbox_id"}, {Name: "dedupe_key"}},
				TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "dedupe_key IS NOT NULL"}}},
				DoNothing:   true,
			}).
			Create(po)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		created = true
		return createAttachments(tx, po.ID, message.Attachments)
	})
	if err != nil {
		return false, err
	}
	if created {
		message.ID = po.ID
		message.AttachmentCount = po.AttachmentCount
		message.CreatedAt = po.CreatedAt
		message.UpdatedAt = po.UpdatedAt
		return true, nil
	}

	var existing MailMessagePO
	err = r.conn(ctx).
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
	return r.conn(ctx).Model(&MailMessagePO{}).Where("id = ?", id).Updates(map[string]any{
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

	query := r.conn(ctx).Model(&MailMessagePO{}).Where("mailbox_id = ?", mailboxID).Where(unsealedMessage)
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
	if err := r.conn(ctx).Where("id = ? AND mailbox_id = ?", id, mailboxID).Where(unsealedMessage).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailMessageNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) SetMessageRead(ctx context.Context, mailboxID, id, membershipID uint, read bool, at time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	message, err := r.FindMessage(ctx, mailboxID, id)
	if err != nil {
		return err
	}
	if membershipID == 0 {
		return fmt.Errorf("%w: read state needs a membership", domain.ErrInvalidInput)
	}
	if message.Direction == domain.MailDirectionOutbound {
		return nil
	}
	if !read {
		return r.conn(ctx).Where("membership_id = ? AND message_id = ?", membershipID, id).Delete(&MailMessageReadPO{}).Error
	}
	err = r.conn(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&MailMessageReadPO{MembershipID: membershipID, MessageID: id, ReadAt: at}).Error
	if isForeignKeyViolation(err) {
		// The message was deleted after it was found.
		return ErrMailMessageNotFound
	}
	return err
}

func isForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return true
	}
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "23503"
}

func (r *repository) ReadMessages(ctx context.Context, membershipID uint, messageIDs []uint) (map[uint]bool, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	read := make(map[uint]bool, len(messageIDs))
	if membershipID == 0 || len(messageIDs) == 0 {
		return read, nil
	}
	var ids []uint
	err := r.conn(ctx).Model(&MailMessageReadPO{}).
		Where("membership_id = ? AND message_id IN ?", membershipID, messageIDs).
		Pluck("message_id", &ids).Error
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		read[id] = true
	}
	return read, nil
}

func (r *repository) DeleteMailboxReads(ctx context.Context, mailboxID, membershipID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	messages := r.conn(ctx).Model(&MailMessagePO{}).Select("id").Where("mailbox_id = ?", mailboxID)
	query := r.conn(ctx).Where("message_id IN (?)", messages)
	if membershipID != 0 {
		query = query.Where("membership_id = ?", membershipID)
	}
	return query.Delete(&MailMessageReadPO{}).Error
}

func (r *repository) DeleteMessage(ctx context.Context, mailboxID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND mailbox_id = ?", id, mailboxID).Where(unsealedMessage).Delete(&MailMessagePO{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMailMessageNotFound
		}
		// The schema cascades too; deleting here keeps file content and read rows from outliving
		// their message even where the foreign key is absent.
		if err := tx.Where("message_id = ?", id).Delete(&MailMessageReadPO{}).Error; err != nil {
			return err
		}
		return tx.Where("message_id = ?", id).Delete(&MailAttachmentPO{}).Error
	})
}

// CountOutboundSince counts messages the organization tried to send since the given time,
// including failed attempts, across every mailbox it owns or has deleted.
func (r *repository) CountOutboundSince(ctx context.Context, organizationID uint, since time.Time) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&MailMessagePO{}).
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id").
		Where("mailboxes.organization_id = ? AND mail_messages.direction = ? AND mail_messages.send_attempted_at >= ?",
			organizationID, string(domain.MailDirectionOutbound), since).
		Count(&count).Error
	return count, err
}

// CountWaitingSends counts the organization's messages waiting for room in the send limit.
func (r *repository) CountWaitingSends(ctx context.Context, organizationID uint) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&MailMessagePO{}).
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id").
		Where("mailboxes.organization_id = ? AND mail_messages.queued_reason IS NOT NULL", organizationID).
		Count(&count).Error
	return count, err
}

type mailLogRow struct {
	MailMessagePO
	MailboxAddress string
}

func (r *repository) ListMessagesForOrganization(
	ctx context.Context,
	organizationID uint,
	mailboxIDs []uint,
	filter domain.MailLogFilter,
) ([]*domain.MailMessage, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	if len(mailboxIDs) == 0 {
		return []*domain.MailMessage{}, 0, nil
	}
	page, perPage := filter.Page, filter.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 15
	}

	query := r.conn(ctx).Table("mail_messages").
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id AND mailboxes.deleted_at IS NULL").
		Where("mailboxes.organization_id = ? AND mail_messages.mailbox_id IN ?", organizationID, mailboxIDs).
		Where(unsealedMessage)
	if filter.Direction != "" {
		query = query.Where("mail_messages.direction = ?", string(filter.Direction))
	}
	if filter.Status != "" {
		query = query.Where("mail_messages.status = ?", string(filter.Status))
	}
	if filter.MailboxID != 0 {
		query = query.Where("mail_messages.mailbox_id = ?", filter.MailboxID)
	}
	if filter.Query != "" {
		pattern := "%" + escapeLike(filter.Query) + "%"
		query = query.Where(
			`(mail_messages.subject ILIKE ? ESCAPE '\' OR mail_messages.from_address ILIKE ? ESCAPE '\' OR mail_messages.to_addresses ILIKE ? ESCAPE '\')`,
			pattern, pattern, pattern,
		)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []mailLogRow
	// The log never needs the bodies; the detail endpoint returns them.
	err := query.
		Select(`mail_messages.id, mail_messages.mailbox_id, mail_messages.direction, mail_messages.status,
			mail_messages.from_address, mail_messages.from_name, mail_messages.to_addresses,
			mail_messages.cc_addresses, mail_messages.subject, mail_messages.message_id,
			mail_messages.in_reply_to, mail_messages.failure_code, mail_messages.last_event,
			mail_messages.attachment_count, mail_messages.template_id,
			mail_messages.created_at, mail_messages.updated_at, mailboxes.address AS mailbox_address`).
		Order("mail_messages.id DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	messages := make([]*domain.MailMessage, 0, len(rows))
	for i := range rows {
		message := rows[i].toDomain()
		message.MailboxAddress = rows[i].MailboxAddress
		messages = append(messages, message)
	}
	return messages, total, nil
}

func (r *repository) StatsForOrganization(ctx context.Context, organizationID uint, mailboxIDs []uint, since time.Time) ([]domain.MailDayStats, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if len(mailboxIDs) == 0 {
		return []domain.MailDayStats{}, nil
	}
	var rows []struct {
		Day      time.Time
		Sent     int
		Failed   int
		Received int
	}
	err := r.conn(ctx).Table("mail_messages").
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id AND mailboxes.deleted_at IS NULL").
		Select(`date_trunc('day', mail_messages.created_at AT TIME ZONE 'UTC') AS day,
			COUNT(*) FILTER (WHERE mail_messages.status = 'sent') AS sent,
			COUNT(*) FILTER (WHERE mail_messages.status = 'failed') AS failed,
			COUNT(*) FILTER (WHERE mail_messages.direction = 'inbound') AS received`).
		Where("mailboxes.organization_id = ? AND mail_messages.mailbox_id IN ? AND mail_messages.created_at >= ?",
			organizationID, mailboxIDs, since).
		Where(unsealedMessage).
		Group("day").
		Order("day ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	stats := make([]domain.MailDayStats, 0, len(rows))
	for _, row := range rows {
		day := time.Date(row.Day.Year(), row.Day.Month(), row.Day.Day(), 0, 0, 0, 0, time.UTC)
		stats = append(stats, domain.MailDayStats{Day: day, Sent: row.Sent, Failed: row.Failed, Received: row.Received})
	}
	return stats, nil
}

// AddMessageEvents locks the message row so concurrent provider events for one message are
// applied one after another, stores the new events, and recomputes the summary from the full
// history.
func (r *repository) AddMessageEvents(
	ctx context.Context,
	messageID uint,
	events []domain.MailEvent,
) (int, domain.MailEventType, error) {
	if r.db == nil {
		return 0, "", domain.ErrServiceUnavailable
	}
	added := 0
	var summary domain.MailEventType
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var locked MailMessagePO
		lock := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", messageID).First(&locked)
		if lock.Error != nil {
			if errors.Is(lock.Error, gorm.ErrRecordNotFound) {
				return ErrMailMessageNotFound
			}
			return lock.Error
		}
		for _, event := range events {
			po := &MailMessageEventPO{
				MessageID:  messageID,
				Type:       string(event.Type),
				Recipient:  event.Recipient,
				Detail:     event.Detail,
				OccurredAt: event.OccurredAt,
			}
			query := tx
			if event.ProviderEventID != "" {
				providerEventID := event.ProviderEventID
				po.ProviderEventID = &providerEventID
				query = tx.Clauses(clause.OnConflict{
					Columns:     []clause.Column{{Name: "provider_event_id"}},
					TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "provider_event_id IS NOT NULL"}}},
					DoNothing:   true,
				})
			}
			result := query.Create(po)
			if result.Error != nil {
				return result.Error
			}
			added += int(result.RowsAffected)
		}

		var rows []MailMessageEventPO
		if err := tx.Where("message_id = ?", messageID).Order("occurred_at ASC, id ASC").Find(&rows).Error; err != nil {
			return err
		}
		history := make([]domain.MailEvent, 0, len(rows))
		for i := range rows {
			history = append(history, rows[i].toDomain())
		}
		summary = domain.SummarizeMailEvents(history)
		return tx.Model(&MailMessagePO{}).Where("id = ?", messageID).UpdateColumn("last_event", string(summary)).Error
	})
	if err != nil {
		return 0, "", err
	}
	return added, summary, nil
}

func (r *repository) ListMessageEvents(ctx context.Context, messageID uint) ([]domain.MailEvent, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailMessageEventPO
	err := r.conn(ctx).Where("message_id = ?", messageID).Order("occurred_at ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	events := make([]domain.MailEvent, 0, len(rows))
	for i := range rows {
		events = append(events, rows[i].toDomain())
	}
	return events, nil
}

func (r *repository) ListMessageAttachments(ctx context.Context, messageID uint) ([]domain.MailAttachment, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailAttachmentPO
	// The listing never loads file content.
	err := r.conn(ctx).Omit("content").Where("message_id = ?", messageID).Order("id ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	attachments := make([]domain.MailAttachment, 0, len(rows))
	for i := range rows {
		attachments = append(attachments, rows[i].toDomain())
	}
	return attachments, nil
}

func (r *repository) FindMessageAttachment(ctx context.Context, messageID, id uint) (*domain.MailAttachment, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailAttachmentPO
	if err := r.conn(ctx).Where("id = ? AND message_id = ?", id, messageID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailMessageNotFound
		}
		return nil, err
	}
	attachment := po.toDomain()
	return &attachment, nil
}

// FindOutboundMessageByProviderID finds the message whatever its seal: the event is still
// recorded, and the returned mailbox lets the caller decide what may be published.
func (r *repository) FindOutboundMessageByProviderID(ctx context.Context, providerMessageID string) (*domain.MailMessage, *domain.Mailbox, error) {
	if r.db == nil {
		return nil, nil, domain.ErrServiceUnavailable
	}
	if providerMessageID == "" {
		return nil, nil, ErrMailMessageNotFound
	}
	var row struct {
		MailMessagePO
		MailboxAddress         string
		OrganizationID         *uint
		MailboxType            string
		MailboxWebhooksEnabled bool
		MailboxSealedBefore    *time.Time
	}
	err := r.conn(ctx).Table("mail_messages").
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id AND mailboxes.deleted_at IS NULL").
		Select(`mail_messages.id, mail_messages.mailbox_id, mail_messages.direction, mail_messages.status,
			mail_messages.from_address, mail_messages.from_name, mail_messages.to_addresses,
			mail_messages.cc_addresses, mail_messages.subject, mail_messages.message_id,
			mail_messages.in_reply_to, mail_messages.failure_code, mail_messages.last_event,
			mail_messages.attachment_count, mail_messages.template_id, mail_messages.created_at,
			mail_messages.updated_at,
			mailboxes.address AS mailbox_address, mailboxes.organization_id AS organization_id,
			mailboxes.type AS mailbox_type, mailboxes.webhooks_enabled AS mailbox_webhooks_enabled,
			mailboxes.sealed_before AS mailbox_sealed_before`).
		Where("mail_messages.message_id = ? AND mail_messages.direction = ?", providerMessageID, string(domain.MailDirectionOutbound)).
		Order("mail_messages.id DESC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, nil, err
	}
	if row.ID == 0 {
		return nil, nil, ErrMailMessageNotFound
	}
	message := row.toDomain()
	message.MailboxAddress = row.MailboxAddress
	mailbox := &domain.Mailbox{
		ID: row.MailboxID, Address: row.MailboxAddress, Type: domain.MailboxType(row.MailboxType),
		WebhooksEnabled: row.MailboxWebhooksEnabled, SealedBefore: row.MailboxSealedBefore,
	}
	if row.OrganizationID != nil {
		message.OrganizationID = *row.OrganizationID
		mailbox.OrganizationID = *row.OrganizationID
	}
	return message, mailbox, nil
}

// escapeLike makes user text match literally inside a LIKE pattern.
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "23505"
}
