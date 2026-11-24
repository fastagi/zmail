package externalmail

import (
	"github.com/fastagi/zmail/api/internal/infra/router"
)

// RegisterRoutes registers HTTP routes owned by the externalmail module.
func (h *Handler) RegisterRoutes(r *router.Router) {
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth", "organization_context")

		auth.GET("/external-mail-connectors", h.ListExternalMailConnectors).Name("external_mail_connectors.index")
		auth.POST("/external-mail-authorizations", h.StartExternalMailAuthorization).
			Name("external_mail_authorizations.store")
		auth.POST("/external-mail-authorizations/completions", h.CompleteExternalMailAuthorization).
			Name("external_mail_authorizations.completions.store")
		auth.GET("/external-mail-connections", h.ListExternalMailConnections).Name("external_mail_connections.index")
		auth.DELETE("/external-mail-connections/:connection_id", h.RevokeExternalMailConnection).
			Name("external_mail_connections.destroy").WhereNumber("connection_id")
	})
}
