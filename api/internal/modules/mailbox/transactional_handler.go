package mailbox

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// ScopeMailTransactional lets an API key send and read transactional messages. It neither
// includes nor is included by mail:send.
const ScopeMailTransactional = "mail:transactional"

// maxTransactionalBodyBytes bounds a transactional send request.
const maxTransactionalBodyBytes = 64 << 10

// SendTransactionalMessageRequest is the body of POST /v1/transactional-messages.
type SendTransactionalMessageRequest struct {
	Template  string            `json:"template" binding:"required,max=64"`
	To        string            `json:"to" binding:"required,min=3,max=320"`
	Variables map[string]string `json:"variables" binding:"omitempty,max=100"`
}

// SendTransactionalTestRequest is the body of POST /v1/transactional-test-sends. It has no
// recipient: a test send always goes to the caller's own account address.
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

// TransactionalMessageResponse is a transactional message as its API key sees it, with the full
// recipient and the masked subject and variables.
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

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func toTransactionalMessageResponse(message *domain.TransactionalMessage) TransactionalMessageResponse {
	variables := message.VariablesMasked
	if variables == nil {
		variables = map[string]string{}
	}
	events := make([]TransactionalEventResponse, 0, len(message.Events))
	for _, event := range message.Events {
		events = append(events, TransactionalEventResponse{
			Type: string(event.Type), Detail: event.Detail, OccurredAt: event.OccurredAt.UTC(),
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

// transactionalLocation is the path a transactional message is read at.
func transactionalLocation(id uint) string {
	return "/v1/transactional-messages/" + strconv.FormatUint(uint64(id), 10)
}

// apiKeyID returns the authenticated key's id, set by the api_key middleware.
func apiKeyID(c *gin.Context) *uint {
	value, ok := c.Get("apiKeyID")
	if !ok {
		return nil
	}
	id, ok := value.(uint)
	if !ok {
		return nil
	}
	return &id
}

// SendTransactional sends one transactional message for an API key with mail:transactional.
func (h *Handler) SendTransactional(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	// The switch answers before the body is read, so a disabled instance never validates.
	if !h.service.TransactionalPolicy().Enabled {
		response.HandleError(c, "Transactional mail is disabled", ErrTransactionalDisabled)
		return
	}
	var req SendTransactionalMessageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	actor := TransactionalActor{
		OrganizationID: viewer.OrganizationID,
		UserID:         viewer.UserID,
		APIKeyID:       apiKeyID(c),
		Mode:           apikey.APIKeyModeFromContext(c),
	}
	message, created, err := h.service.SendTransactional(c.Request.Context(), actor, TransactionalSendInput{
		Template:       req.Template,
		To:             req.To,
		IdempotencyKey: c.GetHeader(IdempotencyKeyHeader),
		Variables:      req.Variables,
	})
	if err != nil {
		respondTransactionalError(c, "Failed to send transactional message", err)
		return
	}
	privateResponse(c)
	c.Header("Location", transactionalLocation(message.ID))
	if created {
		response.Accepted(c, toTransactionalMessageResponse(message))
		return
	}
	response.Success(c, toTransactionalMessageResponse(message))
}

// GetTransactional reads one transactional message of the key's organization and mode.
func (h *Handler) GetTransactional(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	messageID, ok := httphandler.ParseID(c, "message_id")
	if !ok {
		return
	}
	message, err := h.service.GetTransactional(c.Request.Context(), viewer.OrganizationID, apikey.APIKeyModeFromContext(c), messageID)
	if err != nil {
		response.HandleError(c, "Failed to get transactional message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toTransactionalMessageResponse(message))
}

// ListTransactionalLog lists the organization's transactional messages for the console; any
// member may read it.
func (h *Handler) ListTransactionalLog(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	// URL.Query drops a pair it cannot decode, which would turn a malformed filter into none.
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		response.HandleError(c, "Invalid transactional log query", fmt.Errorf("%w: malformed query string", domain.ErrInvalidInput))
		return
	}
	filter, err := parseTransactionalLogQuery(query)
	if err != nil {
		response.HandleError(c, "Invalid transactional log query", err)
		return
	}
	page, err := h.service.ListTransactionalLog(c.Request.Context(), viewer.OrganizationID, filter)
	if err != nil {
		response.HandleError(c, "Failed to list transactional messages", err)
		return
	}
	privateResponse(c)
	response.SuccessWithMeta(c, toTransactionalLogEntries(page.Items), toTransactionalLogMeta(page))
}

// GetTransactionalLogEntry reads one transactional message of the organization, of either mode,
// with its timeline; any member may read it.
func (h *Handler) GetTransactionalLogEntry(c *gin.Context) {
	viewer, messageID, ok := viewerScope(c, "message_id")
	if !ok {
		return
	}
	message, err := h.service.GetTransactionalLogEntry(c.Request.Context(), viewer.OrganizationID, messageID)
	if err != nil {
		response.HandleError(c, "Failed to get transactional message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toTransactionalLogEntryDetail(message))
}

// parseTransactionalLogQuery reads the log's query parameters. Enumerations and ranges are
// checked by the service; this only rejects values that do not parse.
func parseTransactionalLogQuery(query url.Values) (domain.TransactionalLogFilter, error) {
	invalid := func(reason string) (domain.TransactionalLogFilter, error) {
		return domain.TransactionalLogFilter{}, fmt.Errorf("%w: %s", domain.ErrInvalidInput, reason)
	}
	filter := domain.TransactionalLogFilter{
		Mode:        domain.APIKeyMode(query.Get("mode")),
		Status:      query.Get("status"),
		LastEvent:   query.Get("last_event"),
		Outcome:     query.Get("outcome"),
		TemplateKey: query.Get("template"),
		To:          query.Get("to"),
	}
	for name, target := range map[string]*time.Time{"since": &filter.Since, "until": &filter.Until} {
		if raw := query.Get(name); raw != "" {
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return invalid(name + " is not a date-time")
			}
			*target = parsed.UTC()
		}
	}
	if raw := query.Get("cursor"); raw != "" {
		cursor, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 1 {
			return invalid("malformed cursor")
		}
		filter.Cursor = uint(cursor)
	}
	if raw, present := query["per_page"]; present {
		perPage, err := strconv.Atoi(raw[0])
		if err != nil || perPage < 1 {
			return invalid("per_page out of range")
		}
		filter.PerPage = perPage
	}
	return filter, nil
}

// SendTransactionalTest sends a transactional template to the signed-in member's own address.
func (h *Handler) SendTransactionalTest(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	if !h.service.TransactionalPolicy().Enabled {
		response.HandleError(c, "Transactional mail is disabled", ErrTransactionalDisabled)
		return
	}
	var req SendTransactionalTestRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, err := h.service.SendTransactionalTest(c.Request.Context(), viewer, TransactionalTestInput{
		Template:  req.Template,
		Mode:      domain.APIKeyMode(req.Mode),
		Variables: req.Variables,
	})
	if err != nil {
		respondTransactionalError(c, "Failed to send transactional test", err)
		return
	}
	privateResponse(c)
	response.Accepted(c, toTransactionalMessageResponse(message))
}

// respondTransactionalError answers field errors with their fields, missing variables under
// variables.<name>, and sets Retry-After on a rate limit.
func respondTransactionalError(c *gin.Context, message string, err error) {
	var retry *RetryAfterError
	if errors.As(err, &retry) {
		seconds := int(math.Ceil(retry.RetryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
	}
	var validation *ValidationError
	var fields *FieldsError
	var missing *MissingVariablesError
	switch {
	case errors.As(err, &validation):
		respondFields(c, domain.CodeValidationFailed, "Validation failed", validation.Fields)
	case errors.As(err, &missing):
		named := make(map[string][]string, len(missing.Names))
		for _, name := range missing.Names {
			named["variables."+name] = []string{"is required"}
		}
		respondFields(c, domain.CodeMailTemplateVariablesMissing, "Template variables are missing", named)
	case errors.As(err, &fields) && errors.Is(fields.Err, ErrMailMessageInvalid):
		respondFields(c, domain.CodeMailMessageInvalid, "Mail message is invalid", fields.Fields)
	default:
		response.HandleError(c, message, err)
	}
}

func respondFields(c *gin.Context, code, message string, fields map[string][]string) {
	c.JSON(http.StatusUnprocessableEntity, response.ValidationErrorResponse{
		Code:      http.StatusUnprocessableEntity,
		ErrorCode: code,
		Message:   message,
		Errors:    fields,
		RequestID: c.GetString("request_id"),
	})
}
