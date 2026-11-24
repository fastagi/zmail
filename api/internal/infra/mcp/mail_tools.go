package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Bounded defaults and limits for the AI-native mail MCP toolset.
const (
	DefaultSearchMailLimit = 20
	MaxSearchMailLimit     = 50
	DefaultReadThreadLimit = 10
	MaxReadThreadLimit     = 20
	DefaultTriageLimit     = 20
	MaxTriageLimit         = 50
	DefaultTriageWindowHrs = 24
	MaxTriageWindowHrs     = 720
	MaxSendRecipients      = 50
)

// MailToolBackend supplies intent-oriented mail operations for the MCP toolset.
// Implementations live in module/application wiring so this package stays free
// of internal/modules imports.
type MailToolBackend interface {
	SearchMail(ctx context.Context, input SearchMailInput) (SearchMailOutput, error)
	ReadThread(ctx context.Context, input ReadThreadInput) (ReadThreadOutput, error)
	TriageSummary(ctx context.Context, input TriageSummaryInput) (TriageSummaryOutput, error)
	DraftReply(ctx context.Context, input DraftReplyInput) (DraftReplyOutput, error)
	OrganizeThread(ctx context.Context, input OrganizeThreadInput) (OrganizeThreadOutput, error)
	SendMail(ctx context.Context, input SendMailInput) (SendMailOutput, error)
}

