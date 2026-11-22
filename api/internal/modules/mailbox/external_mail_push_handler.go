package mailbox

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// ExternalMailPushService receives the providers' new-mail notifications.
type ExternalMailPushService interface {
	// ReceiveExternalMailPush authenticates one notification request and marks the connections it
	// names due. It answers the same whether or not a subscription matches.
	ReceiveExternalMailPush(ctx context.Context, provider domain.ExternalMailProvider, delivery ExternalMailPushDelivery) (ExternalMailPushReply, error)
}

// ReceiveGmailPush takes a Cloud Pub/Sub push request for Gmail; it is authenticated by the
// Google-signed OIDC token of the push subscription. 204 acknowledges it.
func (h *Handler) ReceiveGmailPush(c *gin.Context) {
	h.receivePush(c, domain.ExternalMailProviderGmail, http.StatusNoContent)
}

// ReceiveOutlookPush takes a Microsoft Graph change or lifecycle notification, or the validation
// handshake of a new subscription. 202 acknowledges a notification.
func (h *Handler) ReceiveOutlookPush(c *gin.Context) {
	h.receivePush(c, domain.ExternalMailProviderOutlook, http.StatusAccepted)
}

// receivePush reads a bounded body and answers without saying whether a subscription matched.
func (h *Handler) receivePush(c *gin.Context, provider domain.ExternalMailProvider, acknowledged int) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxExternalMailPushBodyBytes+1))
	if err != nil {
		response.HandleError(c, "Failed to read the notification", domain.ErrInvalidInput)
		return
	}
	if len(body) > maxExternalMailPushBodyBytes {
		response.AbortWithCode(c, http.StatusRequestEntityTooLarge, domain.CodeRequestTooLarge, "Notification too large")
		return
	}
	reply, err := h.service.ReceiveExternalMailPush(c.Request.Context(), provider, ExternalMailPushDelivery{
		Header: c.Request.Header, Query: c.Request.URL.Query(), Body: body, ClientIP: c.ClientIP(),
	})
	if err != nil {
		var retry *RetryAfterError
		if errors.As(err, &retry) {
			c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.RetryAfter.Seconds())))))
		}
		response.HandleError(c, "Failed to receive the notification", err)
		return
	}
	privateResponse(c)
	if reply.Validation != "" {
		// The handshake echoes the token as plain text and nothing else.
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(reply.Validation))
		return
	}
	c.Status(acknowledged)
	c.Writer.WriteHeaderNow()
}
