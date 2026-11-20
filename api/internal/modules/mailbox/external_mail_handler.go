package mailbox

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
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
// fastbuild:bounded-list max=10 reason=one member binds at most MaxExternalMailConnectionsPerUser accounts per organization
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
