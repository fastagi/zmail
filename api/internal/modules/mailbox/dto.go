package mailbox

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// CreateMailboxRequest asks for a new address on the shared domain, or on Domain when it names a
// verified domain of the organization. Type, owner, and members are validated by the service, so
// every access mistake answers MAILBOX.ACCESS_INVALID.
type CreateMailboxRequest struct {
	LocalPart   string `json:"local_part" binding:"required,max=64"`
	DisplayName string `json:"display_name" binding:"max=120"`
	Domain      string `json:"domain" binding:"max=253"`
	// Type is "personal" or "shared".
	Type string `json:"type" binding:"max=16"`
	// OwnerUserID is required for a personal mailbox and must be absent for a shared one.
	OwnerUserID uint `json:"owner_user_id"`
	// Members are required for a shared mailbox: 1 to 100, each user once.
	Members []MailboxMemberRequest `json:"members" binding:"omitempty,dive"`
}

// MailboxMemberRequest names one member of a new shared mailbox.
type MailboxMemberRequest struct {
	UserID     uint   `json:"user_id"`
	Permission string `json:"permission" binding:"max=16"`
}

func (r CreateMailboxRequest) input() CreateMailboxInput {
	members := make([]MailboxMemberInput, 0, len(r.Members))
	for _, member := range r.Members {
		members = append(members, MailboxMemberInput{UserID: member.UserID, Permission: domain.MailboxPermission(member.Permission)})
	}
	return CreateMailboxInput{
		LocalPart:   r.LocalPart,
		DisplayName: r.DisplayName,
		Domain:      r.Domain,
		Type:        domain.MailboxType(r.Type),
		OwnerUserID: r.OwnerUserID,
		Members:     members,
	}
}

// SetMailboxWebhookSettingRequest turns a shared mailbox's mail webhooks on or off.
type SetMailboxWebhookSettingRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// HandOverMailboxRequest names the successor of a personal mailbox.
type HandOverMailboxRequest struct {
	SuccessorUserID uint `json:"successor_user_id" binding:"required"`
}

// DeleteMailboxSealedMessagesRequest confirms deleting a mailbox's sealed history with its address.
type DeleteMailboxSealedMessagesRequest struct {
	ConfirmAddress string `json:"confirm_address" binding:"required,max=320"`
}

// MailboxSealedDeletionResponse counts what deleting the sealed history removed.
type MailboxSealedDeletionResponse struct {
	DeletedMessages   int64 `json:"deleted_messages"`
	DeletedBroadcasts int64 `json:"deleted_broadcasts"`
}

// SetMailboxMemberRequest sets one member's permission.
type SetMailboxMemberRequest struct {
	Permission string `json:"permission" binding:"required,max=16"`
}

// MailboxMemberResponse is one member of a shared mailbox.
type MailboxMemberResponse struct {
	UserID     uint      `json:"user_id"`
	Permission string    `json:"permission"`
	GrantedBy  uint      `json:"granted_by"`
	CreatedAt  time.Time `json:"created_at"`
}

func toMailboxMemberResponse(member *domain.MailboxMember) MailboxMemberResponse {
	return MailboxMemberResponse{
		UserID:     member.UserID,
		Permission: string(member.Permission),
		GrantedBy:  member.GrantedBy,
		CreatedAt:  member.CreatedAt,
	}
}

// MailboxAccessEventResponse is one entry of a mailbox's access history.
type MailboxAccessEventResponse struct {
	ID            uint           `json:"id"`
	Type          string         `json:"type"`
	ActorUserID   *uint          `json:"actor_user_id"`
	SubjectUserID *uint          `json:"subject_user_id"`
	Detail        map[string]any `json:"detail"`
	CreatedAt     time.Time      `json:"created_at"`
}

func toMailboxAccessEventResponses(events []*domain.MailboxAccessEvent) []MailboxAccessEventResponse {
	items := make([]MailboxAccessEventResponse, 0, len(events))
	for _, event := range events {
		detail := event.Detail
		if detail == nil {
			detail = map[string]any{}
		}
		items = append(items, MailboxAccessEventResponse{
			ID:            event.ID,
			Type:          string(event.Type),
			ActorUserID:   event.ActorUserID,
			SubjectUserID: event.SubjectUserID,
			Detail:        detail,
			CreatedAt:     event.CreatedAt,
		})
	}
	return items
}

