package mailbox

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// SetPendingMailboxMemberRequest queues a shared-mailbox member grant for an invited email.
type SetPendingMailboxMemberRequest struct {
	Email      string `json:"email" binding:"required,max=320"`
	Permission string `json:"permission" binding:"required,max=16"`
}

// MailboxPendingMemberResponse is one pending shared-mailbox grant queued for an invited email.
type MailboxPendingMemberResponse struct {
	ID         uint      `json:"id"`
	MailboxID  uint      `json:"mailbox_id"`
	Email      string    `json:"email"`
	Permission string    `json:"permission"`
	GrantedBy  uint      `json:"granted_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toMailboxPendingMemberResponse(pending *domain.MailboxPendingMember) MailboxPendingMemberResponse {
	return MailboxPendingMemberResponse{
		ID:         pending.ID,
		MailboxID:  pending.MailboxID,
		Email:      pending.Email,
		Permission: string(pending.Permission),
		GrantedBy:  pending.GrantedBy,
		CreatedAt:  pending.CreatedAt,
		UpdatedAt:  pending.UpdatedAt,
	}
}

// SetPendingMailboxMember queues a shared-mailbox grant for an invited email.
func (h *Handler) SetPendingMailboxMember(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req SetPendingMailboxMemberRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	pending, err := h.service.SetPendingMailboxMember(
		c.Request.Context(),
		viewer,
		mailboxID,
		req.Email,
		domain.MailboxPermission(req.Permission),
	)
	if err != nil {
		response.HandleError(c, "Failed to set pending mailbox member", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailboxPendingMemberResponse(pending))
}
