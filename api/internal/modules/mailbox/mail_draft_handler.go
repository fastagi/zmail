package mailbox

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// SaveMailDraftRequest is what a draft holds; it replaces all of it.
type SaveMailDraftRequest struct {
	MailboxID uint   `json:"mailbox_id" binding:"required,min=1"`
	To        string `json:"to" binding:"max=4000"`
	Cc        string `json:"cc" binding:"max=4000"`
	Subject   string `json:"subject" binding:"max=998"`
	Body      string `json:"body" binding:"max=100000"`
	InReplyTo string `json:"in_reply_to" binding:"max=998"`
}

func (r SaveMailDraftRequest) input() MailDraftInput {
	return MailDraftInput{MailboxID: r.MailboxID, To: r.To, Cc: r.Cc, Subject: r.Subject, Body: r.Body, InReplyTo: r.InReplyTo}
}

// MailDraftResponse is one of the caller's drafts.
type MailDraftResponse struct {
	ID        uint      `json:"id"`
	MailboxID uint      `json:"mailbox_id"`
	To        string    `json:"to"`
	Cc        string    `json:"cc"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	InReplyTo string    `json:"in_reply_to"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toMailDraftResponse(draft domain.MailDraft) MailDraftResponse {
	return MailDraftResponse{
		ID: draft.ID, MailboxID: draft.MailboxID, To: draft.To, Cc: draft.Cc, Subject: draft.Subject,
		Body: draft.Body, InReplyTo: draft.InReplyTo, CreatedAt: draft.CreatedAt, UpdatedAt: draft.UpdatedAt,
	}
}

// ListMailDrafts lists the caller's drafts in the organization, the latest change first.
// fastbuild:bounded-list max=200 reason=a member keeps at most domain.MailDraftsPerMember drafts per organization
func (h *Handler) ListMailDrafts(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	drafts, err := h.service.MailDrafts(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to list drafts", err)
		return
	}
	items := make([]MailDraftResponse, 0, len(drafts))
	for _, draft := range drafts {
		items = append(items, toMailDraftResponse(draft))
	}
	privateResponse(c)
	response.Success(c, items)
}

// CreateMailDraft saves a new draft.
func (h *Handler) CreateMailDraft(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req SaveMailDraftRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	draft, err := h.service.CreateMailDraft(c.Request.Context(), viewer, req.input())
	if err != nil {
		respondOrganizeError(c, "Failed to save the draft", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailDraftResponse(draft))
}

// UpdateMailDraft replaces what one of the caller's drafts holds.
func (h *Handler) UpdateMailDraft(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "draft_id")
	if !ok {
		return
	}
	var req SaveMailDraftRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	draft, err := h.service.UpdateMailDraft(c.Request.Context(), viewer, id, req.input())
	if err != nil {
		respondOrganizeError(c, "Failed to save the draft", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailDraftResponse(draft))
}

// DeleteMailDraft removes one of the caller's drafts.
func (h *Handler) DeleteMailDraft(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "draft_id")
	if !ok {
		return
	}
	if err := h.service.DeleteMailDraft(c.Request.Context(), viewer, id); err != nil {
		response.HandleError(c, "Failed to delete the draft", err)
		return
	}
	response.NoContent(c)
}
