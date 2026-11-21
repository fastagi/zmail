package mailbox

import (
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailboxPO is the persistent object for a mailbox.
type MailboxPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID *uint  `gorm:"index:idx_mailboxes_organization"`
	CreatedBy      uint   `gorm:"not null"`
	Address        string `gorm:"size:320;not null;uniqueIndex:idx_mailboxes_address,where:type <> 'external'"`
	DisplayName    string `gorm:"size:120;not null;default:''"`
	RoutingRuleID  string `gorm:"size:64;not null;default:''"`
	// Type is "personal" or "shared".
	Type            string `gorm:"size:16;not null;default:'shared'"`
	OwnerUserID     *uint
	OwnerLeftAt     *time.Time
	SealedBefore    *time.Time
	WebhooksEnabled bool `gorm:"not null;default:false"`
	// RoleID is the mail role the mailbox is grouped under; nil when it is ungrouped.
	RoleID *uint
	// AITriageEnabled queues the mailbox's inbound mail for AI triage.
	AITriageEnabled bool `gorm:"column:ai_triage_enabled;not null;default:false"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       gorm.DeletedAt
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
		RoutingRuleID:  po.RoutingRuleID,
		Type:           domain.MailboxType(po.Type),
		OwnerUserID:    po.OwnerUserID,
		OwnerLeftAt:    po.OwnerLeftAt,
		SealedBefore:   po.SealedBefore,
		// WebhooksEnabled is only meaningful for shared mailboxes; the schema enforces it too.
		WebhooksEnabled: po.WebhooksEnabled,
		RoleID:          po.RoleID,
		AITriageEnabled: po.AITriageEnabled,
		CreatedAt:       po.CreatedAt,
		UpdatedAt:       po.UpdatedAt,
	}
}

// MailRolePO is an organization's mail role; names are unique per organization ignoring case.
type MailRolePO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;uniqueIndex:idx_mail_roles_name,priority:1"`
	Name           string `gorm:"size:64;not null;uniqueIndex:idx_mail_roles_name,priority:2,expression:lower(name)"`
	Color          string `gorm:"size:16;not null;default:'gray'"`
	Description    string `gorm:"size:280;not null;default:''"`
	OwnerUserID    *uint
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailRolePO) TableName() string {
	return "mail_roles"
}

func (po *MailRolePO) toDomain() *domain.MailRole {
	return &domain.MailRole{
		ID: po.ID, OrganizationID: po.OrganizationID, Name: po.Name, Color: domain.MailLabelColor(po.Color),
		Description: po.Description, OwnerUserID: po.OwnerUserID, CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt,
	}
}