// SendMailMessageRequest asks to send one message from a mailbox.
type SendMailMessageRequest struct {
	To []string `json:"to" binding:"required,min=1,max=50,dive,required,max=320"`
	Cc []string `json:"cc" binding:"omitempty,max=50,dive,required,max=320"`
	// Subject, Text, and HTML are required unless Template is given; the service decides.
	Subject   string `json:"subject" binding:"max=998"`
	Text      string `json:"text" binding:"max=262144"`
	HTML      string `json:"html" binding:"max=262144"`
	InReplyTo string `json:"in_reply_to" binding:"max=900"`
	// Attachments are optional files; content is standard base64. The decoded total is bounded
	// by the service.
	Attachments []MailAttachmentRequest `json:"attachments" binding:"omitempty,max=10,dive"`
	// Template sends a stored template in place of Subject, Text, and HTML.
	Template *MailTemplateSendRequest `json:"template"`
}

// MailAttachmentRequest is one file to attach to an outbound message.
type MailAttachmentRequest struct {
	Filename    string `json:"filename" binding:"required,max=255"`
	ContentType string `json:"content_type" binding:"max=127"`
	Content     string `json:"content" binding:"required"`
}

// SendOutboundMailRequest asks to send one message with an API key. From names one of the key
// owner's mailboxes.
type SendOutboundMailRequest struct {
	From string `json:"from" binding:"required,max=320"`
	SendMailMessageRequest
}

func (r SendMailMessageRequest) input(idempotencyKey string) SendInput {
	attachments := make([]AttachmentInput, 0, len(r.Attachments))
	for _, attachment := range r.Attachments {
		attachments = append(attachments, AttachmentInput(attachment))
	}
	var template *TemplateSendInput
	if r.Template != nil {
		template = &TemplateSendInput{ID: r.Template.ID, Variables: r.Template.Variables}
	}
	return SendInput{
		Template:       template,
		Attachments:    attachments,
		To:             r.To,
		Cc:             r.Cc,
		Subject:        r.Subject,
		Text:           r.Text,
		HTML:           r.HTML,
		InReplyTo:      r.InReplyTo,
		IdempotencyKey: idempotencyKey,
	}
}

// UpdateMailMessageRequest marks, stars, moves, or labels one message. Every field is optional;
// the service checks access and the referenced folder and labels before it validates the rules,
// so binding enforces only JSON types.
type UpdateMailMessageRequest struct {
	Read      *bool   `json:"read"`
	Starred   *bool   `json:"starred"`
	Placement *string `json:"placement"`
	FolderID  *uint   `json:"folder_id"`
	// LabelIDs replaces every label of the message.
	LabelIDs *[]uint `json:"label_ids"`
}

func (r UpdateMailMessageRequest) update() MessageUpdate {
	return MessageUpdate{
		Read: r.Read, Starred: r.Starred, Placement: placementPointer(r.Placement), FolderID: r.FolderID, SetLabelIDs: r.LabelIDs,
	}
}

// UpdateMailMessagesRequest applies one change to up to 100 messages. Like
// UpdateMailMessageRequest, its rules are checked by the service.
type UpdateMailMessagesRequest struct {
	MessageIDs     []uint  `json:"message_ids"`
	Read           *bool   `json:"read"`
	Starred        *bool   `json:"starred"`
	Placement      *string `json:"placement"`
	FolderID       *uint   `json:"folder_id"`
	AddLabelIDs    []uint  `json:"add_label_ids"`
	RemoveLabelIDs []uint  `json:"remove_label_ids"`
}

func (r UpdateMailMessagesRequest) update() MessageUpdate {
	return MessageUpdate{
		Read: r.Read, Starred: r.Starred, Placement: placementPointer(r.Placement), FolderID: r.FolderID,
		AddLabelIDs: r.AddLabelIDs, RemoveLabelIDs: r.RemoveLabelIDs,
	}
}

func placementPointer(value *string) *domain.MailPlacement {
	if value == nil {
		return nil
	}
	placement := domain.MailPlacement(*value)
	return &placement
}