// SearchMailInput defines parameters for searching mailbox messages.
type SearchMailInput struct {
	Query      string `json:"query,omitempty"`
	Folder     string `json:"folder,omitempty"`
	From       string `json:"from,omitempty"`
	Label      string `json:"label,omitempty"`
	UnreadOnly bool   `json:"unread_only,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// SearchMailHit is a compact summary of a matching message.
type SearchMailHit struct {
	MessageID  string   `json:"message_id"`
	ThreadID   string   `json:"thread_id"`
	Subject    string   `json:"subject"`
	From       string   `json:"from"`
	Snippet    string   `json:"snippet"`
	ReceivedAt string   `json:"received_at"`
	Unread     bool     `json:"unread"`
	Labels     []string `json:"labels,omitempty"`
}

// SearchMailOutput returns bounded search results.
type SearchMailOutput struct {
	Matches   []SearchMailHit `json:"matches"`
	Total     int             `json:"total"`
	Truncated bool            `json:"truncated,omitempty"`
}

// ReadThreadInput defines parameters for reading a thread.
type ReadThreadInput struct {
	ThreadID           string `json:"thread_id"`
	Limit              int    `json:"limit,omitempty"`
	IncludeAttachments bool   `json:"include_attachments,omitempty"`
}

// ThreadMessage is one message inside a thread.
type ThreadMessage struct {
	MessageID   string   `json:"message_id"`
	From        string   `json:"from"`
	To          []string `json:"to"`
	Cc          []string `json:"cc,omitempty"`
	SentAt      string   `json:"sent_at"`
	BodyText    string   `json:"body_text"`
	Attachments []string `json:"attachments,omitempty"`
}

// ReadThreadOutput returns a bounded slice of thread messages.
type ReadThreadOutput struct {
	ThreadID      string          `json:"thread_id"`
	Subject       string          `json:"subject"`
	Messages      []ThreadMessage `json:"messages"`
	TotalMessages int             `json:"total_messages"`
	Truncated     bool            `json:"truncated,omitempty"`
}

// TriageSummaryInput defines parameters for mailbox triage.
type TriageSummaryInput struct {
	Folder      string `json:"folder,omitempty"`
	WindowHours int    `json:"window_hours,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// TriageItem represents a prioritized thread requiring attention.
type TriageItem struct {
	ThreadID   string `json:"thread_id"`
	Subject    string `json:"subject"`
	From       string `json:"from"`
	Priority   string `json:"priority"`
	Reason     string `json:"reason"`
	ReceivedAt string `json:"received_at"`
}

// TriageSummaryOutput summarizes mailbox counts and priority threads.
type TriageSummaryOutput struct {
	Folder      string       `json:"folder"`
	WindowHours int          `json:"window_hours"`
	UnreadCount int          `json:"unread_count"`
	UrgentCount int          `json:"urgent_count"`
	Items       []TriageItem `json:"items"`
	Truncated   bool         `json:"truncated,omitempty"`
}

// DraftReplyInput defines parameters for generating a non-mutating reply draft.
type DraftReplyInput struct {
	ThreadID     string `json:"thread_id"`
	Instructions string `json:"instructions"`
	Tone         string `json:"tone,omitempty"`
	ReplyAll     bool   `json:"reply_all,omitempty"`
}

// DraftReplyOutput contains the proposed reply without sending or persisting.
type DraftReplyOutput struct {
	ThreadID string   `json:"thread_id"`
	To       []string `json:"to"`
	Cc       []string `json:"cc,omitempty"`
	Subject  string   `json:"subject"`
	BodyText string   `json:"body_text"`
}

// OrganizeThreadInput defines a state mutation on a thread.
type OrganizeThreadInput struct {
	ThreadID    string `json:"thread_id"`
	Action      string `json:"action"`
	Label       string `json:"label,omitempty"`
	SnoozeUntil string `json:"snooze_until,omitempty"`
}

// OrganizeThreadOutput reports the applied thread organization state.
type OrganizeThreadOutput struct {
	ThreadID string   `json:"thread_id"`
	Action   string   `json:"action"`
	Applied  bool     `json:"applied"`
	Labels   []string `json:"labels,omitempty"`
}

// SendMailInput defines parameters for sending a new email or reply.
type SendMailInput struct {
	To       []string `json:"to"`
	Cc       []string `json:"cc,omitempty"`
	Bcc      []string `json:"bcc,omitempty"`
	Subject  string   `json:"subject"`
	BodyText string   `json:"body_text"`
	ThreadID string   `json:"thread_id,omitempty"`
}

// SendMailOutput confirms message acceptance for delivery.
type SendMailOutput struct {
	MessageID          string `json:"message_id"`
	ThreadID           string `json:"thread_id"`
	AcceptedRecipients int    `json:"accepted_recipients"`
	QueuedAt           string `json:"queued_at"`
}

var (
	validDraftTones      = map[string]struct{}{"concise": {}, "formal": {}, "friendly": {}, "direct": {}}
	validOrganizeActions = map[string]struct{}{"archive": {}, "label": {}, "unlabel": {}, "mark_read": {}, "mark_unread": {}, "star": {}, "unstar": {}, "snooze": {}, "trash": {}}
)

// MailTools returns the 6 high-cohesion, intent-oriented mail MCP tools.
func MailTools(backend MailToolBackend) []Tool {
	return []Tool{
		searchMailTool(backend), readThreadTool(backend), triageSummaryTool(backend),
		draftReplyTool(backend), organizeThreadTool(backend), sendMailTool(backend),
	}
}

func searchMailTool(backend MailToolBackend) Tool {
	return Tool{
		Name: "search_mail", Title: "Search Mail", ReadOnly: true,
		Description: "Search mailbox messages by query, folder, sender, label, or unread state. Returns compact summaries.",
		InputSchema: mailObjectSchema(map[string]any{
			"query":       map[string]any{"type": "string", "description": "Free-text search query."},
			"folder":      map[string]any{"type": "string", "description": "Mailbox folder name, e.g. inbox or sent."},
			"from":        map[string]any{"type": "string", "description": "Filter by sender address or display name."},
			"label":       map[string]any{"type": "string", "description": "Filter by label name."},
			"unread_only": map[string]any{"type": "boolean", "description": "When true, return only unread messages."},
			"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": MaxSearchMailLimit, "description": "Maximum results to return (default 20, max 50)."},
		}),
		Handle: mailHandler(backend, func(ctx context.Context, input SearchMailInput) (SearchMailOutput, error) {
			input.Query, input.Folder = strings.TrimSpace(input.Query), strings.TrimSpace(input.Folder)
			input.From, input.Label = strings.TrimSpace(input.From), strings.TrimSpace(input.Label)
			if input.Query == "" && input.Folder == "" && input.From == "" && input.Label == "" && !input.UnreadOnly {
				return SearchMailOutput{}, errors.New("at least one search filter is required")
			}
			limit, err := clampLimit(input.Limit, DefaultSearchMailLimit, MaxSearchMailLimit)
			if err != nil {
				return SearchMailOutput{}, err
			}
			input.Limit = limit
			out, err := backend.SearchMail(ctx, input)
			if err != nil {
				return SearchMailOutput{}, err
			}
			out.Matches, out.Truncated = clampSlice(out.Matches, limit, out.Truncated)
			return out, nil
		}),
	}
}

