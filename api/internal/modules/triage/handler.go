package triage

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// Handler serves the HTTP endpoints for the top-level triage module.
type Handler struct {
	service domain.MailTriagePort
}

var (
	_ assembly.Module      = (*Handler)(nil)
	_ assembly.RouteModule = (*Handler)(nil)
	_ assembly.ErrorModule = (*Handler)(nil)
)

// NewHandler builds a triage HTTP handler backed by the hexagonal domain.MailTriagePort.
func NewHandler(service domain.MailTriagePort) *Handler {
	return &Handler{service: service}
}

// Name identifies this module inside the starter registry.
func (h *Handler) Name() string {
	return "triage"
}

// GetTriageSetting returns the organization's AI triage switches to any member.
func (h *Handler) GetTriageSetting(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	setting, err := h.service.GetTriageSettingPort(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to read the AI triage setting", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailTriageSettingResponse(setting))
}

// UpdateTriageSetting replaces the organization's AI triage switches; owners and admins only.
func (h *Handler) UpdateTriageSetting(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req UpdateMailTriageSettingRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	setting, err := h.service.SaveTriageSettingPort(c.Request.Context(), viewer, domain.MailTriageSettingInput{
		Enabled:         *req.Enabled,
		DailyMessageCap: *req.DailyMessageCap,
		DraftEnabled:    *req.DraftEnabled,
	})
	if err != nil {
		respondTriageError(c, "Failed to change the AI triage setting", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailTriageSettingResponse(setting))
}

// UpdateMessageTriage records a reader's decision on an inbound message.
func (h *Handler) UpdateMessageTriage(c *gin.Context) {
	viewer, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	var req UpdateMailTriageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	triage, err := h.service.OverrideTriagePort(c.Request.Context(), viewer, mailboxID, messageID, domain.MailTriageOverrideInput{
		Category: domain.MailTriageCategory(req.Category),
		DueAt:    req.DueAt,
	})
	if err != nil {
		respondTriageError(c, "Failed to change the message triage", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailTriageResponse(triage))
}

// CreateReplyDraft drafts a reply to an inbound message. The draft is returned once and never stored.
func (h *Handler) CreateReplyDraft(c *gin.Context) {
	viewer, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	draft, err := h.service.DraftReplyPort(c.Request.Context(), viewer, mailboxID, messageID)
	if err != nil {
		respondTriageError(c, "Failed to draft a reply", err)
		return
	}
	basis := make([]ReplyDraftBasisDTO, 0, len(draft.Basis))
	for _, item := range draft.Basis {
		basis = append(basis, ReplyDraftBasisDTO{
			Kind:  item.Kind,
			ID:    item.ID,
			Label: item.Label,
		})
	}
	privateResponse(c)
	response.Success(c, ReplyDraftResponse{Body: draft.Body, Basis: basis})
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

func messageScope(c *gin.Context) (domain.OrganizationContext, uint, uint, bool) {
	viewer, ok := organizationScope(c)
	if !ok {
		return domain.OrganizationContext{}, 0, 0, false
	}
	mailboxID, ok := httphandler.ParseID(c, "mailbox_id")
	if !ok {
		return domain.OrganizationContext{}, 0, 0, false
	}
	messageID, ok := httphandler.ParseID(c, "message_id")
	if !ok {
		return domain.OrganizationContext{}, 0, 0, false
	}
	return viewer, mailboxID, messageID, true
}

func privateResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization, Organization-Id")
}

func respondTriageError(c *gin.Context, message string, err error) {
	var fieldErr interface{ ValidationFields() map[string][]string }
	if errors.As(err, &fieldErr) {
		c.JSON(http.StatusUnprocessableEntity, response.ValidationErrorResponse{
			Code:      http.StatusUnprocessableEntity,
			ErrorCode: domain.CodeValidationFailed,
			Message:   "Validation failed",
			Errors:    fieldErr.ValidationFields(),
			RequestID: c.GetString("request_id"),
		})
		return
	}
	response.HandleError(c, message, err)
}
