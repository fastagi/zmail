package transactional

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

const sendLaneWindowHours = 24

// SendTransactionalMessageRequest is the body of POST /v1/transactional-messages.
type SendTransactionalMessageRequest struct {
	Template  string            `json:"template" binding:"required,max=64"`
	To        string            `json:"to" binding:"required,min=3,max=320"`
	Variables map[string]string `json:"variables" binding:"omitempty,max=100"`
}

// SendTransactionalTestRequest is the body of POST /v1/transactional-test-sends.
type SendTransactionalTestRequest struct {
	Template  string            `json:"template" binding:"required,max=64"`
	Mode      string            `json:"mode" binding:"required,oneof=live test"`
	Variables map[string]string `json:"variables" binding:"omitempty,max=100"`
}

// TransactionalSenderResponse is the template's sender when the message was created.
type TransactionalSenderResponse struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

// TransactionalEventResponse is one delivery event of a transactional message.
type TransactionalEventResponse struct {
	Type       string    `json:"type"`
	Detail     string    `json:"detail"`
	OccurredAt time.Time `json:"occurred_at"`
}

// TransactionalMessageResponse is a transactional message as its API key sees it.
type TransactionalMessageResponse struct {
	ID             uint                         `json:"id"`
	Mode           string                       `json:"mode"`
	Simulated      bool                         `json:"simulated"`
	Template       string                       `json:"template"`
	From           TransactionalSenderResponse  `json:"from"`
	To             string                       `json:"to"`
	ToMasked       string                       `json:"to_masked"`
	Subject        string                       `json:"subject"`
	Variables      map[string]string            `json:"variables"`
	Status         string                       `json:"status"`
	QueuedReason   *string                      `json:"queued_reason"`
	FailureCode    *string                      `json:"failure_code"`
	LastEvent      *string                      `json:"last_event"`
	Attempts       int                          `json:"attempts"`
	IdempotencyKey *string                      `json:"idempotency_key"`
	CreatedAt      time.Time                    `json:"created_at"`
	SentAt         *time.Time                   `json:"sent_at"`
	Events         []TransactionalEventResponse `json:"events"`
}

// TransactionalLogEntryResponse is a transactional message in the console log listing.
type TransactionalLogEntryResponse struct {
	ID             uint       `json:"id"`
	Mode           string     `json:"mode"`
	Simulated      bool       `json:"simulated"`
	Template       string     `json:"template"`
	ToMasked       string     `json:"to_masked"`
	Subject        string     `json:"subject"`
	Status         string     `json:"status"`
	QueuedReason   *string    `json:"queued_reason"`
	FailureCode    *string    `json:"failure_code"`
	LastEvent      *string    `json:"last_event"`
	IdempotencyKey *string    `json:"idempotency_key"`
	CreatedAt      time.Time  `json:"created_at"`
	SentAt         *time.Time `json:"sent_at"`
}

// TransactionalLogEntryDetailResponse adds sender, masked variables, and events to a log entry.
type TransactionalLogEntryDetailResponse struct {
	TransactionalLogEntryResponse
	From      TransactionalSenderResponse  `json:"from"`
	Variables map[string]string            `json:"variables"`
	Events    []TransactionalEventResponse `json:"events"`
}

// TransactionalLogMetaResponse is the meta of a log page.
type TransactionalLogMetaResponse struct {
	NextCursor *string `json:"next_cursor"`
	Total24h   int64   `json:"total_24h"`
}

// MailSendLaneUsageResponse is how full one send lane is.
type MailSendLaneUsageResponse struct {
	Lane    string `json:"lane"`
	Limit   int    `json:"limit"`
	Used    int64  `json:"used"`
	Waiting int64  `json:"waiting"`
}

// MailSendLaneUsageListResponse is the usage of every send lane over the rolling window.
type MailSendLaneUsageListResponse struct {
	Lanes       []MailSendLaneUsageResponse `json:"lanes"`
	WindowHours int                         `json:"window_hours"`
	ComputedAt  time.Time                   `json:"computed_at"`
}