func readThreadTool(backend MailToolBackend) Tool {
	return Tool{
		Name: "read_thread", Title: "Read Thread", ReadOnly: true,
		Description: "Read messages in a mail thread with bounded message count and optional attachment metadata.",
		InputSchema: mailObjectSchema(map[string]any{
			"thread_id":           map[string]any{"type": "string", "minLength": 1, "description": "Identifier of the thread to read."},
			"limit":               map[string]any{"type": "integer", "minimum": 1, "maximum": MaxReadThreadLimit, "description": "Maximum messages to return (default 10, max 20)."},
			"include_attachments": map[string]any{"type": "boolean", "description": "Include attachment filenames on each message."},
		}, "thread_id"),
		Handle: mailHandler(backend, func(ctx context.Context, input ReadThreadInput) (ReadThreadOutput, error) {
			if input.ThreadID = strings.TrimSpace(input.ThreadID); input.ThreadID == "" {
				return ReadThreadOutput{}, errors.New("thread_id is required")
			}
			limit, err := clampLimit(input.Limit, DefaultReadThreadLimit, MaxReadThreadLimit)
			if err != nil {
				return ReadThreadOutput{}, err
			}
			input.Limit = limit
			out, err := backend.ReadThread(ctx, input)
			if err != nil {
				return ReadThreadOutput{}, err
			}
			out.Messages, out.Truncated = clampSlice(out.Messages, limit, out.Truncated)
			return out, nil
		}),
	}
}

func triageSummaryTool(backend MailToolBackend) Tool {
	return Tool{
		Name: "triage_summary", Title: "Triage Summary", ReadOnly: true,
		Description: "Summarize unread counts and priority threads requiring attention within a time window.",
		InputSchema: mailObjectSchema(map[string]any{
			"folder":       map[string]any{"type": "string", "description": "Mailbox folder to triage (default inbox)."},
			"window_hours": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxTriageWindowHrs, "description": "Lookback window in hours (default 24, max 720)."},
			"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": MaxTriageLimit, "description": "Maximum priority items to return (default 20, max 50)."},
		}),
		Handle: mailHandler(backend, func(ctx context.Context, input TriageSummaryInput) (TriageSummaryOutput, error) {
			if input.Folder = strings.TrimSpace(input.Folder); input.Folder == "" {
				input.Folder = "inbox"
			}
			if input.WindowHours < 0 || input.WindowHours > MaxTriageWindowHrs {
				return TriageSummaryOutput{}, fmt.Errorf("window_hours must be between 1 and %d", MaxTriageWindowHrs)
			}
			if input.WindowHours == 0 {
				input.WindowHours = DefaultTriageWindowHrs
			}
			limit, err := clampLimit(input.Limit, DefaultTriageLimit, MaxTriageLimit)
			if err != nil {
				return TriageSummaryOutput{}, err
			}
			input.Limit = limit
			out, err := backend.TriageSummary(ctx, input)
			if err != nil {
				return TriageSummaryOutput{}, err
			}
			out.Items, out.Truncated = clampSlice(out.Items, limit, out.Truncated)
			return out, nil
		}),
	}
}

