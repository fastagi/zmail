package mailbox

import (
	"errors"
	"math"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// maxExternalMailCredentialsBodyBytes bounds a credential binding request.
const maxExternalMailCredentialsBodyBytes = 4096

// bindExternalMailCredentialsRequest binds an account of a password connector. It is never
// logged or echoed back.
type bindExternalMailCredentialsRequest struct {
	Provider string `json:"provider" binding:"required,max=16"`
	Host     string `json:"host" binding:"required,max=253"`
	Port     int    `json:"port" binding:"omitempty,min=1,max=65535"`
	Username string `json:"username" binding:"required,max=320"`
	Password string `json:"password" binding:"required,max=320"`
	// BackfillDays is checked by the service, which answers MAILBOX.EXTERNAL_AUTHORIZATION_INVALID
	// out of range, as the OAuth start route does.
	BackfillDays int `json:"backfill_days"`
}

// BindExternalMailCredentials binds an outside account with an app password: 201 with a new
// connection, or 200 with the caller's own connection restored.
func (h *Handler) BindExternalMailCredentials(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req bindExternalMailCredentialsRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	connection, created, err := h.service.BindExternalMailCredentials(c.Request.Context(), viewer, ExternalMailCredentialsInput{
		Provider: domain.ExternalMailProvider(req.Provider), Host: req.Host, Port: req.Port,
		Username: req.Username, Password: req.Password, BackfillDays: req.BackfillDays,
	})
	if err != nil {
		var retry *RetryAfterError
		if errors.As(err, &retry) {
			c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.RetryAfter.Seconds())))))
		}
		response.HandleError(c, "Failed to bind external mail credentials", err)
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
