package operator

import (
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
)

// RegisterRoutes attaches the operator browser session and operator-only routes. Every protected
// route requires a current grant; each group then requires the capability it exposes.
func (h *Handler) RegisterRoutes(r *router.Router) {
	signIn := r.POST("/operator/session", h.SignIn).Name("operator.session.store")
	if h.guard != nil {
		if limit := h.guard.LoginIPMiddleware(); limit != nil {
			signIn.Middleware(limit)
		}
	}
	r.DELETE("/operator/session", h.SignOut).Name("operator.session.destroy")

	r.Group("/operator", func(operator *router.Router) {
		operator.Use(h.requireOperator)
		operator.GET("/session", h.Current).Name("operator.session.show")

		operator.Group("", func(manage *router.Router) {
			manage.Use(requireCapability(domain.OperatorCapabilityManage))
			h.registerManageRoutes(manage)
		})
		operator.Group("", func(metrics *router.Router) {
			metrics.Use(requireCapability(domain.OperatorCapabilityMailMetrics))
			h.registerMetricsRoutes(metrics)
		})
		if h.mailMetrics != nil {
			operator.Group("", func(content *router.Router) {
				content.Use(requireCapability(domain.OperatorCapabilityMailContent))
				content.POST("/outbound-messages/:organization_id/:message_id/content", h.mailMetrics.ReadBroadcastContent).
					Name("operator.outbound-message-content.read").WhereNumber("organization_id").WhereNumber("message_id")
			})
			// The access records are readable by whoever may read content and by full operators,
			// who oversee the platform without reading bodies themselves.
			operator.Group("", func(audits *router.Router) {
				audits.Use(requireCapability(domain.OperatorCapabilityMailContent, domain.OperatorCapabilityManage))
				audits.GET("/content-access-audits", h.mailMetrics.ContentAccessAudits).
					Name("operator.content-access-audits.index")
			})
		}
	})
}

// registerManageRoutes mounts the routes that need operator:manage.
func (h *Handler) registerManageRoutes(operator *router.Router) {
	operator.GET("/users", h.ListUsers).Name("operator.users.index")
	operator.GET("/users/:id", h.GetUser).Name("operator.users.show").WhereNumber("id")
	operator.POST("/users/:id/disable", h.DisableUser).Name("operator.users.disable").WhereNumber("id")
	operator.POST("/users/:id/enable", h.EnableUser).Name("operator.users.enable").WhereNumber("id")
	operator.POST("/users/:id/sessions/revoke", h.RevokeUserSessions).
		Name("operator.user-sessions.revoke").WhereNumber("id")

	operator.GET("/audit-logs", h.ListAuditLogs).Name("operator.audit-logs.index")
	operator.GET("/system", h.System).Name("operator.system.show")

	if h.settings != nil {
		operator.GET("/settings", h.settings.AppList).Name("operator.settings.index")
		operator.PATCH("/settings/:key", h.settings.AppSet).Name("operator.settings.update")
		operator.DELETE("/settings/:key", h.settings.AppReset).Name("operator.settings.destroy")
	}

	if h.webhooks != nil {
		operator.Group("/organizations/:id", func(organization *router.Router) {
			organization.Use(h.organizations.Require)
			organization.GET("/webhook-endpoints", h.webhooks.ListEndpoints).
				Name("operator.webhook-endpoints.index").WhereNumber("id")
			organization.GET("/webhook-deliveries", h.webhooks.ListDeliveries).
				Name("operator.webhook-deliveries.index").WhereNumber("id")
			organization.GET("/webhook-deliveries/:delivery_id/attempts", h.webhooks.ListAttempts).
				Name("operator.webhook-delivery-attempts.index").WhereNumber("id").WhereNumber("delivery_id")
			organization.POST("/webhook-deliveries/:delivery_id/replay", h.webhooks.ReplayDelivery).
				Name("operator.webhook-deliveries.replay").WhereNumber("id").WhereNumber("delivery_id")
		})
	}
	if h.organizations != nil {
		operator.POST("/organizations/:id/suspend", h.organizations.Suspend).
			Name("operator.organization-suspension.store").WhereNumber("id")
		operator.POST("/organizations/:id/restore", h.organizations.Restore).
			Name("operator.organization-suspension.destroy").WhereNumber("id")
		operator.PUT("/organizations/:id/send-limit", h.organizations.SetSendLimit).
			Name("operator.organization-send-limit.update").WhereNumber("id")
	}
	if h.notifications != nil {
		operator.GET("/notification-deliveries", h.notifications.ListDeliveries).
			Name("operator.notification-deliveries.index")
	}
}

// registerMetricsRoutes mounts the read-only routes that need mail_metrics:read, which every full
// grant also has.
func (h *Handler) registerMetricsRoutes(operator *router.Router) {
	if h.organizations != nil {
		operator.GET("/organizations", h.organizations.List).Name("operator.organizations.index")
		operator.GET("/organizations/:id", h.organizations.Get).
			Name("operator.organizations.show").WhereNumber("id")
		operator.GET("/organizations/:id/members", h.organizations.ListMembers).
			Name("operator.organization-members.index").WhereNumber("id")
	}
	if h.mailMetrics != nil {
		operator.GET("/mail-metrics", h.mailMetrics.Overview).Name("operator.mail-metrics.show")
		operator.GET("/mail-metrics/organizations", h.mailMetrics.Organizations).
			Name("operator.mail-metrics-organizations.index")
		operator.GET("/outbound-messages", h.mailMetrics.OutboundMessages).
			Name("operator.outbound-messages.index")
	}
}