func draftReplyTool(backend MailToolBackend) Tool {
	return Tool{
		Name: "draft_reply", Title: "Draft Reply", ReadOnly: true,
		Description: "Generate a proposed reply for a thread without sending or mutating mailbox state.",
		InputSchema: mailObjectSchema(map[string]any{
			"thread_id":    map[string]any{"type": "string", "minLength": 1, "description": "Identifier of the thread to reply to."},
			"instructions": map[string]any{"type": "string", "minLength": 1, "description": "Key points or intent for the reply."},
			"tone":         map[string]any{"type": "string", "enum": []string{"concise", "formal", "friendly", "direct"}, "description": "Desired tone (default concise)."},
			"reply_all":    map[string]any{"type": "boolean", "description": "Include all original recipients when true."},
		}, "thread_id", "instructions"),
		Handle: mailHandler(backend, func(ctx context.Context, input DraftReplyInput) (DraftReplyOutput, error) {
			input.ThreadID, input.Instructions = strings.TrimSpace(input.ThreadID), strings.TrimSpace(input.Instructions)
			if input.ThreadID == "" {
				return DraftReplyOutput{}, errors.New("thread_id is required")
			}
			if input.Instructions == "" {
				return DraftReplyOutput{}, errors.New("instructions is required")
			}
			if input.Tone = strings.ToLower(strings.TrimSpace(input.Tone)); input.Tone == "" {
				input.Tone = "concise"
			}
			if _, ok := validDraftTones[input.Tone]; !ok {
				return DraftReplyOutput{}, fmt.Errorf("unsupported tone %q", input.Tone)
			}
			return backend.DraftReply(ctx, input)
		}),
	}
}

func organizeThreadTool(backend MailToolBackend) Tool {
	return Tool{
		Name: "organize_thread", Title: "Organize Thread", ReadOnly: false,
		Description: "Apply an organization action to a thread such as archive, label, mark_read, star, snooze, or trash.",
		InputSchema: mailObjectSchema(map[string]any{
			"thread_id":    map[string]any{"type": "string", "minLength": 1, "description": "Identifier of the thread to update."},
			"action":       map[string]any{"type": "string", "enum": []string{"archive", "label", "unlabel", "mark_read", "mark_unread", "star", "unstar", "snooze", "trash"}, "description": "Organization action to apply."},
			"label":        map[string]any{"type": "string", "description": "Label name, required for label and unlabel actions."},
			"snooze_until": map[string]any{"type": "string", "format": "date-time", "description": "RFC3339 timestamp, required for snooze action."},
		}, "thread_id", "action"),
		Handle: mailHandler(backend, func(ctx context.Context, input OrganizeThreadInput) (OrganizeThreadOutput, error) {
			input.ThreadID, input.Action = strings.TrimSpace(input.ThreadID), strings.ToLower(strings.TrimSpace(input.Action))
			input.Label, input.SnoozeUntil = strings.TrimSpace(input.Label), strings.TrimSpace(input.SnoozeUntil)
			if input.ThreadID == "" {
				return OrganizeThreadOutput{}, errors.New("thread_id is required")
			}
			if _, ok := validOrganizeActions[input.Action]; !ok {
				return OrganizeThreadOutput{}, fmt.Errorf("unsupported action %q", input.Action)
			}
			if (input.Action == "label" || input.Action == "unlabel") && input.Label == "" {
				return OrganizeThreadOutput{}, fmt.Errorf("label is required for %s action", input.Action)
			}
			if input.Action == "snooze" {
				if input.SnoozeUntil == "" {
					return OrganizeThreadOutput{}, errors.New("snooze_until is required for snooze action")
				}
				if _, err := time.Parse(time.RFC3339, input.SnoozeUntil); err != nil {
					return OrganizeThreadOutput{}, errors.New("snooze_until must be a valid RFC3339 timestamp")
				}
			}
			return backend.OrganizeThread(ctx, input)
		}),
	}
}

