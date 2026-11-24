package triage

import (
	"github.com/fastagi/zmail/api/internal/infra/router"
)

// RegisterRoutes registers HTTP routes owned by the triage module.
func (h *Handler) RegisterRoutes(r *router.Router) {
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth", "organization_context")

		auth.GET("/mail-triage-setting", h.GetTriageSetting).Name("mail_triage_setting.show")
		auth.PUT("/mail-triage-setting", h.UpdateTriageSetting).Name("mail_triage_setting.update")
		auth.PUT("/mailboxes/:mailbox_id/messages/:message_id/triage", h.UpdateMessageTriage).
			Name("mailboxes.messages.triage.update").WhereNumber("mailbox_id").WhereNumber("message_id")
		auth.POST("/mailboxes/:mailbox_id/messages/:message_id/reply-drafts", h.CreateReplyDraft).
			Name("mailboxes.messages.reply_drafts.store").WhereNumber("mailbox_id").WhereNumber("message_id")
	})
}
