package broadcast

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// SendLimitWindow is the rolling window used for outbound send limits.
	SendLimitWindow = 24 * time.Hour

	// RateWindow is how far back the send rate of a broadcast is measured.
	RateWindow = 10 * time.Minute

	// StopReasonTestWave records that wave 1 finished and wave 2 is waiting out its window.
	StopReasonTestWave = "test_wave_complete"

	// StopReasonOwner records that an organization owner manually paused the broadcast.
	StopReasonOwner = "paused_by_owner"

	// FailurePermanentBounce is the failure code of a message the provider refused for good.
	FailurePermanentBounce = "transport.permanent_bounce"

	// TemplateKeyIndex is the unique index on (organization_id, template_key).
	TemplateKeyIndex = "mail_templates_key_idx"

	// ImportBatch bounds the rows of one statement during a contact import.
	ImportBatch = 500
)

// UnsealedBroadcast keeps the mail_broadcasts rows created at or after their sender mailbox's seal
// boundary.
const UnsealedBroadcast = `NOT EXISTS (SELECT 1 FROM mailboxes AS seal WHERE seal.id = mail_broadcasts.mailbox_id
	AND seal.sealed_before IS NOT NULL AND mail_broadcasts.created_at < seal.sealed_before)`

// BroadcastNoticeState is a broadcast's state in notice terms; keep it in step with the backfill of
// migration 2026_12_20_090000_add_broadcast_notices.
const BroadcastNoticeState = `CASE
	WHEN status = 'sent' THEN 'sent'
	WHEN status = 'canceled' AND stop_reason = 'all_recipients_failed' THEN 'failed'
	WHEN status = 'paused' AND stop_reason IN ('bounce_rate_high', 'complaint_rate_high') THEN stop_reason
	WHEN status = 'sending' AND stop_reason = 'send_limit_reached' THEN 'send_limit_reached'
	ELSE '' END`

// MailTemplatePO is the persistent object for one template.
type MailTemplatePO struct {
	ID              uint   `gorm:"primaryKey"`
	OrganizationID  uint   `gorm:"not null;uniqueIndex:idx_mail_templates_name,priority:1;uniqueIndex:mail_templates_key_idx,priority:1,where:template_key IS NOT NULL"`
	Name            string `gorm:"size:100;not null;uniqueIndex:idx_mail_templates_name,priority:2"`
	Subject         string `gorm:"size:998;not null"`
	HTMLBody        string `gorm:"column:html_body;type:text;not null;default:''"`
	TextBody        string `gorm:"type:text;not null;default:''"`
	SampleVariables string `gorm:"type:jsonb;not null;default:'{}'"`
	// Design is NULL for a template written in HTML.
	Design *string `gorm:"type:jsonb"`
	Kind   string  `gorm:"size:16;not null;default:'standard'"`
	// TemplateKey and FromAddress are NULL for a standard template.
	TemplateKey         *string `gorm:"column:template_key;size:64;uniqueIndex:mail_templates_key_idx,priority:2"`
	FromAddress         *string `gorm:"size:320"`
	FromName            string  `gorm:"size:100;not null;default:''"`
	VariableDefinitions string  `gorm:"type:jsonb;not null;default:'[]'"`
	CreatedBy           uint    `gorm:"not null"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// variableDefinitionPO is one stored variable declaration of a transactional template.
type variableDefinitionPO struct {
	Name     string `json:"name"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
}

func (MailTemplatePO) TableName() string {
	return "mail_templates"
}

func (po *MailTemplatePO) toDomain() *domain.MailTemplate {
	samples := map[string]string{}
	if po.SampleVariables != "" {
		if err := json.Unmarshal([]byte(po.SampleVariables), &samples); err != nil {
			samples = map[string]string{}
		}
	}
	var stored []variableDefinitionPO
	if po.VariableDefinitions != "" {
		if err := json.Unmarshal([]byte(po.VariableDefinitions), &stored); err != nil {
			stored = nil
		}
	}
	variables := make([]domain.TransactionalVariable, 0, len(stored))
	for _, variable := range stored {
		variables = append(variables, domain.TransactionalVariable(variable))
	}
	kind := domain.MailTemplateKind(po.Kind)
	if kind == "" {
		kind = domain.MailTemplateStandard
	}
	return &domain.MailTemplate{
		ID:              po.ID,
		OrganizationID:  po.OrganizationID,
		Name:            po.Name,
		Subject:         po.Subject,
		HTMLBody:        po.HTMLBody,
		TextBody:        po.TextBody,
		SampleVariables: samples,
		Design:          stringOrEmpty(po.Design),
		Kind:            kind,
		Key:             stringOrEmpty(po.TemplateKey),
		FromAddress:     stringOrEmpty(po.FromAddress),
		FromName:        po.FromName,
		Variables:       variables,
		CreatedBy:       po.CreatedBy,
		CreatedAt:       po.CreatedAt,
		UpdatedAt:       po.UpdatedAt,
	}
}