// MailMessageUpdatesResponse counts the messages a batch update applied to.
type MailMessageUpdatesResponse struct {
	Updated int `json:"updated"`
}

// MailTrashPurgeResponse counts the messages emptying the trash deleted.
type MailTrashPurgeResponse struct {
	Deleted int `json:"deleted"`
}

// MailFolderRequest names a folder. The service trims and checks the name.
type MailFolderRequest struct {
	Name string `json:"name"`
}

// MailLabelRequest names and colors a label. The service trims the name and checks both.
type MailLabelRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// MailFolderResponse is one folder of a mailbox.
type MailFolderResponse struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// MailRoleRequest is the whole of a mail role a create or replace sets. Unknown fields are
// ignored; the service trims and checks every field.
type MailRoleRequest struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	// OwnerUserID is the member responsible for the role, for display only; null for none.
	OwnerUserID *uint `json:"owner_user_id"`
}

func (r MailRoleRequest) input() MailRoleInput {
	return MailRoleInput{
		Name: r.Name, Color: domain.MailLabelColor(r.Color), Description: r.Description, OwnerUserID: r.OwnerUserID,
	}
}

// MailRoleResponse is one mail role with the mailboxes in it the caller can see, ascending.
type MailRoleResponse struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Description string    `json:"description"`
	OwnerUserID *uint     `json:"owner_user_id"`
	MailboxIDs  []uint    `json:"mailbox_ids"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toMailRoleResponse(role *domain.MailRole) MailRoleResponse {
	mailboxIDs := role.MailboxIDs
	if mailboxIDs == nil {
		mailboxIDs = []uint{}
	}
	return MailRoleResponse{
		ID: role.ID, Name: role.Name, Color: string(role.Color), Description: role.Description,
		OwnerUserID: role.OwnerUserID, MailboxIDs: mailboxIDs, CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}
}

func toMailRoleResponses(roles []*domain.MailRole) []MailRoleResponse {
	responses := make([]MailRoleResponse, 0, len(roles))
	for _, role := range roles {
		responses = append(responses, toMailRoleResponse(role))
	}
	return responses
}

// MailLabelResponse is one label of a mailbox.
type MailLabelResponse struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

func toMailFolderResponse(folder *domain.MailFolder) MailFolderResponse {
	return MailFolderResponse{ID: folder.ID, Name: folder.Name, CreatedAt: folder.CreatedAt}
}

func toMailFolderResponses(folders []*domain.MailFolder) []MailFolderResponse {
	responses := make([]MailFolderResponse, 0, len(folders))
	for _, folder := range folders {
		responses = append(responses, toMailFolderResponse(folder))
	}
	return responses
}

func toMailLabelResponse(label *domain.MailLabel) MailLabelResponse {
	return MailLabelResponse{ID: label.ID, Name: label.Name, Color: string(label.Color), CreatedAt: label.CreatedAt}
}

func toMailLabelResponses(labels []*domain.MailLabel) []MailLabelResponse {
	responses := make([]MailLabelResponse, 0, len(labels))
	for _, label := range labels {
		responses = append(responses, toMailLabelResponse(label))
	}
	return responses
}

// MailboxDomainResponse describes where a user may create addresses.
type MailboxDomainResponse struct {
	Domain string `json:"domain"`
	// Kind is "shared" for the instance's domain, usable by every organization, or
	// "organization" for a verified domain the organization connected.
	Kind         string `json:"kind"`
	MaxMailboxes int    `json:"max_mailboxes"`
	// Provider is the outbound transport: "cloudflare" delivers, "log" only records.
	Provider string `json:"provider"`
	// InboundEnabled tells whether the API accepts signed deliveries from the mail edge.
	InboundEnabled bool `json:"inbound_enabled"`
	// DailySendLimit is the personal lane limit; the broadcast and transactional lanes have
	// their own, each counted separately per organization over a rolling 24 hours.
	DailySendLimit          int `json:"daily_send_limit"`
	DailyBroadcastLimit     int `json:"daily_broadcast_limit"`
	DailyTransactionalLimit int `json:"daily_transactional_limit"`
	// BroadcastsEnabled tells whether broadcasts can be sent: unsubscribe links need the inbound
	// secret and MAILBOX_PUBLIC_URL.
	BroadcastsEnabled bool `json:"broadcasts_enabled"`
	// CustomDomainsEnabled tells whether owners and admins may connect their own domains.
	CustomDomainsEnabled bool `json:"custom_domains_enabled"`
}

// MailLogEntryResponse is one row of the cross-mailbox log.
type MailLogEntryResponse struct {
	MailMessageSummaryResponse
	MailboxAddress string `json:"mailbox_address"`
}

// MailDayStatsResponse counts one UTC day.
type MailDayStatsResponse struct {
	Day      string `json:"day"`
	Sent     int    `json:"sent"`
	Failed   int    `json:"failed"`
	Received int    `json:"received"`
}

func toMailLogEntries(messages []*domain.MailMessage) []MailLogEntryResponse {
	entries := make([]MailLogEntryResponse, 0, len(messages))
	for _, message := range messages {
		entries = append(entries, MailLogEntryResponse{
			MailMessageSummaryResponse: toMailMessageSummary(message),
			MailboxAddress:             message.MailboxAddress,
		})
	}
	return entries
}

func toMailDayStats(stats []domain.MailDayStats) []MailDayStatsResponse {
	days := make([]MailDayStatsResponse, 0, len(stats))
	for _, day := range stats {
		days = append(days, MailDayStatsResponse{
			Day:      day.Day.Format("2006-01-02"),
			Sent:     day.Sent,
			Failed:   day.Failed,
			Received: day.Received,
		})
	}
	return days
}

// MailboxResponse is the public shape of a mailbox, as the requesting viewer sees it.
type MailboxResponse struct {
	ID          uint   `json:"id"`
	Address     string `json:"address"`
	DisplayName string `json:"display_name"`
	// Type is "personal" or "shared".
	Type string `json:"type"`
	// OwnerUserID is the owner of a personal mailbox; null for a shared one.
	OwnerUserID *uint `json:"owner_user_id"`
	// AwaitingHandover is true when the owner of a personal mailbox left the organization.
	AwaitingHandover bool `json:"awaiting_handover"`
	// SealedBefore hides every message created before it; null when never sealed.
	SealedBefore    *time.Time `json:"sealed_before"`
	WebhooksEnabled bool       `json:"webhooks_enabled"`
	// Access is what the viewer may do with the mailbox.
	Access MailboxAccessResponse `json:"access"`
	// UnreadCount counts unread inbound messages; 0 for a viewer who may not read the mailbox.
	UnreadCount int `json:"unread_count"`
	// RoleID is the mail role the mailbox belongs to; null when it has none.
	RoleID    *uint     `json:"role_id"`
	CreatedAt time.Time `json:"created_at"`
}

// MailboxAccessResponse is the viewer's capabilities on one mailbox.
type MailboxAccessResponse struct {
	View          bool `json:"view"`
	Send          bool `json:"send"`
	ManageMembers bool `json:"manage_members"`
	Administer    bool `json:"administer"`
	SeeHistory    bool `json:"see_history"`
	Organize      bool `json:"organize"`
}

// MailAddressResponse is one sender with an optional display name.
type MailAddressResponse struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

// MailMessageSummaryResponse is the listing shape of a message; it carries no body.
type MailMessageSummaryResponse struct {
	ID          uint                `json:"id"`
	MailboxID   uint                `json:"mailbox_id"`
	Direction   string              `json:"direction"`
	Status      string              `json:"status"`
	From        MailAddressResponse `json:"from"`
	To          []string            `json:"to"`
	Cc          []string            `json:"cc"`
	Subject     string              `json:"subject"`
	Read        bool                `json:"read"`
	FailureCode string              `json:"failure_code"`
	// QueuedReason is "send_limit" while the message waits for room in the organization's send
	// limit, and null otherwise.
	QueuedReason *string `json:"queued_reason"`
	// LastEvent summarizes the delivery events of an outbound message; empty when there are none.
	LastEvent string `json:"last_event"`
	// TemplateID names the template the message was rendered from; null when none was used.
	TemplateID *uint `json:"template_id"`
	// AttachmentCount is how many files the message has; the detail lists them.
	AttachmentCount int `json:"attachment_count"`
	// Placement, FolderID, LabelIDs, and TrashedAt are shared by everyone with access; Starred is
	// the viewer's own.
	Placement string     `json:"placement"`
	FolderID  *uint      `json:"folder_id"`
	LabelIDs  []uint     `json:"label_ids"`
	Starred   bool       `json:"starred"`
	ThreadID  uint       `json:"thread_id"`
	TrashedAt *time.Time `json:"trashed_at"`
	CreatedAt time.Time  `json:"created_at"`
	// Thread summarizes the message's thread in the listed view; only group=thread sets it.
	Thread *MailThreadSummaryResponse `json:"thread,omitempty"`
}

// MailThreadSummaryResponse is the thread of a listed message in the listed view.
type MailThreadSummaryResponse struct {
	MessageCount int    `json:"message_count"`
	MessageIDs   []uint `json:"message_ids"`
	Unread       bool   `json:"unread"`
	Starred      bool   `json:"starred"`
	LabelIDs     []uint `json:"label_ids"`
}

func toMailThreadSummaryResponse(thread *domain.MailThreadInfo) *MailThreadSummaryResponse {
	if thread == nil {
		return nil
	}
	ids, labelIDs := thread.MessageIDs, thread.LabelIDs
	if ids == nil {
		ids = []uint{}
	}
	if labelIDs == nil {
		labelIDs = []uint{}
	}
	return &MailThreadSummaryResponse{
		MessageCount: thread.MessageCount, MessageIDs: ids,
		Unread: thread.Unread, Starred: thread.Starred, LabelIDs: labelIDs,
	}
}

// MailMessageCountsResponse is the caller's counts for a mailbox's navigation.
type MailMessageCountsResponse struct {
	Placements MailPlacementCountsResponse `json:"placements"`
	Folders    []MailFolderCountResponse   `json:"folders"`
	Starred    int                         `json:"starred"`
}

// MailPlacementCountsResponse counts unread inbound messages in the Inbox and the archive.
type MailPlacementCountsResponse struct {
	DefaultInboundUnread int `json:"default_inbound_unread"`
	ArchiveUnread        int `json:"archive_unread"`
}

// MailFolderCountResponse counts unread inbound messages in one folder.
type MailFolderCountResponse struct {
	FolderID uint `json:"folder_id"`
	Unread   int  `json:"unread"`
}

func toMailMessageCountsResponse(counts domain.MailMessageCounts) MailMessageCountsResponse {
	folders := make([]MailFolderCountResponse, 0, len(counts.Folders))
	for _, folder := range counts.Folders {
		folders = append(folders, MailFolderCountResponse{FolderID: folder.FolderID, Unread: folder.Unread})
	}
	return MailMessageCountsResponse{
		Placements: MailPlacementCountsResponse{
			DefaultInboundUnread: counts.DefaultInboundUnread, ArchiveUnread: counts.ArchiveUnread,
		},
		Folders: folders,
		Starred: counts.Starred,
	}
}

// MailMessageResponse is the full shape of a message.
type MailMessageResponse struct {
	MailMessageSummaryResponse
	TextBody  string `json:"text_body"`
	HTMLBody  string `json:"html_body"`
	MessageID string `json:"message_id"`
	InReplyTo string `json:"in_reply_to"`
	// Events is the delivery history of an outbound message, oldest first.
	Events []MailEventResponse `json:"events"`
	// Attachments lists the files of the message without their content.
	Attachments []MailAttachmentResponse `json:"attachments"`
}

// MailAttachmentResponse describes one attached file. The content is downloaded separately.
type MailAttachmentResponse struct {
	ID          uint   `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

