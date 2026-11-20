package mailbox

import (
	"github.com/fastagi/zmail/api/internal/infra/middleware"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
)

// RegisterRoutes registers HTTP routes for the mailbox starter. Member routes require a
// signed-in user and an organization the user belongs to, only ever touch that organization's
// mailboxes, and only the mailboxes the user's access allows. The inbound route is authenticated by the
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
		auth.POST("/mail-domains/:domain_id/receiving", h.EnableMailDomainReceiving).
			Name("mail_domains.receiving.store").WhereNumber("domain_id")
		auth.POST("/mail-domain-previews", h.PreviewMailDomain).Name("mail_domain_previews.store")

		auth.GET("/mail-zones", h.ListMailZones).Name("mail_zones.index")
		auth.GET("/mail-zones/:zone_id", h.GetMailZone).Name("mail_zones.show").WhereNumber("zone_id")
		auth.DELETE("/mail-zones/:zone_id", h.DeleteMailZone).Name("mail_zones.destroy").WhereNumber("zone_id")
		auth.GET("/mail-zones/:zone_id/dns-records", h.ListMailDNSRecords).
			Name("mail_zones.dns_records.index").WhereNumber("zone_id")
		auth.POST("/mail-zones/:zone_id/dns-records", h.CreateMailDNSRecord).
			Name("mail_zones.dns_records.store").WhereNumber("zone_id")
		auth.PATCH("/mail-zones/:zone_id/dns-records/:record_id", h.UpdateMailDNSRecord).
			Name("mail_zones.dns_records.update").WhereNumber("zone_id").WhereAlphaNumeric("record_id")
		auth.DELETE("/mail-zones/:zone_id/dns-records/:record_id", h.DeleteMailDNSRecord).
			Name("mail_zones.dns_records.destroy").WhereNumber("zone_id").WhereAlphaNumeric("record_id")

		auth.GET("/mail-messages", h.ListLog).Name("mail_messages.index")
		auth.GET("/mail-message-stats", h.ListStats).Name("mail_message_stats.index")
		auth.GET("/inbox-messages", h.ListInboxMessages).Name("inbox_messages.index")
		auth.GET("/inbox-message-counts", h.InboxMessageCounts).Name("inbox_message_counts.index")

		auth.GET("/mail-roles", h.ListMailRoles).Name("mail_roles.index")
		auth.POST("/mail-roles", h.CreateMailRole).Name("mail_roles.store")
		auth.PUT("/mail-roles/:role_id", h.UpdateMailRole).Name("mail_roles.update").WhereNumber("role_id")
		auth.DELETE("/mail-roles/:role_id", h.DeleteMailRole).Name("mail_roles.destroy").WhereNumber("role_id")

		auth.GET("/transactional-mail-log", h.ListTransactionalLog).Name("transactional_mail_log.index")
		auth.GET("/transactional-mail-log/:message_id", h.GetTransactionalLogEntry).
			Name("transactional_mail_log.show").WhereNumber("message_id")
		auth.POST("/transactional-test-sends", h.SendTransactionalTest).
			Name("transactional_test_sends.store").Middleware(middleware.BodyLimit(maxTransactionalBodyBytes))

		auth.GET("/mail-send-lanes", h.ListSendLanes).Name("mail_send_lanes.index")
		auth.GET("/mail-suppressions", h.ListSuppressions).Name("mail_suppressions.index")
		auth.DELETE("/mail-suppressions/:suppression_id", h.RemoveSuppression).
			Name("mail_suppressions.destroy").WhereNumber("suppression_id")

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
		auth.POST("/mail-broadcasts/:broadcast_id/pauses", h.PauseBroadcast).
			Name("mail_broadcasts.pauses.store").WhereNumber("broadcast_id")
		auth.POST("/mail-broadcasts/:broadcast_id/resumptions", h.ResumeBroadcast).
			Name("mail_broadcasts.resumptions.store").WhereNumber("broadcast_id")
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
		auth.PUT("/mailboxes/:mailbox_id", h.UpdateMailbox).Name("mailboxes.update").WhereNumber("mailbox_id")
		auth.DELETE("/mailboxes/:mailbox_id", h.DeleteMailbox).Name("mailboxes.destroy").WhereNumber("mailbox_id")
		auth.GET("/mailbox-directory", h.ListMailboxDirectory).Name("mailbox_directory.index")
		auth.POST("/mailbox-imports", h.ImportMailboxes).Name("mailbox_imports.store").
			Middleware(middleware.BodyLimit(maxImportBodyBytes))
		auth.GET("/mailbox-exports", h.ExportMailboxes).Name("mailbox_exports.index")

		auth.GET("/mailboxes/:mailbox_id/members", h.ListMailboxMembers).
			Name("mailboxes.members.index").WhereNumber("mailbox_id")
		auth.PUT("/mailboxes/:mailbox_id/members/:user_id", h.SetMailboxMember).
			Name("mailboxes.members.update").WhereNumber("mailbox_id").WhereNumber("user_id")
		auth.DELETE("/mailboxes/:mailbox_id/members/:user_id", h.RemoveMailboxMember).
			Name("mailboxes.members.destroy").WhereNumber("mailbox_id").WhereNumber("user_id")
		auth.GET("/mailboxes/:mailbox_id/access-events", h.ListMailboxAccessEvents).
			Name("mailboxes.access_events.index").WhereNumber("mailbox_id")
		auth.POST("/mailboxes/:mailbox_id/handovers", h.HandOverMailbox).
			Name("mailboxes.handovers.store").WhereNumber("mailbox_id")
		auth.DELETE("/mailboxes/:mailbox_id/sealed-messages", h.DeleteMailboxSealedMessages).
			Name("mailboxes.sealed_messages.destroy").WhereNumber("mailbox_id")
		auth.PUT("/mailboxes/:mailbox_id/webhook-setting", h.SetMailboxWebhookSetting).
			Name("mailboxes.webhook_setting.update").WhereNumber("mailbox_id")
		auth.PUT("/mailboxes/:mailbox_id/role-assignment", h.AssignMailboxRole).
			Name("mailboxes.role_assignment.update").WhereNumber("mailbox_id")

		auth.GET("/mailboxes/:mailbox_id/messages", h.ListMessages).
			Name("mailboxes.messages.index").WhereNumber("mailbox_id")
		auth.POST("/mailboxes/:mailbox_id/messages", h.SendMessage).
			Name("mailboxes.messages.store").WhereNumber("mailbox_id")
		auth.GET("/mailboxes/:mailbox_id/messages/:message_id", h.GetMessage).
			Name("mailboxes.messages.show").WhereNumber("mailbox_id").WhereNumber("message_id")
		auth.POST("/mailboxes/:mailbox_id/messages/:message_id/reveal", h.RevealMessage).
			Name("mailboxes.messages.reveal").WhereNumber("mailbox_id").WhereNumber("message_id")
		auth.GET("/mailboxes/:mailbox_id/messages/:message_id/attachments/:attachment_id", h.DownloadAttachment).
			Name("mailboxes.messages.attachments.show").
			WhereNumber("mailbox_id").WhereNumber("message_id").WhereNumber("attachment_id")
		auth.PUT("/mailboxes/:mailbox_id/messages/:message_id", h.UpdateMessage).
			Name("mailboxes.messages.update").WhereNumber("mailbox_id").WhereNumber("message_id")
		auth.DELETE("/mailboxes/:mailbox_id/messages/:message_id", h.DeleteMessage).
			Name("mailboxes.messages.destroy").WhereNumber("mailbox_id").WhereNumber("message_id")
		auth.GET("/mailboxes/:mailbox_id/threads/:thread_id", h.GetThread).
			Name("mailboxes.threads.show").WhereNumber("mailbox_id").WhereNumber("thread_id")
		auth.GET("/mailboxes/:mailbox_id/message-counts", h.MessageCounts).
			Name("mailboxes.message_counts.show").WhereNumber("mailbox_id")
		auth.POST("/mailboxes/:mailbox_id/message-updates", h.UpdateMessages).
			Name("mailboxes.message_updates.store").WhereNumber("mailbox_id")
		auth.POST("/mailboxes/:mailbox_id/trash-purges", h.PurgeTrash).
			Name("mailboxes.trash_purges.store").WhereNumber("mailbox_id")

		auth.GET("/mailboxes/:mailbox_id/mail-folders", h.ListFolders).
			Name("mailboxes.mail_folders.index").WhereNumber("mailbox_id")
		auth.POST("/mailboxes/:mailbox_id/mail-folders", h.CreateFolder).
			Name("mailboxes.mail_folders.store").WhereNumber("mailbox_id")
		auth.PUT("/mailboxes/:mailbox_id/mail-folders/:folder_id", h.RenameFolder).
			Name("mailboxes.mail_folders.update").WhereNumber("mailbox_id").WhereNumber("folder_id")
		auth.DELETE("/mailboxes/:mailbox_id/mail-folders/:folder_id", h.DeleteFolder).
			Name("mailboxes.mail_folders.destroy").WhereNumber("mailbox_id").WhereNumber("folder_id")
		auth.GET("/mailboxes/:mailbox_id/mail-labels", h.ListLabels).
			Name("mailboxes.mail_labels.index").WhereNumber("mailbox_id")
		auth.POST("/mailboxes/:mailbox_id/mail-labels", h.CreateLabel).
			Name("mailboxes.mail_labels.store").WhereNumber("mailbox_id")
		auth.PUT("/mailboxes/:mailbox_id/mail-labels/:label_id", h.UpdateLabel).
			Name("mailboxes.mail_labels.update").WhereNumber("mailbox_id").WhereNumber("label_id")
		auth.DELETE("/mailboxes/:mailbox_id/mail-labels/:label_id", h.DeleteLabel).
			Name("mailboxes.mail_labels.destroy").WhereNumber("mailbox_id").WhereNumber("label_id")
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

		// Transactional mail accepts test keys: a test key's messages are simulated and never
		// reach the provider.
		key.POST("/transactional-messages", h.SendTransactional).
			Name("transactional_messages.store").
			Middleware(middleware.BodyLimit(maxTransactionalBodyBytes), apikey.RequireScopesAllowingTest(ScopeMailTransactional))
		key.GET("/transactional-messages/:message_id", h.GetTransactional).
			Name("transactional_messages.show").WhereNumber("message_id").
			Middleware(apikey.RequireScopesAllowingTest(ScopeMailTransactional))
	})

	r.POST("/inbound-mail-messages", h.ReceiveInbound).Name("inbound_mail_messages.store")
	r.POST("/mail-delivery-events", h.ReceiveDeliveryEvent).Name("mail_delivery_events.store")
	// A signed token authenticates an unsubscribe; recipients and mail clients have no session.
	r.POST("/mail-unsubscribes/:token", h.Unsubscribe).Name("mail_unsubscribes.store")
}
