package mailbox

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailboxPO is the persistent object for a mailbox.
type MailboxPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID *uint  `gorm:"index:idx_mailboxes_organization"`
	CreatedBy      uint   `gorm:"not null"`
	Address        string `gorm:"size:320;not null;uniqueIndex:idx_mailboxes_address"`
	DisplayName    string `gorm:"size:120;not null;default:''"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      gorm.DeletedAt
}

func (MailboxPO) TableName() string {
	return "mailboxes"
}

func (po *MailboxPO) toDomain() *domain.Mailbox {
	if po == nil {
		return nil
	}
	return &domain.Mailbox{
		ID:             po.ID,
		OrganizationID: organizationIDOf(po),
		CreatedBy:      po.CreatedBy,
		Address:        po.Address,
		DisplayName:    po.DisplayName,
		CreatedAt:      po.CreatedAt,
		UpdatedAt:      po.UpdatedAt,
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
	LastEvent              string  `gorm:"size:16;not null;default:''"`
	AttachmentCount        int     `gorm:"not null;default:0"`
	TemplateID             *uint
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
	message.LastEvent = domain.MailEventType(po.LastEvent)
	message.AttachmentCount = po.AttachmentCount
	if po.TemplateID != nil {
		message.TemplateID = *po.TemplateID
	}
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
	po.AttachmentCount = len(message.Attachments)
	if message.TemplateID != 0 {
		templateID := message.TemplateID
		po.TemplateID = &templateID
	}
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

func organizationIDOf(po *MailboxPO) uint {
	if po.OrganizationID == nil {
		return 0
	}
	return *po.OrganizationID
}

// MailMessageEventPO is the persistent object for one delivery event.
type MailMessageEventPO struct {
	ID              uint      `gorm:"primaryKey"`
	MessageID       uint      `gorm:"not null;index:idx_mail_message_events_message,priority:1"`
	Type            string    `gorm:"size:16;not null"`
	Recipient       string    `gorm:"size:320;not null;default:''"`
	Detail          string    `gorm:"size:255;not null;default:''"`
	ProviderEventID *string   `gorm:"size:128;uniqueIndex:idx_mail_message_events_provider,where:provider_event_id IS NOT NULL"`
	OccurredAt      time.Time `gorm:"not null;index:idx_mail_message_events_message,priority:2"`
	CreatedAt       time.Time
}

func (MailMessageEventPO) TableName() string {
	return "mail_message_events"
}

func (po *MailMessageEventPO) toDomain() domain.MailEvent {
	event := domain.MailEvent{
		ID:         po.ID,
		MessageID:  po.MessageID,
		Type:       domain.MailEventType(po.Type),
		Recipient:  po.Recipient,
		Detail:     po.Detail,
		OccurredAt: po.OccurredAt,
	}
	if po.ProviderEventID != nil {
		event.ProviderEventID = *po.ProviderEventID
	}
	return event
}

// MailAttachmentPO is the persistent object for one attached file. The content lives in the
// row: attachments are small and bounded, and deleting a message removes them with it.
type MailAttachmentPO struct {
	ID          uint   `gorm:"primaryKey"`
	MessageID   uint   `gorm:"not null;index:idx_mail_attachments_message"`
	Filename    string `gorm:"size:255;not null"`
	ContentType string `gorm:"size:127;not null"`
	Size        int    `gorm:"not null"`
	Content     []byte `gorm:"type:bytea;not null"`
	CreatedAt   time.Time
}

func (MailAttachmentPO) TableName() string {
	return "mail_attachments"
}

func (po *MailAttachmentPO) toDomain() domain.MailAttachment {
	return domain.MailAttachment{
		ID:          po.ID,
		MessageID:   po.MessageID,
		Filename:    po.Filename,
		ContentType: po.ContentType,
		Size:        po.Size,
		Content:     po.Content,
	}
}

// MailTemplatePO is the persistent object for one template.
type MailTemplatePO struct {
	ID              uint   `gorm:"primaryKey"`
	OrganizationID  uint   `gorm:"not null;uniqueIndex:idx_mail_templates_name,priority:1"`
	Name            string `gorm:"size:100;not null;uniqueIndex:idx_mail_templates_name,priority:2"`
	Subject         string `gorm:"size:998;not null"`
	HTMLBody        string `gorm:"column:html_body;type:text;not null;default:''"`
	TextBody        string `gorm:"type:text;not null;default:''"`
	SampleVariables string `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedBy       uint   `gorm:"not null"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
	return &domain.MailTemplate{
		ID:              po.ID,
		OrganizationID:  po.OrganizationID,
		Name:            po.Name,
		Subject:         po.Subject,
		HTMLBody:        po.HTMLBody,
		TextBody:        po.TextBody,
		SampleVariables: samples,
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
	return &MailTemplatePO{
		ID:              template.ID,
		OrganizationID:  template.OrganizationID,
		Name:            template.Name,
		Subject:         template.Subject,
		HTMLBody:        template.HTMLBody,
		TextBody:        template.TextBody,
		SampleVariables: string(encoded),
		CreatedBy:       template.CreatedBy,
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
	properties := map[string]string{}
	if po.Properties != "" {
		if err := json.Unmarshal([]byte(po.Properties), &properties); err != nil {
			properties = map[string]string{}
		}
	}
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
