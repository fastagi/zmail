package user

import (
	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// AuthProviders reports which external sign-in providers are configured, so clients show only the
// buttons that work.
func (h *Handler) AuthProviders(c *gin.Context) {
	response.Success(c, AuthProvidersResponse{
		Google: AuthProviderStatus{Enabled: h.google != nil && h.google.GoogleSignInEnabled()},
	})
}

// StartGoogleSignIn creates a pending Google sign-in and answers with Google's authorization URL.
func (h *Handler) StartGoogleSignIn(c *gin.Context) {
	if h.google == nil || !h.google.GoogleSignInEnabled() {
		response.HandleError(c, "Google sign-in is not configured", domain.ErrGoogleNotConfigured)
		return
	}
	var req GoogleAuthorizationRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	started, err := h.google.StartGoogleSignIn(c.Request.Context(), &req)
	if err != nil {
		response.HandleError(c, "Google sign-in could not start", err)
		return
	}
	response.Created(c, started)
}

// CompleteGoogleSignIn turns Google's answer into a user session, like POST /v1/login.
func (h *Handler) CompleteGoogleSignIn(c *gin.Context) {
	if h.google == nil || !h.google.GoogleSignInEnabled() {
		response.HandleError(c, "Google sign-in is not configured", domain.ErrGoogleNotConfigured)
		return
	}
	var req GoogleSessionRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	resp, err := h.google.CompleteGoogleSignIn(c.Request.Context(), &req)
	if err != nil {
		response.HandleError(c, "Google sign-in failed", err)
		return
	}
	if resp.User != nil {
		c.Set("userID", resp.User.ID)
	}
	response.Success(c, resp)
}
