package transactional

import (
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/middleware"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
)

// RegisterRoutes registers HTTP routes owned by the transactional module.
func (h *Handler) RegisterRoutes(r *router.Router) {
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth", "organization_context")

		auth.GET("/transactional-mail-log", h.ListTransactionalLog).Name("transactional_mail_log.index")
		auth.GET("/transactional-mail-log/:message_id", h.GetTransactionalLogEntry).
			Name("transactional_mail_log.show").WhereNumber("message_id")
		auth.POST("/transactional-test-sends", h.SendTransactionalTest).
			Name("transactional_test_sends.store").Middleware(middleware.BodyLimit(maxTransactionalBodyBytes))
		auth.GET("/mail-send-lanes", h.ListSendLanes).Name("mail_send_lanes.index")
		auth.GET("/mail-suppressions", h.ListSuppressions).Name("mail_suppressions.index")
		auth.DELETE("/mail-suppressions/:suppression_id", h.RemoveSuppression).
			Name("mail_suppressions.destroy").WhereNumber("suppression_id")
	})

	r.Group("", func(key *router.Router) {
		key.WithMiddleware("api_key", "organization_context")
		key.Use(requireFeature(domain.MemberFeatureAPIKeys))

		key.POST("/transactional-messages", h.SendTransactional).
			Name("transactional_messages.store").
			Middleware(middleware.BodyLimit(maxTransactionalBodyBytes), apikey.RequireScopesAllowingTest(ScopeMailTransactional))
		key.GET("/transactional-messages/:message_id", h.GetTransactional).
			Name("transactional_messages.show").WhereNumber("message_id").
			Middleware(apikey.RequireScopesAllowingTest(ScopeMailTransactional))
	})
}
