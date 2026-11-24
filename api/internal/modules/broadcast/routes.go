package broadcast

import (
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
)

// RegisterRoutes registers HTTP routes owned by the broadcast module.
func (h *Handler) RegisterRoutes(r *router.Router) {
	r.Group("", func(auth *router.Router) {
		auth.WithMiddleware("auth", "organization_context")

		broadcasts := requireFeature(domain.MemberFeatureBroadcasts)

		auth.GET("/mail-broadcasts", h.ListBroadcasts).Name("mail_broadcasts.index").Middleware(broadcasts)
		auth.POST("/mail-broadcasts", h.CreateBroadcast).Name("mail_broadcasts.store").Middleware(broadcasts)
		auth.GET("/mail-broadcasts/:broadcast_id", h.GetBroadcast).Name("mail_broadcasts.show").WhereNumber("broadcast_id").Middleware(broadcasts)
		auth.PUT("/mail-broadcasts/:broadcast_id", h.UpdateBroadcast).Name("mail_broadcasts.update").WhereNumber("broadcast_id").Middleware(broadcasts)
		auth.DELETE("/mail-broadcasts/:broadcast_id", h.DeleteBroadcast).Name("mail_broadcasts.destroy").WhereNumber("broadcast_id").Middleware(broadcasts)
		auth.POST("/mail-broadcasts/:broadcast_id/sends", h.SendBroadcast).
			Name("mail_broadcasts.sends.store").WhereNumber("broadcast_id").Middleware(broadcasts)
		auth.POST("/mail-broadcasts/:broadcast_id/cancellations", h.CancelBroadcast).
			Name("mail_broadcasts.cancellations.store").WhereNumber("broadcast_id").Middleware(broadcasts)
		auth.POST("/mail-broadcasts/:broadcast_id/pauses", h.PauseBroadcast).
			Name("mail_broadcasts.pauses.store").WhereNumber("broadcast_id").Middleware(broadcasts)
		auth.POST("/mail-broadcasts/:broadcast_id/resumptions", h.ResumeBroadcast).
			Name("mail_broadcasts.resumptions.store").WhereNumber("broadcast_id").Middleware(broadcasts)
		auth.GET("/mail-broadcasts/:broadcast_id/recipients", h.ListBroadcastRecipients).
			Name("mail_broadcasts.recipients.index").WhereNumber("broadcast_id").Middleware(broadcasts)
	})

	r.POST("/mail-unsubscribes/:token", h.Unsubscribe).Name("mail_unsubscribes.store")
}