func toMailAttachmentResponses(attachments []domain.MailAttachment) []MailAttachmentResponse {
	responses := make([]MailAttachmentResponse, 0, len(attachments))
	for _, attachment := range attachments {
		responses = append(responses, MailAttachmentResponse{
			ID:          attachment.ID,
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
			Size:        attachment.Size,
		})
	}
	return responses
}

// MailEventResponse is one delivery step of a message.
type MailEventResponse struct {
	Type       string    `json:"type"`
	Recipient  string    `json:"recipient"`
	Detail     string    `json:"detail"`
	OccurredAt time.Time `json:"occurred_at"`
}

// DeliveryEventResponse acknowledges one provider event from the mail edge.
type DeliveryEventResponse struct {
	MessageID uint   `json:"message_id"`
	Type      string `json:"type"`
	Duplicate bool   `json:"duplicate"`
}

func toMailEventResponses(events []domain.MailEvent) []MailEventResponse {
	responses := make([]MailEventResponse, 0, len(events))
	for _, event := range events {
		responses = append(responses, MailEventResponse{
			Type:       string(event.Type),
			Recipient:  event.Recipient,
			Detail:     event.Detail,
			OccurredAt: event.OccurredAt,
		})
	}
	return responses
}

// InboundMailResponse acknowledges one delivery from the mail edge.
type InboundMailResponse struct {
	ID        uint `json:"id"`
	Duplicate bool `json:"duplicate"`
}

