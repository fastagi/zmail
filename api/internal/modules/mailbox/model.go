package mailbox

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/fastmail/api/internal/domain"
)

// MailboxPO is the persistent object for a mailbox.
type MailboxPO struct {
	ID          uint   `gorm:"primaryKey"`
	UserID      uint   `gorm:"not null"`
	Address     string `gorm:"size:320;not null;uniqueIndex:idx_mailboxes_address"`
	DisplayName string `gorm:"size:120;not null;default:''"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt
}

func (MailboxPO) TableName() string {
	return "mailboxes"
}

func (po *MailboxPO) toDomain() *domain.Mailbox {
	if po == nil {
		return nil
	}
	return &domain.Mailbox{
		ID:          po.ID,
		UserID:      po.UserID,
		Address:     po.Address,
		DisplayName: po.DisplayName,
		CreatedAt:   po.CreatedAt,
		UpdatedAt:   po.UpdatedAt,
	}
}

// MailMessagePO is the persistent object for one stored message.
type MailMessagePO struct {
	ID                     uint    `gorm:"primaryKey"`
	MailboxID              uint    `gorm:"not null;index:idx_mail_messages_listing,priority:1;uniqueIndex:idx_mail_messages_dedupe,priority:1,where:dedupe_key IS NOT NULL;uniqueIndex:idx_mail_messages_idempotency,priority:1,where:idempotency_key IS NOT NULL"`
	Direction              string  `gorm:"size:16;not null;index:idx_mail_messages_listing,priority:2"`
	Status                 string  `gorm:"size:16;not null"`
	FromAddress            string  `gorm:"size:320;not null"`
	FromName               string  `gorm:"size:255;not null;default:''"`
	ToAddresses            string  `gorm:"type:text;not null;default:'[]'"`
	CcAddresses            string  `gorm:"type:text;not null;default:'[]'"`
	Subject                string  `gorm:"size:998;not null;default:''"`
	TextBody               string  `gorm:"type:text;not null;default:''"`
	HTMLBody               string  `gorm:"column:html_body;type:text;not null;default:''"`
	MessageID              string  `gorm:"size:998;not null;default:''"`
	InReplyTo              string  `gorm:"size:998;not null;default:''"`
	DedupeKey              *string `gorm:"size:64;uniqueIndex:idx_mail_messages_dedupe,priority:2,where:dedupe_key IS NOT NULL"`
	FailureCode            string  `gorm:"size:120;not null;default:''"`
	IdempotencyKey         *string `gorm:"size:255;uniqueIndex:idx_mail_messages_idempotency,priority:2,where:idempotency_key IS NOT NULL"`
	IdempotencyFingerprint string  `gorm:"size:64;not null;default:''"`
	ReadAt                 *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (MailMessagePO) TableName() string {
	return "mail_messages"
}

func (po *MailMessagePO) toDomain() *domain.MailMessage {
	if po == nil {
		return nil
	}
	message := &domain.MailMessage{
		ID:          po.ID,
		MailboxID:   po.MailboxID,
		Direction:   domain.MailDirection(po.Direction),
		Status:      domain.MailStatus(po.Status),
		FromAddress: po.FromAddress,
		FromName:    po.FromName,
		To:          decodeAddresses(po.ToAddresses),
		Cc:          decodeAddresses(po.CcAddresses),
		Subject:     po.Subject,
		TextBody:    po.TextBody,
		HTMLBody:    po.HTMLBody,
		MessageID:   po.MessageID,
		InReplyTo:   po.InReplyTo,
		FailureCode: po.FailureCode,
		ReadAt:      po.ReadAt,
		CreatedAt:   po.CreatedAt,
		UpdatedAt:   po.UpdatedAt,
	}
	if po.DedupeKey != nil {
		message.DedupeKey = *po.DedupeKey
	}
	if po.IdempotencyKey != nil {
		message.IdempotencyKey = *po.IdempotencyKey
	}
	message.IdempotencyFingerprint = po.IdempotencyFingerprint
	return message
}

func newMailMessagePO(message *domain.MailMessage) *MailMessagePO {
	po := &MailMessagePO{
		ID:          message.ID,
		MailboxID:   message.MailboxID,
		Direction:   string(message.Direction),
		Status:      string(message.Status),
		FromAddress: message.FromAddress,
		FromName:    message.FromName,
		ToAddresses: encodeAddresses(message.To),
		CcAddresses: encodeAddresses(message.Cc),
		Subject:     message.Subject,
		TextBody:    message.TextBody,
		HTMLBody:    message.HTMLBody,
		MessageID:   message.MessageID,
		InReplyTo:   message.InReplyTo,
		FailureCode: message.FailureCode,
		ReadAt:      message.ReadAt,
	}
	if message.DedupeKey != "" {
		key := message.DedupeKey
		po.DedupeKey = &key
	}
	if message.IdempotencyKey != "" {
		key := message.IdempotencyKey
		po.IdempotencyKey = &key
	}
	po.IdempotencyFingerprint = message.IdempotencyFingerprint
	return po
}

func encodeAddresses(addresses []string) string {
	if len(addresses) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(addresses)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func decodeAddresses(raw string) []string {
	addresses := []string{}
	if raw == "" {
		return addresses
	}
	if err := json.Unmarshal([]byte(raw), &addresses); err != nil {
		return []string{}
	}
	return addresses
}
