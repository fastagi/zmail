package mailbox

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// ExternalMailService lists and revokes the caller's external mailbox connections. Both answer
// ErrExternalMailConnectionNotFound while the instance enables no source.
type ExternalMailService interface {
	// ListExternalMailConnections lists the viewer's own connections.
	ListExternalMailConnections(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.ExternalMailConnection, error)
	// RevokeExternalMailConnection unbinds a connection: its owner, or an owner or admin.
	RevokeExternalMailConnection(ctx context.Context, viewer domain.OrganizationContext, id uint) error
}

// ExternalMailConnectionResponse is one bound outside account. It never carries the token.
type ExternalMailConnectionResponse struct {
	ID             uint       `json:"id"`
	Provider       string     `json:"provider"`
	AccountAddress string     `json:"account_address"`
	MailboxID      uint       `json:"mailbox_id"`
	Status         string     `json:"status"`
	LastSyncedAt   *time.Time `json:"last_synced_at"`
	LastErrorCode  *string    `json:"last_error_code"`
	CreatedAt      time.Time  `json:"created_at"`
}

func toExternalMailConnectionResponses(connections []*domain.ExternalMailConnection) []ExternalMailConnectionResponse {
	responses := make([]ExternalMailConnectionResponse, 0, len(connections))
	for _, connection := range connections {
		item := ExternalMailConnectionResponse{
			ID: connection.ID, Provider: string(connection.Provider), AccountAddress: connection.AccountAddress,
			MailboxID: connection.MailboxID, Status: string(connection.Status),
			LastErrorCode: optionalText(connection.LastErrorCode), CreatedAt: connection.CreatedAt.UTC(),
		}
		if connection.LastSyncedAt != nil {
			item.LastSyncedAt = new(connection.LastSyncedAt.UTC())
		}
		responses = append(responses, item)
	}
	return responses
}

// ListExternalMailConnections lists the caller's own connections.
// fastbuild:bounded-list max=20 reason=at most MaxExternalMailConnectionsPerUser live and MaxRevokedExternalMailConnectionsListed revoked connections of one member
func (h *Handler) ListExternalMailConnections(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	connections, err := h.service.ListExternalMailConnections(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to list external mail connections", err)
		return
	}
	privateResponse(c)
	response.Success(c, toExternalMailConnectionResponses(connections))
}

// RevokeExternalMailConnection unbinds an outside account and stops its sync for good.
func (h *Handler) RevokeExternalMailConnection(c *gin.Context) {
	viewer, connectionID, ok := viewerScope(c, "connection_id")
	if !ok {
		return
	}
	if err := h.service.RevokeExternalMailConnection(c.Request.Context(), viewer, connectionID); err != nil {
		response.HandleError(c, "Failed to revoke external mail connection", err)
		return
	}
	response.NoContent(c)
}

// ExternalMailConnectorResponse is one provider members can bind with.
type ExternalMailConnectorResponse struct {
	ID           string                           `json:"id"`
	AuthKind     string                           `json:"auth_kind"`
	Capabilities ExternalMailCapabilitiesResponse `json:"capabilities"`
}

// ExternalMailCapabilitiesResponse says what a connector does with a bound account.
type ExternalMailCapabilitiesResponse struct {
	Read bool `json:"read"`
}

// ListExternalMailConnectors lists the connectors the instance enables, so a client offers one
// button per provider without knowing them in advance. Empty when the feature is off.
// fastbuild:bounded-list max=32 reason=the connector registry is a short code-owned list
func (h *Handler) ListExternalMailConnectors(c *gin.Context) {
	if _, ok := organizationScope(c); !ok {
		return
	}
	connectors := h.service.ListExternalMailConnectors(c.Request.Context())
	items := make([]ExternalMailConnectorResponse, 0, len(connectors))
	for _, connector := range connectors {
		items = append(items, ExternalMailConnectorResponse{
			ID: string(connector.ID), AuthKind: string(connector.AuthKind),
			Capabilities: ExternalMailCapabilitiesResponse{Read: connector.Capabilities.Read},
		})
	}
	privateResponse(c)
	response.Success(c, items)
}

// startExternalMailAuthorizationRequest starts a sign-in.
type startExternalMailAuthorizationRequest struct {
	Provider     string `json:"provider" binding:"omitempty,max=16"`
	ConnectionID *uint  `json:"connection_id" binding:"omitempty,min=1"`
	// BackfillDays is checked by the service, which answers MAILBOX.EXTERNAL_AUTHORIZATION_INVALID
	// out of range, as the credentials route does.
	BackfillDays int `json:"backfill_days"`
}

// ExternalMailAuthorizationResponse is where to send the browser.
type ExternalMailAuthorizationResponse struct {
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

// StartExternalMailAuthorization begins binding an outside account, or reconnecting one.
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
		response.HandleError(c, "Failed to start external mail authorization", ErrExternalMailAuthorizationInvalid)
		return
	}
	started, err := h.service.StartExternalMailAuthorization(c.Request.Context(), viewer, ExternalMailAuthorizationInput{
		Provider: domain.ExternalMailProvider(req.Provider), ConnectionID: req.ConnectionID, BackfillDays: req.BackfillDays,
	})
	if err != nil {
		response.HandleError(c, "Failed to start external mail authorization", err)
		return
	}
	privateResponse(c)
	response.Created(c, ExternalMailAuthorizationResponse{
		AuthorizationURL: started.AuthorizationURL, ExpiresAt: started.ExpiresAt.UTC(),
	})
}

// completeExternalMailAuthorizationRequest carries what the provider returned to the callback.
type completeExternalMailAuthorizationRequest struct {
	State string `json:"state" binding:"required,max=128"`
	Code  string `json:"code" binding:"required,max=4096"`
}

// CompleteExternalMailAuthorization finishes a sign-in: 201 with a new connection, or 200 with
// the restored one.
func (h *Handler) CompleteExternalMailAuthorization(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req completeExternalMailAuthorizationRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	connection, created, err := h.service.CompleteExternalMailAuthorization(c.Request.Context(), viewer, req.State, req.Code)
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