func toMailboxResponse(mailbox *domain.Mailbox) *MailboxResponse {
	if mailbox == nil {
		return nil
	}
	return &MailboxResponse{
		ID:               mailbox.ID,
		Address:          mailbox.Address,
		DisplayName:      mailbox.DisplayName,
		Type:             string(mailbox.Type),
		OwnerUserID:      mailbox.OwnerUserID,
		AwaitingHandover: mailbox.Type == domain.MailboxTypePersonal && mailbox.OwnerLeftAt != nil,
		SealedBefore:     mailbox.SealedBefore,
		WebhooksEnabled:  mailbox.WebhooksEnabled,
		Access:           MailboxAccessResponse(mailbox.Access),
		UnreadCount:      mailbox.UnreadCount,
		RoleID:           mailbox.RoleID,
		CreatedAt:        mailbox.CreatedAt,
	}
}

func toMailboxResponses(mailboxes []*domain.Mailbox) []*MailboxResponse {
	responses := make([]*MailboxResponse, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		responses = append(responses, toMailboxResponse(mailbox))
	}
	return responses
}

func toMailMessageSummary(message *domain.MailMessage) MailMessageSummaryResponse {
	to, cc := message.To, message.Cc
	if to == nil {
		to = []string{}
	}
	if cc == nil {
		cc = []string{}
	}
	labelIDs := message.LabelIDs
	if labelIDs == nil {
		labelIDs = []uint{}
	}
	return MailMessageSummaryResponse{
		ID:              message.ID,
		MailboxID:       message.MailboxID,
		Direction:       string(message.Direction),
		Status:          string(message.Status),
		From:            MailAddressResponse{Address: message.FromAddress, Name: message.FromName},
		To:              to,
		Cc:              cc,
		Subject:         message.Subject,
		Read:            message.Read,
		FailureCode:     message.FailureCode,
		QueuedReason:    queuedReasonOf(message),
		LastEvent:       string(message.LastEvent),
		AttachmentCount: message.AttachmentCount,
		TemplateID:      templateIDOf(message),
		Placement:       string(placementOrDefault(message.Placement)),
		FolderID:        folderIDOf(message),
		LabelIDs:        labelIDs,
		Starred:         message.Starred,
		ThreadID:        threadIDOf(message),
		TrashedAt:       trashedAtOf(message),
		CreatedAt:       message.CreatedAt,
		Thread:          toMailThreadSummaryResponse(message.Thread),
	}
}

