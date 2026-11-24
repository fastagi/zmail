package mailbox

import (
	"github.com/fastagi/zmail/api/internal/domain"
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
		// A member uses broadcasts, contacts, and templates only with the matching grant from an
		// owner or admin. Composing a broadcast reads templates and list names, so that grant
		// reads them too; a template read also serves the developer pages an API key grant opens.
		// A developer writes transactional templates inside the grant's sending domains; the
		// service decides which kind and sender each caller may write.
		templates := requireFeature(domain.MemberFeatureTemplates)
		templateWrites := requireFeature(domain.MemberFeatureTemplates, domain.MemberFeatureDeveloper)
		templateReads := requireFeature(
			domain.MemberFeatureTemplates, domain.MemberFeatureBroadcasts, domain.MemberFeatureAPIKeys)

		auth.GET("/mailbox-domains", h.ListDomains).Name("mailbox_domains.index")

		auth.GET("/mail-cloudflare-connection", h.GetCloudflare).Name("mail_cloudflare_connection.show")
		auth.POST("/mail-cloudflare-connection", h.BindCloudflare).Name("mail_cloudflare_connection.store").Middleware(middleware.BodyLimit(2048))
		auth.DELETE("/mail-cloudflare-connection/:connection_id", h.UnbindCloudflare).Name("mail_cloudflare_connection.destroy")
		auth.POST("/mail-cloudflare-connection/:connection_id/checks", h.CheckCloudflareAccess).Name("mail_cloudflare_connection.checks.store")
		auth.POST("/mail-cloudflare-connection/:connection_id/domains", h.ConnectCloudflareDomain).Name("mail_cloudflare_connection.domains.store")
		auth.POST("/mail-cloudflare-previews", h.PreviewCloudflare).Name("mail_cloudflare_previews.store").Middleware(middleware.BodyLimit(2048))

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

		auth.GET("/platform-accounts", h.ListPlatformAccounts).Name("platform_accounts.index")
		auth.POST("/platform-accounts", h.CreatePlatformAccount).Name("platform_accounts.store")
		auth.PUT("/platform-accounts/:account_id", h.UpdatePlatformAccount).
			Name("platform_accounts.update").WhereNumber("account_id")
		auth.DELETE("/platform-accounts/:account_id", h.DeletePlatformAccount).
			Name("platform_accounts.destroy").WhereNumber("account_id")
		auth.GET("/platform-accounts/:account_id/events", h.ListPlatformAccountEvents).
			Name("platform_accounts.events.index").WhereNumber("account_id")
		auth.POST("/platform-account-handovers", h.HandOverPlatformAccounts).Name("platform_account_handovers.store")

		auth.GET("/external-mail-connectors", h.ListExternalMailConnectors).Name("external_mail_connectors.index")
		auth.POST("/external-mail-authorizations", h.StartExternalMailAuthorization).
			Name("external_mail_authorizations.store")
		auth.POST("/external-mail-authorizations/completions", h.CompleteExternalMailAuthorization).
			Name("external_mail_authorizations.completions.store")
		auth.POST("/external-mail-credentials", h.BindExternalMailCredentials).
			Name("external_mail_credentials.store").Middleware(middleware.BodyLimit(maxExternalMailCredentialsBodyBytes))
		auth.GET("/external-mail-connections", h.ListExternalMailConnections).Name("external_mail_connections.index")
		auth.DELETE("/external-mail-connections/:connection_id", h.RevokeExternalMailConnection).
			Name("external_mail_connections.destroy").WhereNumber("connection_id")
		auth.GET("/external-mail-connections/:connection_id/sync-runs", h.ListExternalMailSyncRuns).
			Name("external_mail_connections.sync_runs.index").WhereNumber("connection_id")
		auth.POST("/external-mail-connections/:connection_id/purges", h.PurgeExternalMailCopies).
			Name("external_mail_connections.purges.store").WhereNumber("connection_id")
		// Development only: exists while `fake` is the active external mail source.
		h.registerFakeExternalMailDelivery(auth)
		auth.GET("/external-mail-connection-directory", h.ListExternalMailConnectionDirectory).
			Name("external_mail_connection_directory.index")
		auth.DELETE("/external-mail-connection-directory/:connection_id", h.ForceDisconnectExternalMailConnection).
			Name("external_mail_connection_directory.destroy").WhereNumber("connection_id")
		// Mail client passwords: listing and revoking need no feature, so a member can always
		// remove a device; creating needs mail_clients.
		auth.GET("/mail-app-passwords", h.ListAppPasswords).Name("mail_app_passwords.index")
		auth.POST("/mail-app-passwords", h.CreateAppPassword).Name("mail_app_passwords.store").
			Middleware(requireFeature(domain.MemberFeatureMailClients), middleware.BodyLimit(maxAppPasswordBodyBytes))
		auth.DELETE("/mail-app-passwords/:password_id", h.RevokeAppPassword).
			Name("mail_app_passwords.destroy").WhereNumber("password_id")
		auth.GET("/mail-app-password-directory", h.ListAppPasswordDirectory).Name("mail_app_password_directory.index")
		auth.DELETE("/mail-app-password-directory/:password_id", h.ForceRevokeAppPassword).
			Name("mail_app_password_directory.destroy").WhereNumber("password_id")
		auth.GET("/mail-client-settings", h.GetMailClientSettings).Name("mail_client_settings.show")
		auth.PUT("/mail-client-settings", h.UpdateMailClientSettings).Name("mail_client_settings.update")
		// Tenants are session-only: no API key, and so no agent, ever reaches tenant-level access.
		auth.GET("/external-mail-tenants", h.ListExternalMailTenants).Name("external_mail_tenants.index")
		auth.POST("/external-mail-tenants", h.AuthorizeExternalMailTenant).
			Name("external_mail_tenants.store").Middleware(middleware.BodyLimit(maxExternalMailTenantBodyBytes))
		auth.DELETE("/external-mail-tenants/:tenant_id", h.RevokeExternalMailTenant).
			Name("external_mail_tenants.destroy").WhereNumber("tenant_id")
		auth.GET("/external-mail-tenants/:tenant_id/directory", h.ListExternalMailTenantDirectory).
			Name("external_mail_tenants.directory.index").WhereNumber("tenant_id")
		auth.POST("/external-mail-tenants/:tenant_id/assignments", h.AssignExternalMailTenantMailboxes).
			Name("external_mail_tenants.assignments.store").WhereNumber("tenant_id").
			Middleware(middleware.BodyLimit(maxExternalMailAssignmentsBodyBytes))

		auth.GET("/mail-templates", h.ListTemplates).Name("mail_templates.index").Middleware(templateReads)
		auth.POST("/mail-templates", h.CreateTemplate).Name("mail_templates.store").Middleware(templateWrites)
		auth.GET("/mail-templates/:template_id", h.GetTemplate).Name("mail_templates.show").WhereNumber("template_id").Middleware(templateReads)
		auth.PUT("/mail-templates/:template_id", h.UpdateTemplate).Name("mail_templates.update").WhereNumber("template_id").Middleware(templateWrites)
		auth.DELETE("/mail-templates/:template_id", h.DeleteTemplate).Name("mail_templates.destroy").WhereNumber("template_id").Middleware(templateWrites)
		auth.POST("/mail-templates/:template_id/tests", h.SendTemplateTest).
			Name("mail_templates.tests.store").WhereNumber("template_id").Middleware(templates)
		auth.POST("/mail-template-previews", h.PreviewTemplate).Name("mail_template_previews.store").Middleware(templateReads)

		auth.GET("/mailboxes", h.ListMailboxes).Name("mailboxes.index")
		auth.POST("/mailboxes", h.CreateMailbox).Name("mailboxes.store")
		auth.PUT("/mailboxes/:mailbox_id", h.UpdateMailbox).Name("mailboxes.update").WhereNumber("mailbox_id")
		auth.DELETE("/mailboxes/:mailbox_id", h.DeleteMailbox).Name("mailboxes.destroy").WhereNumber("mailbox_id")
		auth.GET("/mailbox-directory", h.ListMailboxDirectory).Name("mailbox_directory.index")
		auth.POST("/mailbox-imports", h.ImportMailboxes).Name("mailbox_imports.store").
			Middleware(middleware.BodyLimit(maxImportBodyBytes))
		auth.GET("/mailbox-exports", h.ExportMailboxes).Name("mailbox_exports.index")
		auth.POST("/member-imports", h.ImportMembers).Name("member_imports.store").
			Middleware(middleware.BodyLimit(maxImportBodyBytes))
		auth.GET("/member-welcome-mail-setting", h.GetMemberWelcomeSetting).Name("member_welcome_mail_setting.show")
		auth.PUT("/member-welcome-mail-setting", h.UpdateMemberWelcomeSetting).Name("member_welcome_mail_setting.update").
			Middleware(middleware.BodyLimit(maxMemberWelcomeBodyBytes))
		auth.POST("/member-welcome-mail-previews", h.PreviewMemberWelcome).Name("member_welcome_mail_previews.store").
			Middleware(middleware.BodyLimit(maxMemberWelcomeBodyBytes))
		auth.GET("/member-welcome-mails", h.ListMemberWelcomeMails).Name("member_welcome_mails.index")
		auth.POST("/member-welcome-mails", h.ResendMemberWelcomeMail).Name("member_welcome_mails.store").
			Middleware(middleware.BodyLimit(maxMemberWelcomeBodyBytes))

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
		auth.PUT("/mailboxes/:mailbox_id/triage-setting", h.SetMailboxTriageSetting).
			Name("mailboxes.triage_setting.update").WhereNumber("mailbox_id")
		auth.GET("/mailboxes/:mailbox_id/signature", h.GetMailSignature).
			Name("mailboxes.signature.show").WhereNumber("mailbox_id")
		auth.PUT("/mailboxes/:mailbox_id/signature", h.UpdateMailSignature).
			Name("mailboxes.signature.update").WhereNumber("mailbox_id")
		auth.GET("/mail-drafts", h.ListMailDrafts).Name("mail_drafts.index")
		auth.POST("/mail-drafts", h.CreateMailDraft).Name("mail_drafts.store")
		auth.GET("/mail-drafts/:draft_id", h.ShowMailDraft).
			Name("mail_drafts.show").WhereNumber("draft_id")
		auth.PUT("/mail-drafts/:draft_id", h.UpdateMailDraft).
			Name("mail_drafts.update").WhereNumber("draft_id")
		auth.DELETE("/mail-drafts/:draft_id", h.DeleteMailDraft).
			Name("mail_drafts.destroy").WhereNumber("draft_id")

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
		auth.GET("/mailboxes/:mailbox_id/threads/:thread_id/assignment", h.GetMailThreadAssignment).
			Name("mailboxes.threads.assignment.show").WhereNumber("mailbox_id", "thread_id")
		auth.PUT("/mailboxes/:mailbox_id/threads/:thread_id/assignment", h.UpdateMailThreadAssignment).
			Name("mailboxes.threads.assignment.update").WhereNumber("mailbox_id", "thread_id")
		auth.GET("/mailboxes/:mailbox_id/threads/:thread_id/notes", h.ListMailThreadNotes).
			Name("mailboxes.threads.notes.index").WhereNumber("mailbox_id", "thread_id")
		auth.POST("/mailboxes/:mailbox_id/threads/:thread_id/notes", h.CreateMailThreadNote).
			Name("mailboxes.threads.notes.store").WhereNumber("mailbox_id", "thread_id")
		auth.DELETE("/mailboxes/:mailbox_id/threads/:thread_id/notes/:note_id", h.DeleteMailThreadNote).
			Name("mailboxes.threads.notes.destroy").WhereNumber("mailbox_id", "thread_id", "note_id")
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
		// A member's keys work in an organization only while it grants the member API keys.
		key.Use(requireFeature(domain.MemberFeatureAPIKeys))

		key.POST("/outbound-mail-messages", h.SendOutbound).
			Name("outbound_mail_messages.store").Middleware(apikey.RequireScopes(ScopeMailSend))
		key.GET("/outbound-mail-messages/:message_id", h.GetOutbound).
			Name("outbound_mail_messages.show").WhereNumber("message_id").
			Middleware(apikey.RequireScopes(ScopeMailRead))

		// Agents: message reads stay with mailbox's message response mapper; grants, events, and
		// drafts are owned by the top-level agentgrant module.
		key.GET("/agent-mail-messages/:message_id", h.GetAgentMailMessage).
			Name("agent_mail_messages.show").WhereNumber("message_id").
			Middleware(apikey.RequireScopes(ScopeMailAgent))

		// AI tools save templates with their owner's key. Creating needs the owner's templates
		// grant, exactly as on the session route, and never sends mail.
		key.POST("/agent-mail-templates", h.CreateTemplate).
			Name("agent_mail_templates.store").
			Middleware(requireFeature(domain.MemberFeatureTemplates), apikey.RequireScopes(apikey.ScopeTemplatesCreate))
	})

	r.POST("/cloudflare-connections/:connection_id/inbound-mail-messages", h.ReceiveInbound).Name("cloudflare_connections.inbound.store")
	r.POST("/inbound-mail-messages", h.ReceiveInbound).Name("inbound_mail_messages.store")
	r.POST("/mail-delivery-events", h.ReceiveDeliveryEvent).Name("mail_delivery_events.store")
	// Provider notifications are authenticated by the provider's own proof (a Google-signed token,
	// a Graph subscription's clientState), never by a session or key; they only mark a connection due.
	r.POST("/external-mail-notifications/gmail", h.ReceiveGmailPush).Name("external_mail_notifications.gmail.store").
		Middleware(middleware.BodyLimit(maxExternalMailPushBodyBytes))
	r.POST("/external-mail-notifications/outlook", h.ReceiveOutlookPush).Name("external_mail_notifications.outlook.store").
		Middleware(middleware.BodyLimit(maxExternalMailPushBodyBytes))
}