func newMailTemplatePO(template *domain.MailTemplate) *MailTemplatePO {
	samples := template.SampleVariables
	if samples == nil {
		samples = map[string]string{}
	}
	encoded, err := json.Marshal(samples)
	if err != nil {
		encoded = []byte("{}")
	}
	kind := template.Kind
	if kind == "" {
		kind = domain.MailTemplateStandard
	}
	stored := make([]variableDefinitionPO, 0, len(template.Variables))
	for _, variable := range template.Variables {
		stored = append(stored, variableDefinitionPO(variable))
	}
	definitions, err := json.Marshal(stored)
	if err != nil {
		definitions = []byte("[]")
	}
	return &MailTemplatePO{
		ID:                  template.ID,
		OrganizationID:      template.OrganizationID,
		Name:                template.Name,
		Subject:             template.Subject,
		HTMLBody:            template.HTMLBody,
		TextBody:            template.TextBody,
		SampleVariables:     string(encoded),
		Design:              nullableString(template.Design),
		Kind:                string(kind),
		TemplateKey:         nullableString(template.Key),
		FromAddress:         nullableString(template.FromAddress),
		FromName:            template.FromName,
		VariableDefinitions: string(definitions),
		CreatedBy:           template.CreatedBy,
	}
}

// MailContactPO is the persistent object for one contact.
type MailContactPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;uniqueIndex:idx_mail_contacts_email,priority:1;index:idx_mail_contacts_listing,priority:1"`
	Email          string `gorm:"size:320;not null;uniqueIndex:idx_mail_contacts_email,priority:2"`
	FirstName      string `gorm:"size:100;not null;default:''"`
	LastName       string `gorm:"size:100;not null;default:''"`
	Properties     string `gorm:"type:jsonb;not null;default:'{}'"`
	Status         string `gorm:"size:16;not null;default:'subscribed'"`
	StatusReason   string `gorm:"size:64;not null;default:''"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailContactPO) TableName() string {
	return "mail_contacts"
}

func (po *MailContactPO) toDomain() *domain.MailContact {
	properties := decodeProperties(po.Properties)
	return &domain.MailContact{
		ID:             po.ID,
		OrganizationID: po.OrganizationID,
		Email:          po.Email,
		FirstName:      po.FirstName,
		LastName:       po.LastName,
		Properties:     properties,
		Status:         domain.MailContactStatus(po.Status),
		StatusReason:   po.StatusReason,
		ListIDs:        []uint{},
		CreatedAt:      po.CreatedAt,
		UpdatedAt:      po.UpdatedAt,
	}
}

// MailContactListPO is the persistent object for one list.
type MailContactListPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;uniqueIndex:idx_mail_contact_lists_name,priority:1"`
	Name           string `gorm:"size:100;not null;uniqueIndex:idx_mail_contact_lists_name,priority:2"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailContactListPO) TableName() string {
	return "mail_contact_lists"
}

// MailContactListMemberPO puts one contact in one list.
type MailContactListMemberPO struct {
	ListID    uint `gorm:"primaryKey;autoIncrement:false"`
	ContactID uint `gorm:"primaryKey;autoIncrement:false;index:idx_mail_contact_list_members_contact"`
	CreatedAt time.Time
}

func (MailContactListMemberPO) TableName() string {
	return "mail_contact_list_members"
}

// MailBroadcastPO is the persistent object for one broadcast.
type MailBroadcastPO struct {
	ID               uint       `gorm:"primaryKey"`
	OrganizationID   uint       `gorm:"not null;index:idx_mail_broadcasts_listing,priority:1"`
	Name             string     `gorm:"size:100;not null"`
	MailboxID        uint       `gorm:"not null"`
	TemplateID       uint       `gorm:"not null"`
	ListID           uint       `gorm:"not null"`
	UnsubscribeLabel string     `gorm:"size:50;not null;default:''"`
	Variables        string     `gorm:"type:jsonb;not null;default:'{}'"`
	Status           string     `gorm:"size:16;not null;default:'draft';index:idx_mail_broadcasts_due,priority:1"`
	Subject          string     `gorm:"size:998;not null;default:''"`
	HTMLBody         string     `gorm:"column:html_body;type:text;not null;default:''"`
	TextBody         string     `gorm:"type:text;not null;default:''"`
	ScheduledAt      *time.Time `gorm:"index:idx_mail_broadcasts_due,priority:2"`
	StartedAt        *time.Time
	FinishedAt       *time.Time
	StopReason       string `gorm:"size:64;not null;default:''"`
	TestPercent      int    `gorm:"type:smallint;not null;default:0"`
	TestWaitMinutes  int    `gorm:"not null;default:30"`
	CurrentWave      int    `gorm:"type:smallint;not null;default:1"`
	ResumeAt         *time.Time
	SafetyOverride   bool `gorm:"not null;default:false"`
	CreatedBy        uint `gorm:"not null"`
	// StartedBy is who sent or scheduled the broadcast; null for a draft.
	StartedBy *uint
	// NoticeState is the last state its starter was told about; NoticeCount counts the notices.
	NoticeState string `gorm:"size:32;not null;default:''"`
	NoticeCount int    `gorm:"not null;default:0"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (MailBroadcastPO) TableName() string {
	return "mail_broadcasts"
}

