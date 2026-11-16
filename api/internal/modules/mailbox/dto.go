package mailbox

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// CreateMailboxRequest asks for a new address on the configured domain.
type CreateMailboxRequest struct {
	LocalPart   string `json:"local_part" binding:"required,max=64"`
	DisplayName string `json:"display_name" binding:"max=120"`
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

// UpdateMailMessageRequest changes the read state of one message.
type UpdateMailMessageRequest struct {
	Read *bool `json:"read" binding:"required"`
}

// MailboxDomainResponse describes where a user may create addresses.
type MailboxDomainResponse struct {
	Domain       string `json:"domain"`
	MaxMailboxes int    `json:"max_mailboxes"`
	// Provider is the outbound transport: "cloudflare" delivers, "log" only records.
	Provider string `json:"provider"`
	// InboundEnabled tells whether the API accepts signed deliveries from the mail edge.
	InboundEnabled bool `json:"inbound_enabled"`
	DailySendLimit int  `json:"daily_send_limit"`
	// BroadcastsEnabled tells whether broadcasts can be sent: unsubscribe links need the inbound
	// secret and MAILBOX_PUBLIC_URL.
	BroadcastsEnabled bool `json:"broadcasts_enabled"`
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

// MailboxResponse is the public shape of a mailbox.
type MailboxResponse struct {
	ID          uint   `json:"id"`
	Address     string `json:"address"`
	DisplayName string `json:"display_name"`
	// UnreadCount counts unread inbound messages. A newly created mailbox reports 0.
	UnreadCount int       `json:"unread_count"`
	CreatedAt   time.Time `json:"created_at"`
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
	// LastEvent summarizes the delivery events of an outbound message; empty when there are none.
	LastEvent string `json:"last_event"`
	// TemplateID names the template the message was rendered from; null when none was used.
	TemplateID *uint `json:"template_id"`
	// AttachmentCount is how many files the message has; the detail lists them.
	AttachmentCount int       `json:"attachment_count"`
	CreatedAt       time.Time `json:"created_at"`
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
		ID:          mailbox.ID,
		Address:     mailbox.Address,
		DisplayName: mailbox.DisplayName,
		UnreadCount: mailbox.UnreadCount,
		CreatedAt:   mailbox.CreatedAt,
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
	return MailMessageSummaryResponse{
		ID:              message.ID,
		MailboxID:       message.MailboxID,
		Direction:       string(message.Direction),
		Status:          string(message.Status),
		From:            MailAddressResponse{Address: message.FromAddress, Name: message.FromName},
		To:              to,
		Cc:              cc,
		Subject:         message.Subject,
		Read:            message.ReadAt != nil,
		FailureCode:     message.FailureCode,
		LastEvent:       string(message.LastEvent),
		AttachmentCount: message.AttachmentCount,
		TemplateID:      templateIDOf(message),
		CreatedAt:       message.CreatedAt,
	}
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
