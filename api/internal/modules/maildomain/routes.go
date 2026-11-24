package maildomain

import (
	"github.com/fastagi/zmail/api/internal/infra/router"
)

// RegisterRoutes registers HTTP routes owned by the maildomain module.
func (h *Handler) RegisterRoutes(r *router.Router) {
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth", "organization_context")

		auth.GET("/mail-domains", h.ListMailDomains).Name("mail_domains.index")
		auth.POST("/mail-domains", h.ConnectMailDomain).Name("mail_domains.store")
		auth.GET("/mail-domains/:domain_id", h.GetMailDomain).Name("mail_domains.show").WhereNumber("domain_id")
		auth.DELETE("/mail-domains/:domain_id", h.DeleteMailDomain).Name("mail_domains.destroy").WhereNumber("domain_id")
		auth.POST("/mail-domains/:domain_id/setups", h.SetUpMailDomain).
			Name("mail_domains.setups.store").WhereNumber("domain_id")
		auth.POST("/mail-domains/:domain_id/checks", h.CheckMailDomain).
			Name("mail_domains.checks.store").WhereNumber("domain_id")
		auth.POST("/mail-domains/:domain_id/receiving", h.EnableMailDomainReceiving).
			Name("mail_domains.receiving.store").WhereNumber("domain_id")
		auth.PUT("/mail-domains/:domain_id/default", h.SetDefaultMailDomain).
			Name("mail_domains.default.update").WhereNumber("domain_id")
		auth.POST("/mail-domain-previews", h.PreviewMailDomain).Name("mail_domain_previews.store")

		auth.GET("/mail-zones", h.ListMailZones).Name("mail_zones.index")
		auth.GET("/mail-zones/:zone_id", h.GetMailZone).Name("mail_zones.show").WhereNumber("zone_id")
		auth.DELETE("/mail-zones/:zone_id", h.DeleteMailZone).Name("mail_zones.destroy").WhereNumber("zone_id")
	})
}
