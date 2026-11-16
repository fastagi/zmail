package mailbox

import (
	"github.com/fastagi/fastmail/api/internal/infra/router"
	"github.com/fastagi/fastmail/api/internal/modules/apikey"
)

// RegisterRoutes registers HTTP routes for the mailbox starter. User routes require a signed-in
// user and only ever touch that user's mailboxes. The inbound route is authenticated by the
// mail edge's HMAC signature instead of a session.
func (h *Handler) RegisterRoutes(r *router.Router) {
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth")

		auth.GET("/mailbox-domains", h.ListDomains).Name("mailbox_domains.index")

		auth.GET("/mail-messages", h.ListLog).Name("mail_messages.index")
		auth.GET("/mail-message-stats", h.ListStats).Name("mail_message_stats.index")

		auth.GET("/mailboxes", h.ListMailboxes).Name("mailboxes.index")
		auth.POST("/mailboxes", h.CreateMailbox).Name("mailboxes.store")
		auth.DELETE("/mailboxes/:mailbox_id", h.DeleteMailbox).Name("mailboxes.destroy").WhereNumber("mailbox_id")

		auth.GET("/mailboxes/:mailbox_id/messages", h.ListMessages).
			Name("mailboxes.messages.index").WhereNumber("mailbox_id")
		auth.POST("/mailboxes/:mailbox_id/messages", h.SendMessage).
			Name("mailboxes.messages.store").WhereNumber("mailbox_id")
		auth.GET("/mailboxes/:mailbox_id/messages/:message_id", h.GetMessage).
			Name("mailboxes.messages.show").WhereNumber("mailbox_id").WhereNumber("message_id")
		auth.PUT("/mailboxes/:mailbox_id/messages/:message_id", h.UpdateMessage).
			Name("mailboxes.messages.update").WhereNumber("mailbox_id").WhereNumber("message_id")
		auth.DELETE("/mailboxes/:mailbox_id/messages/:message_id", h.DeleteMessage).
			Name("mailboxes.messages.destroy").WhereNumber("mailbox_id").WhereNumber("message_id")
	})

	// Programmatic sending for backend services: an API key instead of a browser session, with
	// scopes that limit the key to sending or to reading what it sent.
	r.Group("", func(key *router.Router) {
		key.WithMiddleware("api_key")

		key.POST("/outbound-mail-messages", h.SendOutbound).
			Name("outbound_mail_messages.store").Middleware(apikey.RequireScopes(ScopeMailSend))
		key.GET("/outbound-mail-messages/:message_id", h.GetOutbound).
			Name("outbound_mail_messages.show").WhereNumber("message_id").
			Middleware(apikey.RequireScopes(ScopeMailRead))
	})

	r.POST("/inbound-mail-messages", h.ReceiveInbound).Name("inbound_mail_messages.store")
}
