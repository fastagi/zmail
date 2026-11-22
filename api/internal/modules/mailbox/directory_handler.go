package mailbox

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// ListMailboxDirectory returns one keyset page of the organization's mailboxes for owners and
// admins, with member counts, send attempts, and the organization's send total in meta.
func (h *Handler) ListMailboxDirectory(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	if _, present := c.GetQueryArray("page"); present {
		response.HandleError(c, "Invalid directory query", fmt.Errorf("%w: page is not supported; use cursor", domain.ErrInvalidInput))
		return
	}
	var request ListMailboxDirectoryRequest
	if !bindValidatedQuery(c, &request) {
		return
	}
	filter := domain.MailboxDirectoryFilter{
		Type:  domain.MailboxType(request.Type),
		Query: request.Q,
		Limit: request.PerPage,
	}
	if filter.Limit == 0 {
		filter.Limit = DefaultDirectoryPageSize
	}
	if request.Cursor != "" {
		before, err := decodeDirectoryCursor(request.Cursor)
		if err != nil {
			response.HandleError(c, "Invalid directory cursor", err)
			return
		}
		filter.BeforeID = before
	}
	page, err := h.service.MailboxDirectory(c.Request.Context(), viewer, filter)
	if err != nil {
		response.HandleError(c, "Failed to list the mailbox directory", err)
		return
	}
	next := ""
	if page.NextBeforeID != 0 {
		next = encodeDirectoryCursor(page.NextBeforeID)
	}
	items := make([]MailboxDirectoryEntryResponse, 0, len(page.Entries))
	for _, entry := range page.Entries {
		items = append(items, toMailboxDirectoryEntryResponse(entry))
	}
	privateResponse(c)
	response.SuccessCursorPageWithMeta(c, items, filter.Limit, next, map[string]any{
		"daily_send_limit": page.DailySendLimit,
		"sent_last_24h":    page.SentLast24h,
		"mailbox_count":    page.MailboxCount,
		"max_mailboxes":    h.service.MaxMailboxes(),
	})
}

// UpdateMailbox changes a mailbox's display name. Only owners and admins may.
func (h *Handler) UpdateMailbox(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	mailboxID, ok := httphandler.ParseID(c, "mailbox_id")
	if !ok {
		return
	}
	var request UpdateMailboxRequest
	if !httphandler.BindJSON(c, &request) {
		return
	}
	mailbox, err := h.service.RenameMailbox(c.Request.Context(), viewer, mailboxID, *request.DisplayName)
	if err != nil {
		response.HandleError(c, "Failed to rename mailbox", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailboxResponse(mailbox))
}
