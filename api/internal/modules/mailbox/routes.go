package mailbox

import (
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
)

// RegisterRoutes registers HTTP routes for the mailbox starter. Member routes require a
// signed-in user and an organization the user belongs to, and only ever touch that
// organization's mailboxes. The inbound route is authenticated by the
// mail edge's HMAC signature instead of a session.
func (h *Handler) RegisterRoutes(r *router.Router) {
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth", "organization_context")

		auth.GET("/mailbox-domains", h.ListDomains).Name("mailbox_domains.index")

		auth.GET("/mail-domains", h.ListMailDomains).Name("mail_domains.index")
		auth.POST("/mail-domains", h.ConnectMailDomain).Name("mail_domains.store")
		auth.GET("/mail-domains/:domain_id", h.GetMailDomain).Name("mail_domains.show").WhereNumber("domain_id")
		auth.DELETE("/mail-domains/:domain_id", h.DeleteMailDomain).Name("mail_domains.destroy").WhereNumber("domain_id")
		auth.POST("/mail-domains/:domain_id/setups", h.SetUpMailDomain).
			Name("mail_domains.setups.store").WhereNumber("domain_id")
		auth.POST("/mail-domains/:domain_id/checks", h.CheckMailDomain).
			Name("mail_domains.checks.store").WhereNumber("domain_id")

		auth.GET("/mail-messages", h.ListLog).Name("mail_messages.index")
		auth.GET("/mail-message-stats", h.ListStats).Name("mail_message_stats.index")

		auth.GET("/mail-templates", h.ListTemplates).Name("mail_templates.index")
		auth.POST("/mail-templates", h.CreateTemplate).Name("mail_templates.store")
		auth.GET("/mail-templates/:template_id", h.GetTemplate).Name("mail_templates.show").WhereNumber("template_id")
		auth.PUT("/mail-templates/:template_id", h.UpdateTemplate).Name("mail_templates.update").WhereNumber("template_id")
		auth.DELETE("/mail-templates/:template_id", h.DeleteTemplate).Name("mail_templates.destroy").WhereNumber("template_id")
		auth.POST("/mail-templates/:template_id/tests", h.SendTemplateTest).
			Name("mail_templates.tests.store").WhereNumber("template_id")
		auth.POST("/mail-template-previews", h.PreviewTemplate).Name("mail_template_previews.store")

		auth.GET("/mail-broadcasts", h.ListBroadcasts).Name("mail_broadcasts.index")
		auth.POST("/mail-broadcasts", h.CreateBroadcast).Name("mail_broadcasts.store")
		auth.GET("/mail-broadcasts/:broadcast_id", h.GetBroadcast).Name("mail_broadcasts.show").WhereNumber("broadcast_id")
		auth.PUT("/mail-broadcasts/:broadcast_id", h.UpdateBroadcast).Name("mail_broadcasts.update").WhereNumber("broadcast_id")
		auth.DELETE("/mail-broadcasts/:broadcast_id", h.DeleteBroadcast).Name("mail_broadcasts.destroy").WhereNumber("broadcast_id")
		auth.POST("/mail-broadcasts/:broadcast_id/sends", h.SendBroadcast).
			Name("mail_broadcasts.sends.store").WhereNumber("broadcast_id")
		auth.POST("/mail-broadcasts/:broadcast_id/cancellations", h.CancelBroadcast).
			Name("mail_broadcasts.cancellations.store").WhereNumber("broadcast_id")
		auth.GET("/mail-broadcasts/:broadcast_id/recipients", h.ListBroadcastRecipients).
			Name("mail_broadcasts.recipients.index").WhereNumber("broadcast_id")

		auth.GET("/mail-contacts", h.ListContacts).Name("mail_contacts.index")
		auth.POST("/mail-contacts", h.CreateContact).Name("mail_contacts.store")
		auth.GET("/mail-contacts/:contact_id", h.GetContact).Name("mail_contacts.show").WhereNumber("contact_id")
		auth.PUT("/mail-contacts/:contact_id", h.UpdateContact).Name("mail_contacts.update").WhereNumber("contact_id")
		auth.DELETE("/mail-contacts/:contact_id", h.DeleteContact).Name("mail_contacts.destroy").WhereNumber("contact_id")
		auth.POST("/mail-contact-imports", h.ImportContacts).Name("mail_contact_imports.store")
		auth.GET("/mail-contact-lists", h.ListContactLists).Name("mail_contact_lists.index")
		auth.POST("/mail-contact-lists", h.CreateContactList).Name("mail_contact_lists.store")
		auth.PUT("/mail-contact-lists/:list_id", h.RenameContactList).Name("mail_contact_lists.update").WhereNumber("list_id")
		auth.DELETE("/mail-contact-lists/:list_id", h.DeleteContactList).Name("mail_contact_lists.destroy").WhereNumber("list_id")

		auth.GET("/mailboxes", h.ListMailboxes).Name("mailboxes.index")
		auth.POST("/mailboxes", h.CreateMailbox).Name("mailboxes.store")
		auth.DELETE("/mailboxes/:mailbox_id", h.DeleteMailbox).Name("mailboxes.destroy").WhereNumber("mailbox_id")

		auth.GET("/mailboxes/:mailbox_id/messages", h.ListMessages).
			Name("mailboxes.messages.index").WhereNumber("mailbox_id")
		auth.POST("/mailboxes/:mailbox_id/messages", h.SendMessage).
			Name("mailboxes.messages.store").WhereNumber("mailbox_id")
		auth.GET("/mailboxes/:mailbox_id/messages/:message_id", h.GetMessage).
			Name("mailboxes.messages.show").WhereNumber("mailbox_id").WhereNumber("message_id")
		auth.GET("/mailboxes/:mailbox_id/messages/:message_id/attachments/:attachment_id", h.DownloadAttachment).
			Name("mailboxes.messages.attachments.show").
			WhereNumber("mailbox_id").WhereNumber("message_id").WhereNumber("attachment_id")
		auth.PUT("/mailboxes/:mailbox_id/messages/:message_id", h.UpdateMessage).
			Name("mailboxes.messages.update").WhereNumber("mailbox_id").WhereNumber("message_id")
		auth.DELETE("/mailboxes/:mailbox_id/messages/:message_id", h.DeleteMessage).
			Name("mailboxes.messages.destroy").WhereNumber("mailbox_id").WhereNumber("message_id")
	})

	// Programmatic sending for backend services: an API key instead of a browser session, with
	// scopes that limit the key to sending or to reading what was sent. The key acts with its
	// owner's current membership of the organization named by Organization-Id.
	r.Group("", func(key *router.Router) {
		key.WithMiddleware("api_key", "organization_context")

		key.POST("/outbound-mail-messages", h.SendOutbound).
			Name("outbound_mail_messages.store").Middleware(apikey.RequireScopes(ScopeMailSend))
		key.GET("/outbound-mail-messages/:message_id", h.GetOutbound).
			Name("outbound_mail_messages.show").WhereNumber("message_id").
			Middleware(apikey.RequireScopes(ScopeMailRead))
	})

	r.POST("/inbound-mail-messages", h.ReceiveInbound).Name("inbound_mail_messages.store")
	r.POST("/mail-delivery-events", h.ReceiveDeliveryEvent).Name("mail_delivery_events.store")
	// A signed token authenticates an unsubscribe; recipients and mail clients have no session.
	r.POST("/mail-unsubscribes/:token", h.Unsubscribe).Name("mail_unsubscribes.store")
}
