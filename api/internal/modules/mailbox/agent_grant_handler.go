package mailbox

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// maxAgentDraftBodyBytes bounds an agent draft request: two bodies of 100 KB and the envelope.
const maxAgentDraftBodyBytes = 2*domain.MaxMailAgentDraftBytes + 4096

// CreateAgentGrantRequest issues an agent grant.
type CreateAgentGrantRequest struct {
	APIKeyID      uint                      `json:"api_key_id" binding:"required"`
	Capabilities  []string                  `json:"capabilities" binding:"required,min=1,max=2"`
	RevealSecrets bool                      `json:"reveal_secrets"`
	Filters       *AgentGrantFiltersRequest `json:"filters"`
	ExpiresAt     time.Time                 `json:"expires_at" binding:"required"`
}

// AgentGrantFiltersRequest narrows a grant to some senders or labels.
type AgentGrantFiltersRequest struct {
	Senders  []string `json:"senders" binding:"max=20"`
	LabelIDs []uint   `json:"label_ids" binding:"max=20"`
}

func (r CreateAgentGrantRequest) input() IssueAgentGrantInput {
	input := IssueAgentGrantInput{
		APIKeyID: r.APIKeyID, Capabilities: r.Capabilities, RevealSecrets: r.RevealSecrets, ExpiresAt: r.ExpiresAt,
	}
	if r.Filters != nil {
		input.Senders, input.LabelIDs = r.Filters.Senders, r.Filters.LabelIDs
	}
	return input
}

// AgentGrantFiltersResponse is a grant's filters; empty lists match everything.
type AgentGrantFiltersResponse struct {
	Senders  []string `json:"senders"`
	LabelIDs []uint   `json:"label_ids"`
}

// AgentGrantResponse is one agent grant.
type AgentGrantResponse struct {
	ID            uint                      `json:"id"`
	MailboxID     uint                      `json:"mailbox_id"`
	APIKeyID      uint                      `json:"api_key_id"`
	APIKeyName    string                    `json:"api_key_name"`
	APIKeyPrefix  string                    `json:"api_key_prefix"`
	KeyUserID     uint                      `json:"key_user_id"`
	GrantedBy     uint                      `json:"granted_by"`
	Capabilities  []string                  `json:"capabilities"`
	RevealSecrets bool                      `json:"reveal_secrets"`
	Filters       AgentGrantFiltersResponse `json:"filters"`
	Status        string                    `json:"status"`
	ExpiresAt     time.Time                 `json:"expires_at"`
	RevokedAt     *time.Time                `json:"revoked_at"`
	RevokeReason  *string                   `json:"revoke_reason"`
	CreatedAt     time.Time                 `json:"created_at"`
}

func toAgentGrantResponse(grant *domain.MailAgentGrant, now time.Time) *AgentGrantResponse {
	response := &AgentGrantResponse{
		ID: grant.ID, MailboxID: grant.MailboxID, APIKeyID: grant.APIKeyID, APIKeyName: grant.APIKeyName,
		APIKeyPrefix: grant.APIKeyPrefix, KeyUserID: grant.KeyUserID, GrantedBy: grant.GrantedBy,
		Capabilities: grant.Capabilities(), RevealSecrets: grant.RevealSecrets,
		Filters:   AgentGrantFiltersResponse{Senders: agentStrings(grant.SenderFilters), LabelIDs: grant.LabelIDs},
		Status:    string(grant.Status(now)),
		ExpiresAt: grant.ExpiresAt, RevokedAt: grant.RevokedAt, CreatedAt: grant.CreatedAt,
	}
	if response.Filters.LabelIDs == nil {
		response.Filters.LabelIDs = []uint{}
	}
	if grant.RevokedAt != nil {
		reason := string(grant.RevokeReason)
		response.RevokeReason = &reason
	}
	return response
}

