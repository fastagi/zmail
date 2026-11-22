package mailbox

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// UpdateMailSignatureRequest replaces the caller's signature; an empty body removes it.
type UpdateMailSignatureRequest struct {
	Body *string `json:"body" binding:"required,max=2000"`
}

// MailSignatureResponse is the caller's own signature for a mailbox.
type MailSignatureResponse struct {
	MailboxID uint       `json:"mailbox_id"`
	Body      string     `json:"body"`
	UpdatedAt *time.Time `json:"updated_at"`
}

func toMailSignatureResponse(signature domain.MailSignature) MailSignatureResponse {
	return MailSignatureResponse{MailboxID: signature.MailboxID, Body: signature.Body, UpdatedAt: signature.UpdatedAt}
}

// GetMailSignature reads the caller's signature for a mailbox they may send from.
func (h *Handler) GetMailSignature(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	signature, err := h.service.MailSignature(c.Request.Context(), viewer, mailboxID)
	if err != nil {
		response.HandleError(c, "Failed to read the signature", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailSignatureResponse(signature))
}

// UpdateMailSignature replaces the caller's signature for a mailbox they may send from.
func (h *Handler) UpdateMailSignature(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req UpdateMailSignatureRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	signature, err := h.service.SaveMailSignature(c.Request.Context(), viewer, mailboxID, *req.Body)
	if err != nil {
		respondOrganizeError(c, "Failed to save the signature", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailSignatureResponse(signature))
}
