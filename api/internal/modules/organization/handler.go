package organization

import (
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/modules/user"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	"github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// Handler handles organization HTTP requests and active-starter hooks.
type Handler struct {
	service         Service
	contextResolver *ContextResolver
	deletionGuard   user.AccountDeletionGuard
	deletionPolicy  *user.AccountDeletionPolicy
}

var (
	_ assembly.Module           = (*Handler)(nil)
	_ assembly.RouteModule      = (*Handler)(nil)
	_ assembly.MiddlewareModule = (*Handler)(nil)
	_ assembly.ActivationModule = (*Handler)(nil)
)

// NewHandler creates an organization handler.
func NewHandler(
	service *service,
	contextResolver *ContextResolver,
	deletionPolicy *user.AccountDeletionPolicy,
) *Handler {
	return &Handler{
		service:         service,
		contextResolver: contextResolver,
		deletionGuard:   service,
		deletionPolicy:  deletionPolicy,
	}
}

// Name returns the starter module name.
func (h *Handler) Name() string {
	return config.StarterOrganization
}

// Activate installs ownership protection only when this optional starter is selected.
func (h *Handler) Activate() error {
	return h.deletionPolicy.Register(h.deletionGuard)
}

// Create creates an organization owned by the authenticated user.
func (h *Handler) Create(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}

	var req CreateOrganizationRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	if fieldErrors := req.validationErrors(); len(fieldErrors) > 0 {
		response.ValidationFailed(c, fieldErrors)
		return
	}

	membership, err := h.service.Create(c.Request.Context(), userID, &req)
	if err != nil {
		response.HandleError(c, "Failed to create organization", err)
		return
	}
	response.Created(c, toResponse(membership))
}

// List returns organizations visible through the caller's memberships.
func (h *Handler) List(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}

	page := pagination.FromContext(c)
	memberships, total, err := h.service.List(c.Request.Context(), userID, page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list organizations", err)
		return
	}

	items := make([]*OrganizationResponse, len(memberships))
	for i, membership := range memberships {
		items[i] = toResponse(membership)
	}
	paginator := pagination.NewPaginator(items, total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	response.Success(c, paginator)
}

// Get returns one membership-scoped organization.
func (h *Handler) Get(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	organizationID, ok := handler.ParseID(c, "id")
	if !ok {
		return
	}

	membership, err := h.service.Get(c.Request.Context(), userID, organizationID)
	if err != nil {
		response.HandleError(c, "Organization not found", err)
		return
	}
	response.Success(c, toResponse(membership))
}

// Update changes mutable organization settings.
func (h *Handler) Update(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	organizationID, ok := handler.ParseID(c, "id")
	if !ok {
		return
	}

	var req UpdateOrganizationRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	if fieldErrors := req.validationErrors(); len(fieldErrors) > 0 {
		response.ValidationFailed(c, fieldErrors)
		return
	}

	membership, err := h.service.Update(c.Request.Context(), userID, organizationID, &req)
	if err != nil {
		response.HandleError(c, "Failed to update organization", err)
		return
	}
	response.Success(c, toResponse(membership))
}

// Invite persists an organization invitation before attempting email delivery and returns its
// link once to the caller.
func (h *Handler) Invite(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	organizationID, ok := handler.ParseID(c, "id")
	if !ok {
		return
	}

	var req CreateOrganizationInvitationRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	if fieldErrors := req.validationErrors(); len(fieldErrors) > 0 {
		response.ValidationFailed(c, fieldErrors)
		return
	}

	// The response may carry a one-time invitation link, a bearer secret.
	c.Header("Cache-Control", "private, no-store")
	result, err := h.service.Invite(c.Request.Context(), userID, organizationID, &req)
	if err != nil {
		response.HandleError(c, "Failed to create organization invitation", err)
		return
	}
	response.Created(c, toCreateInvitationResponse(result))
}

// ListInvitations returns token-free invitation history for organization managers.
func (h *Handler) ListInvitations(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	organizationID, ok := handler.ParseID(c, "id")
	if !ok {
		return
	}

	page := pagination.FromContext(c)
	invitations, total, err := h.service.ListInvitations(
		c.Request.Context(),
		userID,
		organizationID,
		page.GetPage(),
		page.GetPerPage(),
	)
	if err != nil {
		response.HandleError(c, "Failed to list organization invitations", err)
		return
	}

	now := time.Now().UTC()
	items := make([]*OrganizationInvitationResponse, len(invitations))
	for index, invitation := range invitations {
		items[index] = toInvitationResponse(invitation, now)
	}
	paginator := pagination.NewPaginator(items, total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	response.Success(c, paginator)
}

// RevokeInvitation consumes one pending invitation without deleting its history.
func (h *Handler) RevokeInvitation(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	organizationID, ok := handler.ParseID(c, "id")
	if !ok {
		return
	}
	invitationID, ok := handler.ParseID(c, "invitation_id")
	if !ok {
		return
	}

	if err := h.service.RevokeInvitation(c.Request.Context(), userID, organizationID, invitationID); err != nil {
		response.HandleError(c, "Failed to revoke organization invitation", err)
		return
	}
	response.NoContent(c)
}

// ResendInvitation emails a pending invitation again with a fresh one-time link and returns that
// link once to the caller.
func (h *Handler) ResendInvitation(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	organizationID, ok := handler.ParseID(c, "id")
	if !ok {
		return
	}
	invitationID, ok := handler.ParseID(c, "invitation_id")
	if !ok {
		return
	}

	// The response may carry a one-time invitation link, a bearer secret.
	c.Header("Cache-Control", "private, no-store")
	result, err := h.service.ResendInvitation(c.Request.Context(), userID, organizationID, invitationID)
	if err != nil {
		var retry *InvitationRetryAfterError
		if errors.As(err, &retry) {
			c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.RetryAfter.Seconds())))))
		}
		response.HandleError(c, "Failed to resend organization invitation", err)
		return
	}
	response.Created(c, toCreateInvitationResponse(result))
}

// AcceptInvitation creates membership and consumes a one-time invitation token atomically.
func (h *Handler) AcceptInvitation(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}

	var req AcceptOrganizationInvitationRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	if fieldErrors := req.validationErrors(); len(fieldErrors) > 0 {
		response.ValidationFailed(c, fieldErrors)
		return
	}
	membership, err := h.service.AcceptInvitation(c.Request.Context(), userID, &req)
	if err != nil {
		response.HandleError(c, "Failed to accept organization invitation", err)
		return
	}
	response.Success(c, toResponse(membership))
}
