package user

import (
	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// GetTOTP reports the signed-in user's two-factor state.
func (h *Handler) GetTOTP(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	status, err := h.profile.TOTPStatus(c.Request.Context(), userID)
	if err != nil {
		response.HandleError(c, "Failed to load two-factor sign-in", err)
		return
	}
	response.Success(c, status)
}

// StartTOTPEnrollment creates a pending authenticator secret.
func (h *Handler) StartTOTPEnrollment(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	enrollment, err := h.profile.StartTOTPEnrollment(c.Request.Context(), userID)
	if err != nil {
		response.HandleError(c, "Failed to start two-factor enrollment", err)
		return
	}
	response.Created(c, enrollment)
}

// ConfirmTOTP turns two-factor sign-in on with a first code and returns recovery codes.
func (h *Handler) ConfirmTOTP(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	var req TOTPCodeRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	codes, err := h.profile.ConfirmTOTP(c.Request.Context(), userID, req.Code)
	if err != nil {
		response.HandleError(c, "Failed to turn on two-factor sign-in", err)
		return
	}
	response.Success(c, codes)
}

// DisableTOTP turns two-factor sign-in off with an authenticator or recovery code.
func (h *Handler) DisableTOTP(c *gin.Context) {
	userID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	var req TOTPCodeRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	if err := h.profile.DisableTOTP(c.Request.Context(), userID, req.Code); err != nil {
		response.HandleError(c, "Failed to turn off two-factor sign-in", err)
		return
	}
	response.NoContent(c)
}
