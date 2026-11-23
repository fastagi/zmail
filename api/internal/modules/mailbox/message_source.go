package mailbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// messageSourceInbound is the origin of a source the mail edge delivered; mail submission adds
// "smtp" and "composed" later.
const messageSourceInbound = "inbound"

// reconstructedHeader marks a message rebuilt from stored fields instead of its original source.
const reconstructedHeader = "X-Zmail-Reconstructed"

// MailMessageSourcePO is the raw RFC 5322 source of one message.
type MailMessageSourcePO struct {
	MessageID uint   `gorm:"primaryKey;autoIncrement:false"`
	Raw       []byte `gorm:"not null"`
	Size      int    `gorm:"not null"`
	SHA256    string `gorm:"column:sha256;size:64;not null"`
	Origin    string `gorm:"size:16;not null"`
	CreatedAt time.Time
}

// TableName pins the source table name.
func (MailMessageSourcePO) TableName() string { return "mail_message_sources" }

// createMessageSource stores a message's source inside the transaction that stores the message.
func createMessageSource(tx *gorm.DB, messageID uint, raw []byte, origin string, at time.Time) error {
	sum := sha256.Sum256(raw)
	return tx.Create(&MailMessageSourcePO{
		MessageID: messageID, Raw: raw, Size: len(raw), SHA256: hex.EncodeToString(sum[:]), Origin: origin, CreatedAt: at,
	}).Error
}

// messageSourceStore reads stored sources; the PostgreSQL repository provides it.
type messageSourceStore interface {
	FindMessageSource(ctx context.Context, messageID uint) ([]byte, error)
}

// FindMessageSource returns a message's stored source, or ErrMailMessageSourceNotFound.
func (r *repository) FindMessageSource(ctx context.Context, messageID uint) ([]byte, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailMessageSourcePO
	err := r.conn(ctx).Select("raw").Where("message_id = ?", messageID).Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMailMessageSourceNotFound
	}
	if err != nil {
		return nil, err
	}
	return po.Raw, nil
}

// composeMessageSource rebuilds an RFC 5322 message from the stored fields of a message that has
// no stored source. The result is lossy (no Reply-To, original Date, signatures, or inline image
// references) and says so with X-Zmail-Reconstructed.
func composeMessageSource(message *domain.MailMessage, attachments []domain.MailAttachment) ([]byte, error) {
	files := make([]mailtransport.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		files = append(files, mailtransport.Attachment{
			Filename: attachment.Filename, ContentType: attachment.ContentType, Content: attachment.Content,
		})
	}
	return buildMIMEMessage(mailtransport.Message{
		FromAddress: message.FromAddress, FromName: message.FromName, To: message.To, Cc: message.Cc,
		Subject: message.Subject, TextBody: message.TextBody, HTMLBody: message.HTMLBody,
		InReplyTo: message.InReplyTo, References: message.References, Attachments: files,
		Headers: map[string]string{reconstructedHeader: "1"},
	}, composedMessageID(message), message.CreatedAt)
}

// composedMessageID keeps the message's own Message-ID; a message without one gets a stable id
// derived from its database id, so a mail app sees the same message on every download.
func composedMessageID(message *domain.MailMessage) string {
	switch {
	case message.MessageID != "":
		return message.MessageID
	case message.RFCMessageID != "":
		return message.RFCMessageID
	default:
		return fmt.Sprintf("message-%d@zmail.invalid", message.ID)
	}
}

// messageSource returns what a mail app downloads for a message: its stored source, or a message
// rebuilt from its fields and attachments.
func (s *service) messageSource(ctx context.Context, message *domain.MailMessage) ([]byte, error) {
	if store, ok := s.repo.(messageSourceStore); ok {
		raw, err := store.FindMessageSource(ctx, message.ID)
		if err == nil {
			return raw, nil
		}
		if !errors.Is(err, ErrMailMessageSourceNotFound) {
			return nil, err
		}
	}
	var attachments []domain.MailAttachment
	if message.AttachmentCount > 0 {
		listed, err := s.repo.ListMessageAttachments(ctx, message.ID)
		if err != nil {
			return nil, err
		}
		for _, item := range listed {
			file, err := s.repo.FindMessageAttachment(ctx, message.ID, item.ID)
			if err != nil {
				return nil, err
			}
			attachments = append(attachments, *file)
		}
	}
	return composeMessageSource(message, attachments)
}
