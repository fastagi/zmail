package agentgrant

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

const (
	// ScopeMailAgent is the API key scope required for agent feed and draft endpoints.
	ScopeMailAgent = "mail:agent"
	// maxAgentDraftBodyBytes bounds an agent draft request: two bodies of 100 KB and the envelope.
	maxAgentDraftBodyBytes = 2*domain.MaxMailAgentDraftBytes + 4096
)

// Handler serves the HTTP endpoints for the top-level agentgrant module.
type Handler struct {
	service domain.MailAgentGrantPort
}

var (
	_ assembly.Module      = (*Handler)(nil)
	_ assembly.RouteModule = (*Handler)(nil)
	_ assembly.ErrorModule = (*Handler)(nil)
)

// NewHandler builds an agentgrant HTTP handler backed by domain.MailAgentGrantPort.
func NewHandler(service domain.MailAgentGrantPort) *Handler {
	return &Handler{service: service}
}

// Name identifies this module inside the starter registry.
func (h *Handler) Name() string {
	return "agentgrant"
}

// IssueAgentGrant lets an API key receive the mailbox's new mail.
func (h *Handler) IssueAgentGrant(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req CreateAgentGrantRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	grant, err := h.service.IssueAgentGrantPort(c.Request.Context(), viewer, mailboxID, req.input())
	if err != nil {
		response.HandleError(c, "Failed to issue agent grant", err)
		return
	}
	privateResponse(c)
	response.Created(c, toAgentGrantResponse(grant, time.Now()))
}

