package externalmail

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// Handler serves the HTTP endpoints for the top-level externalmail module.
type Handler struct {
	service domain.ExternalMailPort
}

var (
	_ assembly.Module      = (*Handler)(nil)
	_ assembly.RouteModule = (*Handler)(nil)
	_ assembly.ErrorModule = (*Handler)(nil)
)

// NewHandler builds an externalmail HTTP handler backed by domain.ExternalMailPort.
func NewHandler(service domain.ExternalMailPort) *Handler {
	return &Handler{service: service}
}

// Name identifies this module inside the starter registry.
func (h *Handler) Name() string {
	return "externalmail"
}

// ListExternalMailConnectors lists enabled external mail providers.
// fastbuild:bounded-list max=32 reason=the connector registry is a short code-owned list
func (h *Handler) ListExternalMailConnectors(c *gin.Context) {
	if _, ok := organizationScope(c); !ok {
		return
	}
	connectors := h.service.ListExternalMailConnectorsPort(c.Request.Context())
	items := make([]ExternalMailConnectorResponse, 0, len(connectors))
	for _, connector := range connectors {
		items = append(items, ExternalMailConnectorResponse{
			ID:       connector.ID,
			AuthKind: connector.AuthKind,
			Capabilities: ExternalMailCapabilitiesResponse{
				Read:   connector.Read,
				Send:   connector.Send,
				Tenant: connector.Tenant,
			},
		})
	}
	privateResponse(c)
	response.Success(c, items)
}

// StartExternalMailAuthorization begins binding or reconnecting an external mail account.
func (h *Handler) StartExternalMailAuthorization(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req startExternalMailAuthorizationRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	if req.Provider == "" && req.ConnectionID == nil {
		response.HandleError(c, "Failed to start external mail authorization", domain.ErrExternalMailAuthorizationInvalid)
		return
	}
	started, err := h.service.StartExternalMailAuthorizationPort(c.Request.Context(), viewer, domain.ExternalMailAuthorizationInput{
		Provider:     domain.ExternalMailProvider(req.Provider),
		ConnectionID: req.ConnectionID,
		BackfillDays: req.BackfillDays,
		Sharing:      req.Sharing.input(),
	})
	if err != nil {
		response.HandleError(c, "Failed to start external mail authorization", err)
		return
	}
	privateResponse(c)
	response.Created(c, ExternalMailAuthorizationResponse{
		AuthorizationURL: started.AuthorizationURL,
		ExpiresAt:        started.ExpiresAt.UTC(),
	})
}

// CompleteExternalMailAuthorization finishes an OAuth callback.
func (h *Handler) CompleteExternalMailAuthorization(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req completeExternalMailAuthorizationRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	connection, created, err := h.service.CompleteExternalMailAuthorizationPort(c.Request.Context(), viewer, req.State, req.Code)
	if err != nil {
		response.HandleError(c, "Failed to complete external mail authorization", err)
		return
	}
	privateResponse(c)
	item := toExternalMailConnectionResponses([]*domain.ExternalMailConnection{connection})[0]
	if created {
		response.Created(c, item)
		return
	}
	response.Success(c, item)
}

// ListExternalMailConnections lists the caller's own and managed shared external mail connections.
// fastbuild:bounded-list max=220 reason=bounded by per-user, revoked, tenant, and shared connection ceilings
func (h *Handler) ListExternalMailConnections(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	connections, err := h.service.ListExternalMailConnectionsPort(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to list external mail connections", err)
		return
	}
	privateResponse(c)
	response.Success(c, toExternalMailConnectionResponses(connections))
}

// RevokeExternalMailConnection unbinds an external account and stops its sync.
func (h *Handler) RevokeExternalMailConnection(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	connectionID, ok := httphandler.ParseID(c, "connection_id")
	if !ok {
		return
	}
	if err := h.service.RevokeExternalMailConnectionPort(c.Request.Context(), viewer, connectionID); err != nil {
		response.HandleError(c, "Failed to revoke external mail connection", err)
		return
	}
	response.NoContent(c)
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

func privateResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization, Organization-Id")
}
