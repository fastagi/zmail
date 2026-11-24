package broadcast

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// Handler serves the HTTP endpoints for the top-level broadcast module.
type Handler struct {
	service domain.MailBroadcastPort
}

var (
	_ assembly.Module      = (*Handler)(nil)
	_ assembly.RouteModule = (*Handler)(nil)
	_ assembly.ErrorModule = (*Handler)(nil)
)

// NewHandler builds a broadcast HTTP handler backed by domain.MailBroadcastPort.
func NewHandler(service domain.MailBroadcastPort) *Handler {
	return &Handler{service: service}
}

// Name identifies this module inside the starter registry.
func (h *Handler) Name() string {
	return "broadcast"
}

func (h *Handler) ListBroadcasts(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	broadcasts, total, err := h.service.ListBroadcastsPort(c.Request.Context(), viewer, page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list mail broadcasts", err)
		return
	}
	items := make([]MailBroadcastResponse, 0, len(broadcasts))
	for _, b := range broadcasts {
		items = append(items, toMailBroadcastResponse(b))
	}
	paginator := pagination.NewPaginator(items, total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

func (h *Handler) GetBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	b, err := h.service.GetBroadcastPort(c.Request.Context(), viewer, id)
	if err != nil {
		respondBroadcast(c, b, err, false)
		return
	}
	progress, err := h.service.BroadcastProgressPort(c.Request.Context(), b)
	if err != nil {
		response.HandleError(c, "Failed to estimate mail broadcast progress", err)
		return
	}
	body := toMailBroadcastResponse(b)
	body.Progress = toMailBroadcastProgressResponse(progress)
	privateResponse(c)
	response.Success(c, body)
}

func (h *Handler) CreateBroadcast(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req MailBroadcastRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	b, err := h.service.CreateBroadcastPort(c.Request.Context(), viewer, req.input())
	respondBroadcast(c, b, err, true)
}

func (h *Handler) UpdateBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	var req MailBroadcastRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	b, err := h.service.UpdateBroadcastPort(c.Request.Context(), viewer, id, req.input())
	respondBroadcast(c, b, err, false)
}

func (h *Handler) DeleteBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	if err := h.service.DeleteBroadcastPort(c.Request.Context(), viewer, id); err != nil {
		response.HandleError(c, "Failed to delete mail broadcast", err)
		return
	}
	privateResponse(c)
	response.NoContent(c)
}

func (h *Handler) SendBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	var req MailBroadcastSendRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	b, err := h.service.SendBroadcastPort(c.Request.Context(), viewer, id, req.ScheduledAt)
	if err != nil && respondMissingVariablesAt(c, err, "variables.") {
		return
	}
	respondBroadcast(c, b, err, false)
}

func (h *Handler) CancelBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	b, visible, err := h.service.CancelBroadcastPort(c.Request.Context(), viewer, id)
	respondStoppedBroadcast(c, b, visible, err)
}

func (h *Handler) PauseBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	b, visible, err := h.service.PauseBroadcastPort(c.Request.Context(), viewer, id)
	respondStoppedBroadcast(c, b, visible, err)
}

func (h *Handler) ResumeBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	b, err := h.service.ResumeBroadcastPort(c.Request.Context(), viewer, id)
	respondBroadcast(c, b, err, false)
}

func (h *Handler) ListBroadcastRecipients(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	recipients, total, err := h.service.ListBroadcastRecipientsPort(c.Request.Context(), viewer, id,
		domain.MailRecipientStatus(c.Query("status")), page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list mail broadcast recipients", err)
		return
	}
	items := make([]MailBroadcastRecipientResponse, 0, len(recipients))
	for _, recipient := range recipients {
		item := MailBroadcastRecipientResponse{
			ID:          recipient.ID,
			ContactID:   recipient.ContactID,
			Email:       recipient.Email,
			Status:      string(recipient.Status),
			FailureCode: recipient.FailureCode,
			LastEvent:   string(recipient.LastEvent),
			UpdatedAt:   recipient.UpdatedAt,
		}
		if recipient.ContactStatus != "" {
			contactStatus := string(recipient.ContactStatus)
			item.ContactStatus = &contactStatus
		}
		if recipient.MessageID != 0 {
			messageID := recipient.MessageID
			item.MessageID = &messageID
		}
		items = append(items, item)
	}
	paginator := pagination.NewPaginator(items, total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

// Unsubscribe applies a signed unsubscribe link without requiring a session.
func (h *Handler) Unsubscribe(c *gin.Context) {
	if err := h.service.UnsubscribePort(c.Request.Context(), c.Param("token")); err != nil {
		response.HandleError(c, "Failed to unsubscribe", err)
		return
	}
	privateResponse(c)
	response.NoContent(c)
}

func respondStoppedBroadcast(c *gin.Context, b *domain.MailBroadcast, visible bool, err error) {
	if err != nil || visible {
		respondBroadcast(c, b, err, false)
		return
	}
	privateResponse(c)
	response.NoContent(c)
}

func respondBroadcast(c *gin.Context, b *domain.MailBroadcast, err error, created bool) {
	if err != nil {
		response.HandleError(c, "Failed to handle mail broadcast", err)
		return
	}
	privateResponse(c)
	if created {
		response.Created(c, toMailBroadcastResponse(b))
		return
	}
	response.Success(c, toMailBroadcastResponse(b))
}

func respondMissingVariablesAt(c *gin.Context, err error, prefix string) bool {
	var missing interface{ MissingVariableNames() []string }
	if !errors.As(err, &missing) {
		return false
	}
	names := missing.MissingVariableNames()
	fields := make(map[string][]string, len(names))
	for _, name := range names {
		fields[prefix+name] = []string{"is required"}
	}
	c.JSON(http.StatusUnprocessableEntity, response.ValidationErrorResponse{
		Code:      http.StatusUnprocessableEntity,
		ErrorCode: domain.CodeMailTemplateVariablesMissing,
		Message:   "Template variables are missing",
		Errors:    fields,
		RequestID: c.GetString("request_id"),
	})
	return true
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

func viewerScope(c *gin.Context, param string) (domain.OrganizationContext, uint, bool) {
	viewer, ok := organizationScope(c)
	if !ok {
		return domain.OrganizationContext{}, 0, false
	}
	id, ok := httphandler.ParseID(c, param)
	return viewer, id, ok
}

func privateResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization, Organization-Id")
}

func requireFeature(features ...domain.MemberFeature) gin.HandlerFunc {
	if len(features) == 0 {
		panic("broadcast: requireFeature needs at least one feature")
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
