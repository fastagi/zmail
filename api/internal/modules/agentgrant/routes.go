package agentgrant

import (
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/middleware"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
)

// RegisterRoutes registers HTTP routes owned by the agentgrant module.
func (h *Handler) RegisterRoutes(r *router.Router) {
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth", "organization_context")

		auth.GET("/mailboxes/:mailbox_id/agent-grants", h.ListAgentGrants).
			Name("mailboxes.agent_grants.index").WhereNumber("mailbox_id")
		auth.POST("/mailboxes/:mailbox_id/agent-grants", h.IssueAgentGrant).
			Name("mailboxes.agent_grants.store").WhereNumber("mailbox_id")
		auth.DELETE("/mailboxes/:mailbox_id/agent-grants/:grant_id", h.RevokeAgentGrant).
			Name("mailboxes.agent_grants.destroy").WhereNumber("mailbox_id").WhereNumber("grant_id")
		auth.GET("/mailboxes/:mailbox_id/agent-grants/:grant_id/activity", h.AgentGrantActivity).
			Name("mailboxes.agent_grants.activity.index").WhereNumber("mailbox_id").WhereNumber("grant_id")
		auth.GET("/mailboxes/:mailbox_id/agent-drafts", h.ListAgentDrafts).
			Name("mailboxes.agent_drafts.index").WhereNumber("mailbox_id")
	})

	r.Group("", func(key *router.Router) {
		key.WithMiddleware("api_key", "organization_context")
		key.Use(requireFeature(domain.MemberFeatureAPIKeys))

		key.GET("/agent-mail-events", h.ListAgentMailEvents).
			Name("agent_mail_events.index").Middleware(apikey.RequireScopes(ScopeMailAgent))
		key.POST("/agent-mail-drafts", h.CreateAgentDraft).
			Name("agent_mail_drafts.store").
			Middleware(middleware.BodyLimit(maxAgentDraftBodyBytes), apikey.RequireScopes(ScopeMailAgent))
	})
}
