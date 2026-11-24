package mailbox

import (
	"github.com/gin-gonic/gin"

	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// maxImportBodyBytes bounds an import request: 500 rows at their longest cells fit well within it.
const maxImportBodyBytes = 6 << 20

// MailboxImportRowRequest is one row of POST /v1/mailbox-imports.
type MailboxImportRowRequest struct {
	Address     string `json:"address" binding:"required,max=320"`
	DisplayName string `json:"display_name" binding:"max=480"`
	Members     string `json:"members" binding:"required,max=8192"`
}

// CreateMailboxImportRequest is the body of POST /v1/mailbox-imports.
type CreateMailboxImportRequest struct {
	DryRun *bool                     `json:"dry_run" binding:"required"`
	Rows   []MailboxImportRowRequest `json:"rows" binding:"required,min=1,max=500,dive"`
}

// MailboxImportRowResultResponse is one row of an import result.
type MailboxImportRowResultResponse struct {
	Row           int      `json:"row"`
	Address       string   `json:"address"`
	Status        string   `json:"status"`
	MailboxID     *uint    `json:"mailbox_id"`
	ErrorCode     *string  `json:"error_code"`
	MissingEmails []string `json:"missing_emails"`
}

// MailboxImportResultResponse is the result of an import.
type MailboxImportResultResponse struct {
	DryRun    bool `json:"dry_run"`
	Succeeded int  `json:"succeeded"`
	Failed    int  `json:"failed"`
	// fastbuild:bounded-list max=500 reason=one result per request row, bounded by the request
	Rows []MailboxImportRowResultResponse `json:"rows"`
}

func toMailboxImportResultResponse(result *MailboxImportResult) MailboxImportResultResponse {
	rows := make([]MailboxImportRowResultResponse, 0, len(result.Rows))
	for _, row := range result.Rows {
		item := MailboxImportRowResultResponse{
			Row: row.Row, Address: row.Address, Status: string(row.Status), MissingEmails: row.MissingEmails,
		}
		if item.MissingEmails == nil {
			item.MissingEmails = []string{}
		}
		if row.MailboxID != 0 {
			id := row.MailboxID
			item.MailboxID = &id
		}
		if row.ErrorCode != "" {
			code := row.ErrorCode
			item.ErrorCode = &code
		}
		rows = append(rows, item)
	}
	return MailboxImportResultResponse{DryRun: result.DryRun, Succeeded: result.Succeeded, Failed: result.Failed, Rows: rows}
}

// ImportMailboxes checks or creates shared mailboxes from import rows. Only owners and admins may.
func (h *Handler) ImportMailboxes(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	var request CreateMailboxImportRequest
	if !httphandler.BindJSON(c, &request) {
		return
	}
	rows := make([]MailboxImportRow, 0, len(request.Rows))
	for _, row := range request.Rows {
		rows = append(rows, MailboxImportRow(row))
	}
	result, err := h.roster.ImportMailboxes(c.Request.Context(), viewer, rows, *request.DryRun)
	if err != nil {
		response.HandleError(c, "Failed to import mailboxes", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailboxImportResultResponse(result))
}
