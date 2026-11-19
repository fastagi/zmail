package mailbox

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// TransactionalMessagePO is the persistent object for one transactional message. The schema is
// owned by the 2026_11_23_110000_create_transactional_messages migration.
type TransactionalMessagePO struct {
	ID                     uint `gorm:"primaryKey"`
	OrganizationID         uint
	Mode                   string
	Simulated              bool
	APIKeyID               *uint `gorm:"column:api_key_id"`
	CreatedBy              uint
	TemplateID             *uint
	TemplateKey            string
	FromAddress            string
	FromName               string
	ToAddress              string
	SubjectMasked          string
	VariablesMasked        string `gorm:"type:jsonb"`
	PayloadCiphertext      []byte `gorm:"type:bytea"`
	IdempotencyKey         *string
	IdempotencyFingerprint []byte `gorm:"type:bytea"`
	Status                 string
	QueuedReason           *string
	Attempts               int
	NextAttemptAt          *time.Time
	LeaseUntil             *time.Time
	SendAttemptedAt        *time.Time
	ProviderMessageID      *string
	RFCMessageID           string `gorm:"column:rfc_message_id"`
	FailureCode            string
	LastEvent              *string
	SentAt                 *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (TransactionalMessagePO) TableName() string {
	return "transactional_messages"
}

func transactionalMessageToPO(message *domain.TransactionalMessage) (*TransactionalMessagePO, error) {
	variables := message.VariablesMasked
	if variables == nil {
		variables = map[string]string{}
	}
	encoded, err := json.Marshal(variables)
	if err != nil {
		return nil, err
	}
	po := &TransactionalMessagePO{
		ID:                     message.ID,
		OrganizationID:         message.OrganizationID,
		Mode:                   string(message.Mode),
		Simulated:              message.Simulated,
		APIKeyID:               message.APIKeyID,
		CreatedBy:              message.CreatedBy,
		TemplateID:             message.TemplateID,
		TemplateKey:            message.TemplateKey,
		FromAddress:            message.FromAddress,
		FromName:               message.FromName,
		ToAddress:              strings.ToLower(strings.TrimSpace(message.ToAddress)),
		SubjectMasked:          message.SubjectMasked,
		VariablesMasked:        string(encoded),
		PayloadCiphertext:      message.PayloadCiphertext,
		IdempotencyKey:         message.IdempotencyKey,
		IdempotencyFingerprint: message.IdempotencyFingerprint,
		Status:                 string(message.Status),
		Attempts:               message.Attempts,
		NextAttemptAt:          pgTimePointer(message.NextAttemptAt),
		LeaseUntil:             pgTimePointer(message.LeaseUntil),
		SendAttemptedAt:        pgTimePointer(message.SendAttemptedAt),
		ProviderMessageID:      message.ProviderMessageID,
		RFCMessageID:           message.RFCMessageID,
		FailureCode:            message.FailureCode,
		SentAt:                 pgTimePointer(message.SentAt),
		CreatedAt:              message.CreatedAt,
		UpdatedAt:              message.UpdatedAt,
	}
	if po.IdempotencyFingerprint == nil {
		po.IdempotencyFingerprint = []byte{}
	}
	if message.QueuedReason != nil {
		reason := string(*message.QueuedReason)
		po.QueuedReason = &reason
	}
	if message.LastEvent != "" {
		event := string(message.LastEvent)
		po.LastEvent = &event
	}
	return po, nil
}

func (po *TransactionalMessagePO) toDomain() *domain.TransactionalMessage {
	message := &domain.TransactionalMessage{
		ID:                     po.ID,
		OrganizationID:         po.OrganizationID,
		Mode:                   domain.APIKeyMode(po.Mode),
		Simulated:              po.Simulated,
		APIKeyID:               po.APIKeyID,
		CreatedBy:              po.CreatedBy,
		TemplateID:             po.TemplateID,
		TemplateKey:            po.TemplateKey,
		FromAddress:            po.FromAddress,
		FromName:               po.FromName,
		ToAddress:              po.ToAddress,
		SubjectMasked:          po.SubjectMasked,
		VariablesMasked:        map[string]string{},
		PayloadCiphertext:      po.PayloadCiphertext,
		IdempotencyKey:         po.IdempotencyKey,
		IdempotencyFingerprint: po.IdempotencyFingerprint,
		Status:                 domain.TransactionalStatus(po.Status),
		Attempts:               po.Attempts,
		NextAttemptAt:          po.NextAttemptAt,
		LeaseUntil:             po.LeaseUntil,
		SendAttemptedAt:        po.SendAttemptedAt,
		SentAt:                 po.SentAt,
		ProviderMessageID:      po.ProviderMessageID,
		RFCMessageID:           po.RFCMessageID,
		FailureCode:            po.FailureCode,
		CreatedAt:              po.CreatedAt,
		UpdatedAt:              po.UpdatedAt,
	}
	if po.VariablesMasked != "" {
		// A stored value always decodes; a corrupt one shows no variables rather than failing the read.
		if err := json.Unmarshal([]byte(po.VariablesMasked), &message.VariablesMasked); err != nil {
			message.VariablesMasked = map[string]string{}
		}
	}
	if po.QueuedReason != nil {
		reason := domain.TransactionalQueuedReason(*po.QueuedReason)
		message.QueuedReason = &reason
	}
	if po.LastEvent != nil {
		message.LastEvent = domain.MailEventType(*po.LastEvent)
	}
	return message
}

// TransactionalMessageEventPO is the persistent object for one delivery event of a
// transactional message. It has no recipient: the message has exactly one.
type TransactionalMessageEventPO struct {
	ID              uint `gorm:"primaryKey"`
	MessageID       uint
	Type            string
	Detail          string
	ProviderEventID *string
	OccurredAt      time.Time
	CreatedAt       time.Time
}

func (TransactionalMessageEventPO) TableName() string {
	return "transactional_message_events"
}

func (po *TransactionalMessageEventPO) toDomain() domain.MailEvent {
	event := domain.MailEvent{
		ID:         po.ID,
		MessageID:  po.MessageID,
		Type:       domain.MailEventType(po.Type),
		Detail:     po.Detail,
		OccurredAt: po.OccurredAt,
	}
	if po.ProviderEventID != nil {
		event.ProviderEventID = *po.ProviderEventID
	}
	return event
}

// MailSuppressionPO is the persistent object for one suppressed address of an organization.
type MailSuppressionPO struct {
	ID             uint `gorm:"primaryKey"`
	OrganizationID uint
	Email          string
	Reason         string
	Source         string
	CreatedAt      time.Time
}

func (MailSuppressionPO) TableName() string {
	return "mail_suppressions"
}

func (po *MailSuppressionPO) toDomain() domain.MailSuppression {
	return domain.MailSuppression{
		ID:             po.ID,
		OrganizationID: po.OrganizationID,
		Email:          po.Email,
		Reason:         po.Reason,
		Source:         po.Source,
		CreatedAt:      po.CreatedAt,
	}
}

// pgTime drops what PostgreSQL cannot store, so a time written and later compared for equality
// (a lease) is the same value on both sides.
func pgTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}

func pgTimePointer(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	value := pgTime(*t)
	return &value
}

// normalizeEmail is the form addresses are stored and matched in.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