func placementOrDefault(placement domain.MailPlacement) domain.MailPlacement {
	if placement == "" {
		return domain.MailPlacementDefault
	}
	return placement
}

// folderIDOf is the folder only while the message is in one.
func folderIDOf(message *domain.MailMessage) *uint {
	if message.Placement != domain.MailPlacementFolder {
		return nil
	}
	return message.FolderID
}

// trashedAtOf is the trash time only while the message is in the trash.
func trashedAtOf(message *domain.MailMessage) *time.Time {
	if message.Placement != domain.MailPlacementTrash {
		return nil
	}
	return message.TrashedAt
}

// threadIDOf falls back to the message's own id, which is the thread a message starts.
func threadIDOf(message *domain.MailMessage) uint {
	if message.ThreadID == 0 {
		return message.ID
	}
	return message.ThreadID
}

func queuedReasonOf(message *domain.MailMessage) *string {
	if message.QueuedReason == "" {
		return nil
	}
	reason := message.QueuedReason
	return &reason
}

func toMailMessageSummaries(messages []*domain.MailMessage) []MailMessageSummaryResponse {
	responses := make([]MailMessageSummaryResponse, 0, len(messages))
	for _, message := range messages {
		responses = append(responses, toMailMessageSummary(message))
	}
	return responses
}