// MailboxMemberPO is one membership's permission on a shared mailbox.
type MailboxMemberPO struct {
	MailboxID    uint   `gorm:"primaryKey;autoIncrement:false"`
	MembershipID uint   `gorm:"primaryKey;autoIncrement:false;index:idx_mailbox_members_membership,priority:1"`
	UserID       uint   `gorm:"not null"`
	Permission   string `gorm:"size:16;not null"`
	GrantedBy    uint   `gorm:"not null"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (MailboxMemberPO) TableName() string {
	return "mailbox_members"
}

func (po *MailboxMemberPO) toDomain() *domain.MailboxMember {
	return &domain.MailboxMember{
		MailboxID:    po.MailboxID,
		MembershipID: po.MembershipID,
		UserID:       po.UserID,
		Permission:   domain.MailboxPermission(po.Permission),
		GrantedBy:    po.GrantedBy,
		CreatedAt:    po.CreatedAt,
	}
}

// MailboxAccessEventPO is one entry of a mailbox's access history.
type MailboxAccessEventPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null"`
	MailboxID      uint   `gorm:"not null;index:idx_mailbox_access_events_mailbox,priority:1"`
	Type           string `gorm:"size:32;not null"`
	ActorUserID    *uint
	SubjectUserID  *uint
	Detail         string `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt      time.Time
}

func (MailboxAccessEventPO) TableName() string {
	return "mailbox_access_events"
}

func (po *MailboxAccessEventPO) toDomain() *domain.MailboxAccessEvent {
	detail := map[string]any{}
	if po.Detail != "" {
		// The column is jsonb written by this module; an unreadable value shows as empty detail.
		if err := json.Unmarshal([]byte(po.Detail), &detail); err != nil {
			detail = map[string]any{}
		}
	}
	return &domain.MailboxAccessEvent{
		ID:             po.ID,
		OrganizationID: po.OrganizationID,
		MailboxID:      po.MailboxID,
		Type:           domain.MailboxAccessEventType(po.Type),
		ActorUserID:    po.ActorUserID,
		SubjectUserID:  po.SubjectUserID,
		Detail:         detail,
		CreatedAt:      po.CreatedAt,
	}
}

// MailMessagePO is the persistent object for one stored message.
type MailMessagePO struct {
	ID                     uint    `gorm:"primaryKey"`
	MailboxID              uint    `gorm:"not null;index:idx_mail_messages_listing,priority:1;uniqueIndex:idx_mail_messages_dedupe,priority:1,where:dedupe_key IS NOT NULL;uniqueIndex:idx_mail_messages_idempotency,priority:1,where:idempotency_key IS NOT NULL;uniqueIndex:idx_mail_messages_external,priority:1,where:external_id IS NOT NULL"`
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
	// QueuedReason is set while a message waits for room in the send limit.
	QueuedReason *string `gorm:"size:32"`
	// SendAttemptedAt is when the provider call started; the send limit counts by it.
	SendAttemptedAt *time.Time
	// SendLane is the send limit lane the message counts against: personal or broadcast.
	SendLane string `gorm:"size:16;not null;default:'personal'"`
	// SendLeaseUntil keeps a waiting message to one worker while it is being sent.
	SendLeaseUntil *time.Time
	// Placement is default, archive, folder, or trash; FolderID names the folder while in one.
	Placement string `gorm:"size:16;not null;default:'default'"`
	FolderID  *uint
	TrashedAt *time.Time
	// ThreadID is the id of the message that started the thread. Nullable only until every
	// insert path sets it; the backfill migration threads the rows stored before.
	ThreadID *uint
	// ReferencesHeader holds the References Message-IDs, oldest first, separated by spaces.
	ReferencesHeader string `gorm:"type:text;not null;default:''"`
	// RFCMessageID is the RFC 5322 Message-ID threading matches In-Reply-To against.
	RFCMessageID string `gorm:"column:rfc_message_id;size:998;not null;default:''"`
	// Source is zmail, gmail, or outlook; ExternalID is the provider's id of a synced copy.
	Source     string  `gorm:"size:16;not null;default:'zmail'"`
	ExternalID *string `gorm:"size:255;uniqueIndex:idx_mail_messages_external,priority:2,where:external_id IS NOT NULL"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (MailMessagePO) TableName() string {
	return "mail_messages"
}

// MailMessageReadPO records that one organization membership has read one message. The schema
// cascades it with the membership and the message.
type MailMessageReadPO struct {
	MembershipID uint `gorm:"primaryKey;autoIncrement:false"`
	MessageID    uint `gorm:"primaryKey;autoIncrement:false"`
	ReadAt       time.Time
}

func (MailMessageReadPO) TableName() string {
	return "mail_message_reads"
}

// MailMessageStarPO records that one organization membership starred one message. Like a read
// row, the schema cascades it with the membership and the message.
type MailMessageStarPO struct {
	MembershipID uint `gorm:"primaryKey;autoIncrement:false"`
	MessageID    uint `gorm:"primaryKey;autoIncrement:false"`
	StarredAt    time.Time
}

func (MailMessageStarPO) TableName() string {
	return "mail_message_stars"
}

// MailMessageLabelPO puts one label on one message. The schema cascades it with both.
type MailMessageLabelPO struct {
	MessageID uint `gorm:"primaryKey;autoIncrement:false"`
	LabelID   uint `gorm:"primaryKey;autoIncrement:false"`
}

func (MailMessageLabelPO) TableName() string {
	return "mail_message_labels"
}

// MailFolderPO is a folder of a mailbox; names are unique per mailbox ignoring case.
type MailFolderPO struct {
	ID        uint   `gorm:"primaryKey"`
	MailboxID uint   `gorm:"not null;uniqueIndex:idx_mail_folders_name,priority:1"`
	Name      string `gorm:"size:64;not null;uniqueIndex:idx_mail_folders_name,priority:2,expression:lower(name)"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (MailFolderPO) TableName() string {
	return "mail_folders"
}

func (po *MailFolderPO) toDomain() *domain.MailFolder {
	return &domain.MailFolder{ID: po.ID, MailboxID: po.MailboxID, Name: po.Name, CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt}
}

// MailLabelPO is a colored label of a mailbox; names are unique per mailbox ignoring case.
type MailLabelPO struct {
	ID        uint   `gorm:"primaryKey"`
	MailboxID uint   `gorm:"not null;uniqueIndex:idx_mail_labels_name,priority:1"`
	Name      string `gorm:"size:64;not null;uniqueIndex:idx_mail_labels_name,priority:2,expression:lower(name)"`
	Color     string `gorm:"size:16;not null;default:'gray'"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (MailLabelPO) TableName() string {
	return "mail_labels"
}

func (po *MailLabelPO) toDomain() *domain.MailLabel {
	return &domain.MailLabel{
		ID: po.ID, MailboxID: po.MailboxID, Name: po.Name, Color: domain.MailLabelColor(po.Color),
		CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt,
	}
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
		// Outbound mail is read for everyone; inbound read state is the viewer's, filled by the service.
		Read:      po.Direction == string(domain.MailDirectionOutbound),
		CreatedAt: po.CreatedAt,
		UpdatedAt: po.UpdatedAt,
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
	if po.QueuedReason != nil {
		message.QueuedReason = *po.QueuedReason
	}
	message.SendAttemptedAt = po.SendAttemptedAt
	message.SendLane = domain.SendLane(po.SendLane)
	if message.SendLane == "" {
		message.SendLane = domain.SendLanePersonal
	}
	message.Placement = domain.MailPlacement(po.Placement)
	if message.Placement == "" {
		message.Placement = domain.MailPlacementDefault
	}
	message.FolderID = po.FolderID
	message.TrashedAt = po.TrashedAt
	if po.ThreadID != nil {
		message.ThreadID = *po.ThreadID
	}
	message.References = strings.Fields(po.ReferencesHeader)
	message.RFCMessageID = po.RFCMessageID
	message.Source = domain.MailSource(po.Source)
	if message.Source == "" {
		message.Source = domain.MailSourceZmail
	}
	if po.ExternalID != nil {
		message.ExternalID = *po.ExternalID
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
	if message.QueuedReason != "" {
		reason := message.QueuedReason
		po.QueuedReason = &reason
	}
	po.SendAttemptedAt = message.SendAttemptedAt
	po.SendLane = string(message.SendLane)
	if po.SendLane == "" {
		po.SendLane = string(domain.SendLanePersonal)
	}
	po.Placement = string(message.Placement)
	if po.Placement == "" {
		po.Placement = string(domain.MailPlacementDefault)
	}
	po.FolderID = message.FolderID
	po.TrashedAt = message.TrashedAt
	if message.ThreadID != 0 {
		threadID := message.ThreadID
		po.ThreadID = &threadID
	}
	po.ReferencesHeader = strings.Join(message.References, " ")
	po.RFCMessageID = message.RFCMessageID
	po.Source = string(message.Source)
	if po.Source == "" {
		po.Source = string(domain.MailSourceZmail)
	}
	if message.ExternalID != "" {
		externalID := message.ExternalID
		po.ExternalID = &externalID
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

// decodeProperties reads a stored jsonb object of strings; anything else reads as empty.
func decodeProperties(encoded string) map[string]string {
	properties := map[string]string{}
	if encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &properties); err != nil || properties == nil {
			properties = map[string]string{}
		}
	}
	return properties
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
	CreatedAt        time.Time
	UpdatedAt        time.Time
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