// MailSuppressionResponse is one entry of the suppression list.
type MailSuppressionResponse struct {
	ID        uint      `json:"id"`
	Email     string    `json:"email"`
	Reason    string    `json:"reason"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// MailSuppressionPageMetaResponse is the meta of a suppression list page.
type MailSuppressionPageMetaResponse struct {
	NextCursor *string `json:"next_cursor"`
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func maskRecipient(address string) string {
	at := strings.LastIndex(address, "@")
	if at <= 0 {
		return "***"
	}
	local, host := address[:at], address[at+1:]
	first, size := utf8.DecodeRuneInString(local)
	if size == 0 || first == utf8.RuneError {
		return "***@" + host
	}
	return string(first) + "***@" + host
}

func toTransactionalMessageResponse(message *domain.TransactionalMessage) TransactionalMessageResponse {
	variables := message.VariablesMasked
	if variables == nil {
		variables = map[string]string{}
	}
	events := make([]TransactionalEventResponse, 0, len(message.Events))
	for _, event := range message.Events {
		events = append(events, TransactionalEventResponse{
			Type:       string(event.Type),
			Detail:     event.Detail,
			OccurredAt: event.OccurredAt.UTC(),
		})
	}
	var queuedReason *string
	if message.QueuedReason != nil {
		queuedReason = optionalString(string(*message.QueuedReason))
	}
	var sentAt *time.Time
	if message.SentAt != nil {
		at := message.SentAt.UTC()
		sentAt = &at
	}
	return TransactionalMessageResponse{
		ID:             message.ID,
		Mode:           string(message.Mode),
		Simulated:      message.Simulated,
		Template:       message.TemplateKey,
		From:           TransactionalSenderResponse{Address: message.FromAddress, Name: message.FromName},
		To:             message.ToAddress,
		ToMasked:       maskRecipient(message.ToAddress),
		Subject:        message.SubjectMasked,
		Variables:      variables,
		Status:         string(message.Status),
		QueuedReason:   queuedReason,
		FailureCode:    optionalString(message.FailureCode),
		LastEvent:      optionalString(string(message.LastEvent)),
		Attempts:       message.Attempts,
		IdempotencyKey: message.IdempotencyKey,
		CreatedAt:      message.CreatedAt.UTC(),
		SentAt:         sentAt,
		Events:         events,
	}
}

func toTransactionalLogEntry(message *domain.TransactionalMessage) TransactionalLogEntryResponse {
	full := toTransactionalMessageResponse(message)
	return TransactionalLogEntryResponse{
		ID:             full.ID,
		Mode:           full.Mode,
		Simulated:      full.Simulated,
		Template:       full.Template,
		ToMasked:       full.ToMasked,
		Subject:        full.Subject,
		Status:         full.Status,
		QueuedReason:   full.QueuedReason,
		FailureCode:    full.FailureCode,
		LastEvent:      full.LastEvent,
		IdempotencyKey: full.IdempotencyKey,
		CreatedAt:      full.CreatedAt,
		SentAt:         full.SentAt,
	}
}

func toTransactionalLogEntries(messages []*domain.TransactionalMessage) []TransactionalLogEntryResponse {
	entries := make([]TransactionalLogEntryResponse, 0, len(messages))
	for _, message := range messages {
		entries = append(entries, toTransactionalLogEntry(message))
	}
	return entries
}

func toTransactionalLogEntryDetail(message *domain.TransactionalMessage) TransactionalLogEntryDetailResponse {
	full := toTransactionalMessageResponse(message)
	return TransactionalLogEntryDetailResponse{
		TransactionalLogEntryResponse: toTransactionalLogEntry(message),
		From:                          full.From,
		Variables:                     full.Variables,
		Events:                        full.Events,
	}
}

func toTransactionalLogMeta(page domain.MailTransactionalLogPage) TransactionalLogMetaResponse {
	meta := TransactionalLogMetaResponse{Total24h: page.Total24h}
	if page.NextCursor != nil {
		cursor := strconv.FormatUint(uint64(*page.NextCursor), 10)
		meta.NextCursor = &cursor
	}
	return meta
}

func toMailSendLaneUsageList(lanes []domain.MailSendLaneUsage, computedAt time.Time) MailSendLaneUsageListResponse {
	items := make([]MailSendLaneUsageResponse, 0, len(lanes))
	for _, lane := range lanes {
		items = append(items, MailSendLaneUsageResponse{
			Lane:    string(lane.Lane),
			Limit:   lane.Limit,
			Used:    lane.Used,
			Waiting: lane.Waiting,
		})
	}
	return MailSendLaneUsageListResponse{
		Lanes:       items,
		WindowHours: sendLaneWindowHours,
		ComputedAt:  computedAt.UTC(),
	}
}

func toMailSuppressions(suppressions []domain.MailSuppression) []MailSuppressionResponse {
	items := make([]MailSuppressionResponse, 0, len(suppressions))
	for _, suppression := range suppressions {
		items = append(items, MailSuppressionResponse{
			ID:        suppression.ID,
			Email:     suppression.Email,
			Reason:    suppression.Reason,
			Source:    suppression.Source,
			CreatedAt: suppression.CreatedAt.UTC(),
		})
	}
	return items
}

func toMailSuppressionPageMeta(next *uint) MailSuppressionPageMetaResponse {
	if next == nil {
		return MailSuppressionPageMetaResponse{}
	}
	cursor := strconv.FormatUint(uint64(*next), 10)
	return MailSuppressionPageMetaResponse{NextCursor: &cursor}
}
