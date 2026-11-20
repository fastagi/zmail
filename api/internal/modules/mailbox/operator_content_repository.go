package mailbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailContentAccessAuditPO records one operator read of a broadcast body: who, when, why, and which
// organization and message. It never holds a body, subject, code, or recipient address, and its IDs
// have no foreign key so the record outlives what it names.
type MailContentAccessAuditPO struct {
	ID             uint      `gorm:"primaryKey"`
	ViewerUserID   uint      `gorm:"not null"`
	OrganizationID uint      `gorm:"not null"`
	MessageID      uint      `gorm:"not null"`
	Reason         string    `gorm:"size:500;not null"`
	AccessedAt     time.Time `gorm:"not null"`
}

func (MailContentAccessAuditPO) TableName() string {
	return "mail_content_access_audits"
}

// broadcastOfMessage is the broadcast of the message's own organization that created the message,
// or NULL. A broadcast of another organization pointing at the message never qualifies it. It
// reads idx_mail_broadcast_recipients_message.
const broadcastOfMessage = `(SELECT recipient.broadcast_id FROM mail_broadcast_recipients AS recipient
	JOIN mail_broadcasts AS broadcast ON broadcast.id = recipient.broadcast_id
		AND broadcast.organization_id = mailboxes.organization_id
	WHERE recipient.message_id = mail_messages.id
	ORDER BY recipient.id LIMIT 1)`

type broadcastContentRequest struct {
	ViewerUserID   uint
	OrganizationID uint
	MessageID      uint
	Reason         string
	At             time.Time
}

type broadcastContentRow struct {
	AccessID    uint
	BroadcastID uint
	Subject     string
	FromAddress string
	TextBody    string
	HTMLBody    string
	CreatedAt   time.Time
}

// readBroadcastContent checks that the message is an outbound Zmail broadcast message of the
// organization, records the access, and only then loads the body, all in one transaction. A failed
// audit insert or commit returns an error and no body.
func (r *repository) readBroadcastContent(ctx context.Context, request broadcastContentRequest) (*broadcastContentRow, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var content *broadcastContentRow
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var targets []struct {
			Direction   string
			Source      string
			BroadcastID *uint
		}
		if err := tx.Table("mail_messages").
			Joins(`JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id AND mailboxes.deleted_at IS NULL`).
			Where("mail_messages.id = ? AND mailboxes.organization_id = ?", request.MessageID, request.OrganizationID).
			Select(`mail_messages.direction, mail_messages.source, ` + broadcastOfMessage + ` AS broadcast_id`).
			Limit(1).
			Scan(&targets).Error; err != nil {
			return fmt.Errorf("check broadcast content target: %w", err)
		}
		if len(targets) == 0 {
			return ErrMailMessageNotFound
		}
		target := targets[0]
		if target.Direction != string(domain.MailDirectionOutbound) || target.Source != string(domain.MailSourceZmail) ||
			target.BroadcastID == nil {
			return domain.ErrOperatorContentNotEligible
		}

		audit := MailContentAccessAuditPO{
			ViewerUserID: request.ViewerUserID, OrganizationID: request.OrganizationID,
			MessageID: request.MessageID, Reason: request.Reason, AccessedAt: request.At.UTC(),
		}
		if err := tx.Create(&audit).Error; err != nil {
			return fmt.Errorf("record content access: %w", err)
		}

		var message MailMessagePO
		if err := tx.Select("id, subject, from_address, text_body, html_body, created_at").
			Where("id = ?", request.MessageID).Take(&message).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMailMessageNotFound
			}
			return fmt.Errorf("load broadcast content: %w", err)
		}
		content = &broadcastContentRow{
			AccessID: audit.ID, BroadcastID: *target.BroadcastID, Subject: message.Subject,
			FromAddress: message.FromAddress, TextBody: message.TextBody, HTMLBody: message.HTMLBody,
			CreatedAt: message.CreatedAt,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return content, nil
}

type contentAuditFilter struct {
	OrganizationID uint
	ViewerUserID   uint
	From           time.Time
	To             time.Time
}

// contentAccessAudits lists access records newest first.
func (r *repository) contentAccessAudits(
	ctx context.Context,
	filter contentAuditFilter,
	page, perPage int,
) ([]MailContentAccessAuditPO, int64, error) {
	if r == nil || r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	scope := r.conn(ctx).Model(&MailContentAccessAuditPO{})
	if filter.OrganizationID != 0 {
		scope = scope.Where("organization_id = ?", filter.OrganizationID)
	}
	if filter.ViewerUserID != 0 {
		scope = scope.Where("viewer_user_id = ?", filter.ViewerUserID)
	}
	if !filter.From.IsZero() {
		scope = scope.Where("accessed_at >= ?", filter.From)
	}
	if !filter.To.IsZero() {
		scope = scope.Where("accessed_at < ?", filter.To)
	}
	var total int64
	if err := scope.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count content access records: %w", err)
	}
	var rows []MailContentAccessAuditPO
	if err := scope.Order("accessed_at DESC, id DESC").Offset((page - 1) * perPage).Limit(perPage).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list content access records: %w", err)
	}
	return rows, total, nil
}
