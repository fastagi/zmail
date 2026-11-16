package mailbox

import (
	"time"

	"github.com/fastagi/fastmail/api/internal/domain"
)

// CreateMailboxRequest asks for a new address on the configured domain.
type CreateMailboxRequest struct {
	LocalPart   string `json:"local_part" binding:"required,max=64"`
	DisplayName string `json:"display_name" binding:"max=120"`
}

// SendMailMessageRequest asks to send one message from a mailbox.
type SendMailMessageRequest struct {
	To        []string `json:"to" binding:"required,min=1,max=50,dive,required,max=320"`
	Cc        []string `json:"cc" binding:"omitempty,max=50,dive,required,max=320"`
	Subject   string   `json:"subject" binding:"required,max=998"`
	Text      string   `json:"text" binding:"max=262144"`
	HTML      string   `json:"html" binding:"max=262144"`
	InReplyTo string   `json:"in_reply_to" binding:"max=900"`
}

// SendOutboundMailRequest asks to send one message with an API key. From names one of the key
// owner's mailboxes.
type SendOutboundMailRequest struct {
	From string `json:"from" binding:"required,max=320"`
	SendMailMessageRequest
}

func (r SendMailMessageRequest) input(idempotencyKey string) SendInput {
	return SendInput{
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
	CreatedAt   time.Time           `json:"created_at"`
}

// MailMessageResponse is the full shape of a message.
type MailMessageResponse struct {
	MailMessageSummaryResponse
	TextBody  string `json:"text_body"`
	HTMLBody  string `json:"html_body"`
	MessageID string `json:"message_id"`
	InReplyTo string `json:"in_reply_to"`
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
		ID:          message.ID,
		MailboxID:   message.MailboxID,
		Direction:   string(message.Direction),
		Status:      string(message.Status),
		From:        MailAddressResponse{Address: message.FromAddress, Name: message.FromName},
		To:          to,
		Cc:          cc,
		Subject:     message.Subject,
		Read:        message.ReadAt != nil,
		FailureCode: message.FailureCode,
		CreatedAt:   message.CreatedAt,
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
	}
}
