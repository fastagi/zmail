package roster

import (
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
)

// RegisterRoutes registers HTTP routes owned by the roster module.
func (h *Handler) RegisterRoutes(r *router.Router) {
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth", "organization_context")

		contacts := requireFeature(domain.MemberFeatureContacts)
		contactLists := requireFeature(domain.MemberFeatureContacts, domain.MemberFeatureBroadcasts)

		auth.GET("/mail-contacts", h.ListContacts).Name("mail_contacts.index").Middleware(contacts)
		auth.POST("/mail-contacts", h.CreateContact).Name("mail_contacts.store").Middleware(contacts)
		auth.GET("/mail-contacts/:contact_id", h.GetContact).Name("mail_contacts.show").WhereNumber("contact_id").Middleware(contacts)
		auth.PUT("/mail-contacts/:contact_id", h.UpdateContact).Name("mail_contacts.update").WhereNumber("contact_id").Middleware(contacts)
		auth.DELETE("/mail-contacts/:contact_id", h.DeleteContact).Name("mail_contacts.destroy").WhereNumber("contact_id").Middleware(contacts)
		auth.POST("/mail-contact-imports", h.ImportContacts).Name("mail_contact_imports.store").Middleware(contacts)
		auth.GET("/mail-contact-lists", h.ListContactLists).Name("mail_contact_lists.index").Middleware(contactLists)
		auth.POST("/mail-contact-lists", h.CreateContactList).Name("mail_contact_lists.store").Middleware(contacts)
		auth.PUT("/mail-contact-lists/:list_id", h.RenameContactList).Name("mail_contact_lists.update").WhereNumber("list_id").Middleware(contacts)
		auth.DELETE("/mail-contact-lists/:list_id", h.DeleteContactList).Name("mail_contact_lists.destroy").WhereNumber("list_id").Middleware(contacts)
	})
}