// AgentAuditResponse is one audited agent read or draft.
type AgentAuditResponse struct {
	ID        uint      `json:"id"`
	GrantID   uint      `json:"grant_id"`
	APIKeyID  uint      `json:"api_key_id"`
	MessageID uint      `json:"message_id"`
	Action    string    `json:"action"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentDraftResponse is a reply draft awaiting a person.
type AgentDraftResponse struct {
	ID        uint      `json:"id"`
	MailboxID uint      `json:"mailbox_id"`
	GrantID   uint      `json:"grant_id"`
	MessageID uint      `json:"message_id"`
	APIKeyID  uint      `json:"api_key_id"`
	Text      string    `json:"text"`
	HTML      string    `json:"html"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toAgentDraftResponse(draft *domain.MailAgentDraft) *AgentDraftResponse {
	return &AgentDraftResponse{
		ID: draft.ID, MailboxID: draft.MailboxID, GrantID: draft.GrantID, MessageID: draft.MessageID,
		APIKeyID: draft.APIKeyID, Text: draft.TextBody, HTML: draft.HTMLBody, Status: draft.Status,
		CreatedAt: draft.CreatedAt, UpdatedAt: draft.UpdatedAt,
	}
}

// AgentMailEventResponse is one feed event.
type AgentMailEventResponse struct {
	ID        uint      `json:"id"`
	Type      string    `json:"type"`
	GrantID   uint      `json:"grant_id"`
	MailboxID uint      `json:"mailbox_id"`
	MessageID uint      `json:"message_id"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentMailFeedResponse is one page of the feed.
type AgentMailFeedResponse struct {
	Events     []AgentMailEventResponse `json:"events"`
	NextCursor string                   `json:"next_cursor"`
	HasMore    bool                     `json:"has_more"`
}

// CreateAgentDraftRequest stores an agent's reply draft.
type CreateAgentDraftRequest struct {
	MessageID uint   `json:"message_id" binding:"required"`
	Text      string `json:"text"` // blank is MAILBOX.MESSAGE_INVALID, checked by the service
	HTML      string `json:"html"`
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
	grant, err := h.service.IssueAgentGrant(c.Request.Context(), viewer, mailboxID, req.input())
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
	grants, total, err := h.service.ListAgentGrants(c.Request.Context(), viewer, mailboxID, page.GetPage(), page.GetPerPage())
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
	if err := h.service.RevokeAgentGrant(c.Request.Context(), viewer, mailboxID, grantID); err != nil {
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
	entries, err := h.service.AgentGrantActivity(c.Request.Context(), viewer, mailboxID, grantID)
	if err != nil {
		response.HandleError(c, "Failed to list agent grant activity", err)
		return
	}
	// fastbuild:bounded-list max=100 reason=the newest audit rows of one grant, capped by MaxMailAgentActivity
	items := make([]AgentAuditResponse, 0, len(entries))
	for _, entry := range entries {
		items = append(items, AgentAuditResponse{
			ID: entry.ID, GrantID: entry.GrantID, APIKeyID: entry.APIKeyID, MessageID: entry.MessageID,
			Action: string(entry.Action), CreatedAt: entry.CreatedAt,
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
	drafts, total, err := h.service.ListAgentDrafts(c.Request.Context(), viewer, mailboxID, page.GetPage(), page.GetPerPage())
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

// respondAgentError sets Retry-After when the key used up its requests, then answers the error.
func respondAgentError(c *gin.Context, message string, err error) {
	var retry *RetryAfterError
	if errors.As(err, &retry) {
		c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.RetryAfter.Seconds())))))
	}
	response.HandleError(c, message, err)
}

// agentScope is the key owner's verified membership and the authenticated key's id.
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

// parseAgentFeedQuery reads after (a cursor from next_cursor, default 0) and limit (1 to 100,
// default 50).
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

// ListAgentMailEvents returns the key's feed after a cursor. The same cursor returns the same
// events while the grants stay as they are.
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
	feed, err := h.service.AgentMailEvents(c.Request.Context(), viewer, keyID, after, limit)
	if err != nil {
		respondAgentError(c, "Failed to read agent mail events", err)
		return
	}
	// fastbuild:bounded-list max=100 reason=one feed page, capped by MaxMailAgentFeedPage
	events := make([]AgentMailEventResponse, 0, len(feed.Events))
	for _, event := range feed.Events {
		events = append(events, AgentMailEventResponse{
			ID: event.ID, Type: string(event.Type), GrantID: event.GrantID, MailboxID: event.MailboxID,
			MessageID: event.MessageID, CreatedAt: event.CreatedAt,
		})
	}
	privateResponse(c)
	response.Success(c, AgentMailFeedResponse{
		Events: events, NextCursor: strconv.FormatUint(uint64(feed.NextCursor), 10), HasMore: feed.HasMore,
	})
}

// GetAgentMailMessage returns one message the key's grants received, masked unless the grant
// reveals secrets. Nobody's read state changes.
func (h *Handler) GetAgentMailMessage(c *gin.Context) {
	viewer, keyID, ok := agentScope(c)
	if !ok {
		return
	}
	messageID, ok := httphandler.ParseID(c, "message_id")
	if !ok {
		return
	}
	read, err := h.service.AgentMailMessage(c.Request.Context(), viewer, keyID, messageID)
	if err != nil {
		respondAgentError(c, "Failed to get agent mail message", err)
		return
	}
	privateResponse(c)
	if read.Revealed {
		response.Success(c, toRevealedMailMessageResponse(read.Message))
		return
	}
	response.Success(c, toMailMessageResponse(read.Message))
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
	draft, created, err := h.service.SaveAgentDraft(c.Request.Context(), viewer, keyID, AgentDraftInput(req))
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