func toMailMessageResponse(message *domain.MailMessage) *MailMessageResponse {
	if message == nil {
		return nil
	}
	return &MailMessageResponse{
		MailMessageSummaryResponse: toMailMessageSummary(message),
		TextBody:                   message.TextBody,
		HTMLBody:                   message.HTMLBody,
		MessageID:                  message.MessageID,
		InReplyTo:                  message.InReplyTo,
		Events:                     toMailEventResponses(message.Events),
		Attachments:                toMailAttachmentResponses(message.Attachments),
	}
}

func templateIDOf(message *domain.MailMessage) *uint {
	if message.TemplateID == 0 {
		return nil
	}
	id := message.TemplateID
	return &id
}

// InboxItemResponse is a unified inbox row: the single-mailbox listing item with the owning
// mailbox's address, its mail role, and a short text snippet.
type InboxItemResponse struct {
	MailMessageSummaryResponse
	MailboxAddress string `json:"mailbox_address"`
	// RoleID is the mailbox's mail role now; null when it has none.
	RoleID  *uint  `json:"role_id"`
	Snippet string `json:"snippet"`
}

func toInboxItemResponses(items []InboxItem) []InboxItemResponse {
	responses := make([]InboxItemResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, InboxItemResponse{
			MailMessageSummaryResponse: toMailMessageSummary(item.Message),
			MailboxAddress:             item.Message.MailboxAddress,
			RoleID:                     item.RoleID,
			Snippet:                    item.Snippet,
		})
	}
	return responses
}

// InboxCountsResponse is the caller's own counts over the mailboxes they may read.
type InboxCountsResponse struct {
	Views           InboxViewCountsResponse     `json:"views"`
	Roles           []InboxRoleCountResponse    `json:"roles"`
	UngroupedUnread int                         `json:"ungrouped_unread"`
	Mailboxes       []InboxMailboxCountResponse `json:"mailboxes"`
	TodayStartsAt   time.Time                   `json:"today_starts_at"`
	TodayEndsAt     time.Time                   `json:"today_ends_at"`
}

// InboxViewCountsResponse counts the unified inbox views.
type InboxViewCountsResponse struct {
	AllUnread   int `json:"all_unread"`
	Today       int `json:"today"`
	TodayUnread int `json:"today_unread"`
	Pending     int `json:"pending"`
	SentWaiting int `json:"sent_waiting"`
}

// InboxRoleCountResponse is the unread Inbox mail of one mail role's readable mailboxes.
type InboxRoleCountResponse struct {
	RoleID uint `json:"role_id"`
	Unread int  `json:"unread"`
}

// InboxMailboxCountResponse is the unread Inbox mail of one readable mailbox.
type InboxMailboxCountResponse struct {
	MailboxID uint `json:"mailbox_id"`
	Unread    int  `json:"unread"`
}

func toInboxCountsResponse(counts InboxCounts) InboxCountsResponse {
	roles := make([]InboxRoleCountResponse, 0, len(counts.Roles))
	for _, role := range counts.Roles {
		roles = append(roles, InboxRoleCountResponse(role))
	}
	mailboxes := make([]InboxMailboxCountResponse, 0, len(counts.Mailboxes))
	for _, mailbox := range counts.Mailboxes {
		mailboxes = append(mailboxes, InboxMailboxCountResponse(mailbox))
	}
	return InboxCountsResponse{
		Views: InboxViewCountsResponse{
			AllUnread: counts.AllUnread, Today: counts.Today, TodayUnread: counts.TodayUnread,
			Pending: counts.Pending, SentWaiting: counts.SentWaiting,
		},
		Roles:           roles,
		UngroupedUnread: counts.UngroupedUnread,
		Mailboxes:       mailboxes,
		TodayStartsAt:   counts.TodayStartsAt.UTC(),
		TodayEndsAt:     counts.TodayEndsAt.UTC(),
	}
}