func sendMailTool(backend MailToolBackend) Tool {
	return Tool{
		Name: "send_mail", Title: "Send Mail", ReadOnly: false,
		Description: "Send a new email or thread reply to one or more recipients.",
		InputSchema: mailObjectSchema(map[string]any{
			"to":        map[string]any{"type": "array", "minItems": 1, "maxItems": MaxSendRecipients, "items": map[string]any{"type": "string", "minLength": 3}, "description": "Primary recipient email addresses."},
			"cc":        map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 3}, "description": "Carbon-copy recipient email addresses."},
			"bcc":       map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 3}, "description": "Blind carbon-copy recipient email addresses."},
			"subject":   map[string]any{"type": "string", "minLength": 1, "description": "Email subject line."},
			"body_text": map[string]any{"type": "string", "minLength": 1, "description": "Plain-text email body."},
			"thread_id": map[string]any{"type": "string", "description": "Optional thread identifier when sending a reply."},
		}, "to", "subject", "body_text"),
		Handle: mailHandler(backend, func(ctx context.Context, input SendMailInput) (SendMailOutput, error) {
			var err error
			if input.To, err = normalizeRecipients("to", input.To); err != nil {
				return SendMailOutput{}, err
			}
			if len(input.To) == 0 {
				return SendMailOutput{}, errors.New("at least one recipient in to is required")
			}
			if input.Cc, err = normalizeRecipients("cc", input.Cc); err != nil {
				return SendMailOutput{}, err
			}
			if input.Bcc, err = normalizeRecipients("bcc", input.Bcc); err != nil {
				return SendMailOutput{}, err
			}
			if len(input.To)+len(input.Cc)+len(input.Bcc) > MaxSendRecipients {
				return SendMailOutput{}, fmt.Errorf("total recipients must not exceed %d", MaxSendRecipients)
			}
			input.Subject, input.BodyText, input.ThreadID = strings.TrimSpace(input.Subject), strings.TrimSpace(input.BodyText), strings.TrimSpace(input.ThreadID)
			if input.Subject == "" {
				return SendMailOutput{}, errors.New("subject is required")
			}
			if input.BodyText == "" {
				return SendMailOutput{}, errors.New("body_text is required")
			}
			return backend.SendMail(ctx, input)
		}),
	}
}

func mailHandler[In, Out any](backend MailToolBackend, fn func(context.Context, In) (Out, error)) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if backend == nil {
			return nil, errors.New("mail tool backend is not configured")
		}
		var input In
		if err := DecodeArguments(raw, &input); err != nil {
			return nil, err
		}
		return fn(ctx, input)
	}
}

func mailObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func clampLimit(limit, defaultLimit, maxLimit int) (int, error) {
	if limit < 0 {
		return 0, errors.New("limit must be >= 0")
	}
	if limit == 0 {
		return defaultLimit, nil
	}
	if limit > maxLimit {
		return maxLimit, nil
	}
	return limit, nil
}

func clampSlice[T any](items []T, limit int, truncated bool) ([]T, bool) {
	if len(items) > limit {
		return items[:limit], true
	}
	if items == nil {
		return []T{}, truncated
	}
	return items, truncated
}

func normalizeRecipients(field string, raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	cleaned := make([]string, 0, len(raw))
	for _, addr := range raw {
		trimmed := strings.TrimSpace(addr)
		at := strings.IndexByte(trimmed, '@')
		if trimmed == "" || at <= 0 || at == len(trimmed)-1 || strings.ContainsAny(trimmed, " \t\r\n") {
			return nil, fmt.Errorf("invalid recipient in %s: %q", field, addr)
		}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned, nil
}