// ListAgentGrants pages the mailbox's agent grants, newest first.
func (h *Handler) ListAgentGrants(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	grants, total, err := h.service.ListAgentGrantsPort(c.Request.Context(), viewer, mailboxID, page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list agent grants", err)
		return
	}
	now := time.Now()
	items := make([]*AgentGrantResponse, 0, len(grants))
	for _, grant := range grants {
		items = append(items, toAgentGrantResponse(grant, now))
	}
	paginator := pagination.NewPaginator(items, total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

// RevokeAgentGrant ends an agent grant at once.
func (h *Handler) RevokeAgentGrant(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	grantID, ok := httphandler.ParseID(c, "grant_id")
	if !ok {
		return
	}
	if err := h.service.RevokeAgentGrantPort(c.Request.Context(), viewer, mailboxID, grantID); err != nil {
		response.HandleError(c, "Failed to revoke agent grant", err)
		return
	}
	response.NoContent(c)
}

// AgentGrantActivity returns a grant's newest audited reads and drafts.
func (h *Handler) AgentGrantActivity(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	grantID, ok := httphandler.ParseID(c, "grant_id")
	if !ok {
		return
	}
	entries, err := h.service.AgentGrantActivityPort(c.Request.Context(), viewer, mailboxID, grantID)
	if err != nil {
		response.HandleError(c, "Failed to list agent grant activity", err)
		return
	}
	// fastbuild:bounded-list max=100 reason=the newest audit rows of one grant, capped by MaxMailAgentActivity
	items := make([]AgentAuditResponse, 0, len(entries))
	for _, entry := range entries {
		items = append(items, AgentAuditResponse{
			ID:        entry.ID,
			GrantID:   entry.GrantID,
			APIKeyID:  entry.APIKeyID,
			MessageID: entry.MessageID,
			Action:    string(entry.Action),
			CreatedAt: entry.CreatedAt,
		})
	}
	privateResponse(c)
	response.Success(c, items)
}

// ListAgentDrafts pages the mailbox's agent drafts for the people who may send from it.
func (h *Handler) ListAgentDrafts(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	drafts, total, err := h.service.ListAgentDraftsPort(c.Request.Context(), viewer, mailboxID, page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list agent drafts", err)
		return
	}
	items := make([]*AgentDraftResponse, 0, len(drafts))
	for _, draft := range drafts {
		items = append(items, toAgentDraftResponse(draft))
	}
	paginator := pagination.NewPaginator(items, total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

// ListAgentMailEvents returns the API key's feed after a cursor.
func (h *Handler) ListAgentMailEvents(c *gin.Context) {
	viewer, keyID, ok := agentScope(c)
	if !ok {
		return
	}
	after, limit, err := parseAgentFeedQuery(c)
	if err != nil {
		response.HandleError(c, "Invalid agent mail feed query", err)
		return
	}
	feed, err := h.service.AgentMailEventsPort(c.Request.Context(), viewer, keyID, after, limit)
	if err != nil {
		respondAgentError(c, "Failed to read agent mail events", err)
		return
	}
	// fastbuild:bounded-list max=100 reason=one feed page, capped by MaxMailAgentFeedPage
	events := make([]AgentMailEventResponse, 0, len(feed.Events))
	for _, event := range feed.Events {
		events = append(events, AgentMailEventResponse{
			ID:        event.ID,
			Type:      string(event.Type),
			GrantID:   event.GrantID,
			MailboxID: event.MailboxID,
			MessageID: event.MessageID,
			CreatedAt: event.CreatedAt,
		})
	}
	privateResponse(c)
	response.Success(c, AgentMailFeedResponse{
		Events:     events,
		NextCursor: strconv.FormatUint(uint64(feed.NextCursor), 10),
		HasMore:    feed.HasMore,
	})
}

// CreateAgentDraft stores a reply draft for a person to review; it never sends.
func (h *Handler) CreateAgentDraft(c *gin.Context) {
	viewer, keyID, ok := agentScope(c)
	if !ok {
		return
	}
	var req CreateAgentDraftRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	draft, created, err := h.service.SaveAgentDraftPort(c.Request.Context(), viewer, keyID, domain.MailAgentDraftInput{
		MessageID: req.MessageID,
		Text:      req.Text,
		HTML:      req.HTML,
	})
	if err != nil {
		response.HandleError(c, "Failed to save agent draft", err)
		return
	}
	privateResponse(c)
	if created {
		response.Created(c, toAgentDraftResponse(draft))
		return
	}
	response.Success(c, toAgentDraftResponse(draft))
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

func mailboxScope(c *gin.Context) (domain.OrganizationContext, uint, bool) {
	viewer, ok := organizationScope(c)
	if !ok {
		return domain.OrganizationContext{}, 0, false
	}
	mailboxID, ok := httphandler.ParseID(c, "mailbox_id")
	if !ok {
		return domain.OrganizationContext{}, 0, false
	}
	return viewer, mailboxID, true
}

func agentScope(c *gin.Context) (domain.OrganizationContext, uint, bool) {
	viewer, ok := organizationScope(c)
	if !ok {
		return domain.OrganizationContext{}, 0, false
	}
	value, exists := c.Get("apiKeyID")
	keyID, isID := value.(uint)
	if !exists || !isID || keyID == 0 {
		response.AbortUnauthorized(c, "API key authentication required")
		return domain.OrganizationContext{}, 0, false
	}
	return viewer, keyID, true
}

func parseAgentFeedQuery(c *gin.Context) (uint, int, error) {
	var after uint64
	if raw := c.Query("after"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 63)
		if err != nil {
			return 0, 0, fmt.Errorf("%w: after must be a cursor from next_cursor", domain.ErrInvalidInput)
		}
		after = parsed
	}
	limit := domain.DefaultMailAgentFeedPage
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > domain.MaxMailAgentFeedPage {
			return 0, 0, fmt.Errorf("%w: limit must be between 1 and %d", domain.ErrInvalidInput, domain.MaxMailAgentFeedPage)
		}
		limit = parsed
	}
	return uint(after), limit, nil
}

func respondAgentError(c *gin.Context, message string, err error) {
	var retry interface{ RetryAfterDuration() time.Duration }
	if errors.As(err, &retry) {
		c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.RetryAfterDuration().Seconds())))))
	}
	response.HandleError(c, message, err)
}

func privateResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization, Organization-Id")
}

func requireFeature(features ...domain.MemberFeature) gin.HandlerFunc {
	if len(features) == 0 {
		panic("agentgrant: requireFeature needs at least one feature")
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
