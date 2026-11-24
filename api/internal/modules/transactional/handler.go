package transactional

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
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

const (
	// ScopeMailTransactional lets an API key send and read transactional messages.
	ScopeMailTransactional = "mail:transactional"
	// IdempotencyKeyHeader is the HTTP header carrying the caller's idempotency key.
	IdempotencyKeyHeader = "Idempotency-Key"
	// maxTransactionalBodyBytes bounds a transactional send request.
	maxTransactionalBodyBytes = 64 << 10
	suppressionMaxPerPage     = 100
)

// Handler serves the HTTP endpoints for the top-level transactional module.
type Handler struct {
	service domain.MailTransactionalPort
}

var (
	_ assembly.Module      = (*Handler)(nil)
	_ assembly.RouteModule = (*Handler)(nil)
	_ assembly.ErrorModule = (*Handler)(nil)
)

// NewHandler builds a transactional HTTP handler backed by domain.MailTransactionalPort.
func NewHandler(service domain.MailTransactionalPort) *Handler {
	return &Handler{service: service}
}

// Name identifies this module inside the starter registry.
func (h *Handler) Name() string {
	return "transactional"
}

// SendTransactional sends one transactional message for an API key with mail:transactional.
func (h *Handler) SendTransactional(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	if !h.service.TransactionalEnabledPort() {
		response.HandleError(c, "Transactional mail is disabled", domain.ErrTransactionalDisabled)
		return
	}
	var req SendTransactionalMessageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	actor := domain.MailTransactionalActor{
		OrganizationID: viewer.OrganizationID,
		UserID:         viewer.UserID,
		APIKeyID:       apiKeyID(c),
		Mode:           apikey.APIKeyModeFromContext(c),
		Viewer:         viewer,
	}
	message, created, err := h.service.SendTransactionalPort(c.Request.Context(), actor, domain.MailTransactionalSendInput{
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
	c.Header("Location", "/v1/transactional-messages/"+strconv.FormatUint(uint64(message.ID), 10))
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
	message, err := h.service.GetTransactionalPort(c.Request.Context(), viewer.OrganizationID, apikey.APIKeyModeFromContext(c), messageID)
	if err != nil {
		response.HandleError(c, "Failed to get transactional message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toTransactionalMessageResponse(message))
}

// ListTransactionalLog lists the organization's transactional messages for the console.
func (h *Handler) ListTransactionalLog(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
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
	page, err := h.service.ListTransactionalLogPort(c.Request.Context(), viewer.OrganizationID, filter)
	if err != nil {
		response.HandleError(c, "Failed to list transactional messages", err)
		return
	}
	privateResponse(c)
	response.SuccessWithMeta(c, toTransactionalLogEntries(page.Items), toTransactionalLogMeta(page))
}

// GetTransactionalLogEntry reads one transactional message of the organization with its timeline.
func (h *Handler) GetTransactionalLogEntry(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	messageID, ok := httphandler.ParseID(c, "message_id")
	if !ok {
		return
	}
	message, err := h.service.GetTransactionalLogEntryPort(c.Request.Context(), viewer.OrganizationID, messageID)
	if err != nil {
		response.HandleError(c, "Failed to get transactional message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toTransactionalLogEntryDetail(message))
}

// SendTransactionalTest sends a transactional template to the signed-in member's own address.
func (h *Handler) SendTransactionalTest(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	if !h.service.TransactionalEnabledPort() {
		response.HandleError(c, "Transactional mail is disabled", domain.ErrTransactionalDisabled)
		return
	}
	var req SendTransactionalTestRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, err := h.service.SendTransactionalTestPort(c.Request.Context(), viewer, domain.MailTransactionalTestInput{
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

// ListSendLanes shows owners, admins, and developers how full each send lane is.
func (h *Handler) ListSendLanes(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	lanes, computedAt, err := h.service.ListSendLanesPort(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to list send lanes", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailSendLaneUsageList(lanes, computedAt))
}

// ListSuppressions lists the organization's suppression list.
func (h *Handler) ListSuppressions(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		response.HandleError(c, "Invalid suppression list query", fmt.Errorf("%w: malformed query string", domain.ErrInvalidInput))
		return
	}
	filter, err := parseSuppressionQuery(query)
	if err != nil {
		response.HandleError(c, "Invalid suppression list query", err)
		return
	}
	suppressions, next, err := h.service.ListSuppressionsPort(c.Request.Context(), viewer, filter)
	if err != nil {
		response.HandleError(c, "Failed to list suppressions", err)
		return
	}
	privateResponse(c)
	response.SuccessWithMeta(c, toMailSuppressions(suppressions), toMailSuppressionPageMeta(next))
}

// RemoveSuppression takes an address off the suppression list.
func (h *Handler) RemoveSuppression(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	suppressionID, ok := httphandler.ParseID(c, "suppression_id")
	if !ok {
		return
	}
	if err := h.service.RemoveSuppressionPort(c.Request.Context(), viewer, suppressionID); err != nil {
		response.HandleError(c, "Failed to remove suppression", err)
		return
	}
	c.Status(http.StatusNoContent)
}

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

func parseSuppressionQuery(query url.Values) (domain.MailSuppressionFilter, error) {
	invalid := func(reason string) (domain.MailSuppressionFilter, error) {
		return domain.MailSuppressionFilter{}, fmt.Errorf("%w: %s", domain.ErrInvalidInput, reason)
	}
	filter := domain.MailSuppressionFilter{Email: query.Get("email"), Reason: query.Get("reason")}
	if raw := query.Get("cursor"); raw != "" {
		cursor, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 1 {
			return invalid("malformed cursor")
		}
		filter.Cursor = uint(cursor)
	}
	if raw, present := query["per_page"]; present {
		perPage, err := strconv.Atoi(raw[0])
		if err != nil || perPage < 1 || perPage > suppressionMaxPerPage {
			return invalid("per_page out of range")
		}
		filter.PerPage = perPage
	}
	return filter, nil
}

func respondTransactionalError(c *gin.Context, message string, err error) {
	var retry interface{ RetryAfterDuration() time.Duration }
	if errors.As(err, &retry) {
		seconds := int(math.Ceil(retry.RetryAfterDuration().Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
	}
	var validation interface{ ValidationFields() map[string][]string }
	var missing interface{ MissingVariableNames() []string }
	var fields interface{ FieldErrors() (error, map[string][]string) }
	switch {
	case errors.As(err, &validation):
		respondFields(c, domain.CodeValidationFailed, "Validation failed", validation.ValidationFields())
	case errors.As(err, &missing):
		names := missing.MissingVariableNames()
		named := make(map[string][]string, len(names))
		for _, name := range names {
			named["variables."+name] = []string{"is required"}
		}
		respondFields(c, domain.CodeMailTemplateVariablesMissing, "Template variables are missing", named)
	case errors.As(err, &fields):
		inner, fieldMap := fields.FieldErrors()
		if errors.Is(inner, domain.ErrMailMessageInvalid) {
			respondFields(c, domain.CodeMailMessageInvalid, "Mail message is invalid", fieldMap)
			return
		}
		response.HandleError(c, message, err)
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

func organizationScope(c *gin.Context) (domain.OrganizationContext, bool) {
	viewer, ok := domain.OrganizationContextFromContext(c.Request.Context())
	if !ok {
		response.AbortWithCode(c, http.StatusBadRequest, domain.CodeOrganizationContextRequired,
			"Organization context required")
		return domain.OrganizationContext{}, false
	}
	return viewer, true
}

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

func privateResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization, Organization-Id")
}

func requireFeature(features ...domain.MemberFeature) gin.HandlerFunc {
	if len(features) == 0 {
		panic("transactional: requireFeature needs at least one feature")
	}
	return func(c *gin.Context) {
		viewer, ok := domain.OrganizationContextFromContext(c.Request.Context())
		if !ok {
			response.AbortWithCode(c, http.StatusBadRequest, domain.CodeOrganizationContextRequired,
				"Organization context required")
			return
		}
		for _, feature := range features {
			if viewer.CanUse(feature) {
				c.Next()
				return
			}
		}
		response.AbortWithCode(c, http.StatusForbidden, domain.CodeOrganizationFeatureNotGranted,
			"An organization owner or admin has not granted you this feature")
	}
}
