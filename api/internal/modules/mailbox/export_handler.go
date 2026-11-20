package mailbox

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// ExportMailboxesRequest is the query of GET /v1/mailbox-exports.
type ExportMailboxesRequest struct {
	Type string `form:"type" binding:"required,oneof=shared personal"`
}

// ExportMailboxes answers every mailbox of one type as a CSV file. Only owners and admins may.
func (h *Handler) ExportMailboxes(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	var request ExportMailboxesRequest
	if !bindValidatedQuery(c, &request) {
		return
	}
	mailboxType := domain.MailboxType(request.Type)
	rows, err := h.service.ExportMailboxes(c.Request.Context(), viewer, mailboxType)
	if err != nil {
		response.HandleError(c, "Failed to export mailboxes", err)
		return
	}
	// The file is built in memory so a failure still answers a JSON error, not half a file.
	var body bytes.Buffer
	if err = writeMailboxCSV(&body, rows); err != nil {
		response.HandleError(c, "Failed to export mailboxes", err)
		return
	}
	privateResponse(c)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", `attachment; filename="`+exportFilename(mailboxType, h.service.Now())+`"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", body.Bytes())
}
