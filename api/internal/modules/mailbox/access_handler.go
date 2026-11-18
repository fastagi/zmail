package mailbox

import (
	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// ListMailboxMembers returns the members of a shared mailbox, ordered by user id.
// fastbuild:bounded-list max=100 reason=a shared mailbox has at most domain.MaxMailboxMembers members
func (h *Handler) ListMailboxMembers(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	members, err := h.service.ListMailboxMembers(c.Request.Context(), viewer, mailboxID)
	if err != nil {
		response.HandleError(c, "Failed to list mailbox members", err)
		return
	}
	items := make([]MailboxMemberResponse, 0, len(members))
	for _, member := range members {
		items = append(items, toMailboxMemberResponse(member))
	}
	privateResponse(c)
	response.Success(c, items)
}

// SetMailboxMember adds a member to a shared mailbox or changes its permission.
func (h *Handler) SetMailboxMember(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	userID, ok := httphandler.ParseID(c, "user_id")
	if !ok {
		return
	}
	var req SetMailboxMemberRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	member, err := h.service.SetMailboxMember(c.Request.Context(), viewer, mailboxID, userID, domain.MailboxPermission(req.Permission))
	if err != nil {
		response.HandleError(c, "Failed to set mailbox member", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailboxMemberResponse(member))
}

// RemoveMailboxMember removes a member from a shared mailbox.
func (h *Handler) RemoveMailboxMember(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	userID, ok := httphandler.ParseID(c, "user_id")
	if !ok {
		return
	}
	if err := h.service.RemoveMailboxMember(c.Request.Context(), viewer, mailboxID, userID); err != nil {
		response.HandleError(c, "Failed to remove mailbox member", err)
		return
	}
	response.NoContent(c)
}

// ListMailboxAccessEvents returns a mailbox's access history, newest first.
func (h *Handler) ListMailboxAccessEvents(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	events, total, err := h.service.ListMailboxAccessEvents(c.Request.Context(), viewer, mailboxID, page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list mailbox access events", err)
		return
	}
	paginator := pagination.NewPaginator(toMailboxAccessEventResponses(events), total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}