func (po *MailBroadcastPO) toDomain() *domain.MailBroadcast {
	return &domain.MailBroadcast{
		ID:               po.ID,
		OrganizationID:   po.OrganizationID,
		Name:             po.Name,
		MailboxID:        po.MailboxID,
		TemplateID:       po.TemplateID,
		ListID:           po.ListID,
		UnsubscribeLabel: po.UnsubscribeLabel,
		Variables:        decodeProperties(po.Variables),
		Status:           domain.MailBroadcastStatus(po.Status),
		Subject:          po.Subject,
		HTMLBody:         po.HTMLBody,
		TextBody:         po.TextBody,
		ScheduledAt:      po.ScheduledAt,
		StartedAt:        po.StartedAt,
		FinishedAt:       po.FinishedAt,
		StopReason:       po.StopReason,
		TestPercent:      po.TestPercent,
		TestWaitMinutes:  po.TestWaitMinutes,
		CurrentWave:      po.CurrentWave,
		ResumeAt:         po.ResumeAt,
		SafetyOverride:   po.SafetyOverride,
		CreatedBy:        po.CreatedBy,
		StartedBy:        derefUint(po.StartedBy),
		CreatedAt:        po.CreatedAt,
		UpdatedAt:        po.UpdatedAt,
	}
}

// MailBroadcastRecipientPO is the persistent object for one recipient of a broadcast.
type MailBroadcastRecipientPO struct {
	ID          uint   `gorm:"primaryKey"`
	BroadcastID uint   `gorm:"not null;uniqueIndex:idx_mail_broadcast_recipients_contact,priority:1"`
	ContactID   uint   `gorm:"not null;uniqueIndex:idx_mail_broadcast_recipients_contact,priority:2"`
	Email       string `gorm:"size:320;not null"`
	Status      string `gorm:"size:16;not null;default:'pending'"`
	MessageID   *uint
	FailureCode string `gorm:"size:120;not null;default:''"`
	Wave        int    `gorm:"type:smallint;not null;default:1"`
	LeaseUntil  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (MailBroadcastRecipientPO) TableName() string {
	return "mail_broadcast_recipients"
}

func (po *MailBroadcastRecipientPO) toDomain() *domain.MailBroadcastRecipient {
	recipient := &domain.MailBroadcastRecipient{
		ID:          po.ID,
		BroadcastID: po.BroadcastID,
		ContactID:   po.ContactID,
		Email:       po.Email,
		Status:      domain.MailRecipientStatus(po.Status),
		FailureCode: po.FailureCode,
		UpdatedAt:   po.UpdatedAt,
	}
	if po.MessageID != nil {
		recipient.MessageID = *po.MessageID
	}
	return recipient
}

func decodeProperties(encoded string) map[string]string {
	properties := map[string]string{}
	if encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &properties); err != nil || properties == nil {
			properties = map[string]string{}
		}
	}
	return properties
}

func encodeProperties(properties map[string]string) string {
	if properties == nil {
		properties = map[string]string{}
	}
	encoded, err := json.Marshal(properties)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func derefUint(value *uint) uint {
	if value == nil {
		return 0
	}
	return *value
}

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

func cleanText(value string) string {
	value = strings.ToValidUTF8(value, "\ufffd")
	return strings.ReplaceAll(value, "\x00", "")
}
