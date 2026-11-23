package user

import (
	"github.com/fastagi/zmail/api/internal/infra/router"
)

// RegisterMiddleware registers the persistent authentication-session middleware.
func (h *Handler) RegisterMiddleware(r *router.Router) {
	r.MiddlewareGroup("auth", sessionAuth(h.sessions))
}

// RegisterRoutes registers the user module routes
// It uses the injected handler instance instead of creating a new one
func (h *Handler) RegisterRoutes(r *router.Router) {
	// Public routes
	h.protectPublicRoute(r.POST("/register", h.Register).Name("auth.register"), authEndpointRegister)
	h.protectPublicRoute(r.POST("/register/email-codes", h.RequestRegistrationEmailCode).Name("auth.register.email_code"), authEndpointRegisterEmailCode)
	h.protectPublicRoute(r.POST("/login", h.Login).Name("auth.login"), authEndpointLogin)
	h.protectPublicRoute(r.POST("/password/reset", h.RequestPasswordReset).Name("auth.password.reset.request"), authEndpointPasswordReset)
	h.protectPublicRoute(r.POST("/password/reset/confirm", h.ConfirmPasswordReset).Name("auth.password.reset.confirm"), authEndpointPasswordResetConfirm)

	// Sign in with Google. Both steps share the login source quota.
	r.GET("/auth/providers", h.AuthProviders).Name("auth.providers")
	h.protectPublicRoute(r.POST("/auth/google/authorizations", h.StartGoogleSignIn).Name("auth.google.authorizations.create"), authEndpointLogin)
	h.protectPublicRoute(r.POST("/auth/google/sessions", h.CompleteGoogleSignIn).Name("auth.google.sessions.create"), authEndpointLogin)

	// Protected routes
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth")

		auth.POST("/logout", h.Logout).Name("auth.logout")

		// Profile
		auth.GET("/users/profile", h.GetProfile).Name("users.profile")
		auth.PUT("/users/profile", h.UpdateProfile).Name("users.profile.update")
		auth.PUT("/users/password", h.ChangePassword).Name("users.password.update")
		auth.DELETE("/users/account", h.DeleteAccount).Name("users.account.delete")

		// Two-factor sign-in
		auth.GET("/users/totp", h.GetTOTP).Name("users.totp")
		auth.POST("/users/totp/enrollments", h.StartTOTPEnrollment).Name("users.totp.enrollments.create")
		auth.PUT("/users/totp", h.ConfirmTOTP).Name("users.totp.confirm")
		auth.DELETE("/users/totp", h.DisableTOTP).Name("users.totp.delete")
	})
}

func (h *Handler) protectPublicRoute(route *router.Route, endpoint authEndpoint) {
	if middleware := h.authGuard.perIPMiddleware(endpoint); middleware != nil {
		route.Middleware(middleware)
	}
}
