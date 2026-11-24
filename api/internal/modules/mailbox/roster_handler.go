package mailbox

import (
	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// MemberImportRowRequest is one roster row of POST /v1/member-imports.
type MemberImportRowRequest struct {
	Email   string `json:"email" binding:"required,max=320"`
	Name    string `json:"name" binding:"max=480"`
	Role    string `json:"role" binding:"max=16"`
	Mailbox string `json:"mailbox" binding:"max=320"`
	// Developer gives this member developer access on joining (the roster's developer=yes).
	Developer bool `json:"developer"`
}

// MemberImportFeaturesRequest is the grants every invited member of the import starts with.
type MemberImportFeaturesRequest struct {
	Broadcasts bool `json:"broadcasts"`
	Contacts   bool `json:"contacts"`
	Templates  bool `json:"templates"`
	APIKeys    bool `json:"api_keys"`
	// MailClients is granted when absent, so a client that predates the feature keeps the default.
	MailClients *bool `json:"mail_clients"`
	// Developer gives every invited member developer access on joining, scoped to the
	// organization's default mail domain then.
	Developer bool `json:"developer"`
}

// CreateMemberImportRequest is the body of POST /v1/member-imports. Features defaults to the
// new-member grants when absent.
type CreateMemberImportRequest struct {
	DryRun   *bool                        `json:"dry_run" binding:"required"`
	Domain   string                       `json:"domain" binding:"required,max=253"`
	Features *MemberImportFeaturesRequest `json:"features"`
	Rows     []MemberImportRowRequest     `json:"rows" binding:"required,min=1,max=500,dive"`
}

func (r CreateMemberImportRequest) input() MemberImportInput {
	features := domain.DefaultMemberFeatures()
	if r.Features != nil {
		features = domain.MemberFeatures{
			Broadcasts: r.Features.Broadcasts, Contacts: r.Features.Contacts,
			Templates: r.Features.Templates, APIKeys: r.Features.APIKeys, MailClients: true,
		}
		if r.Features.MailClients != nil {
			features.MailClients = *r.Features.MailClients
		}
		features.Developer = r.Features.Developer
	}
	rows := make([]MemberImportRow, 0, len(r.Rows))
	for _, row := range r.Rows {
		rows = append(rows, MemberImportRow(row))
	}
	return MemberImportInput{DryRun: *r.DryRun, Domain: r.Domain, Features: features, Rows: rows}
}

// MemberImportRowResultResponse is one row of a roster import result.
type MemberImportRowResultResponse struct {
	Row             int     `json:"row"`
	Email           string  `json:"email"`
	Address         string  `json:"address"`
	Status          string  `json:"status"`
	SkipReason      *string `json:"skip_reason"`
	ErrorCode       *string `json:"error_code"`
	MailboxID       *uint   `json:"mailbox_id"`
	InvitationID    *uint   `json:"invitation_id"`
	EmailSendStatus *string `json:"email_send_status"`
	// WelcomeMailStatus is null when the row provisioned no mailbox; WelcomeMailErrorCode is set
	// only when it is failed.
	WelcomeMailStatus    *string `json:"welcome_mail_status"`
	WelcomeMailErrorCode *string `json:"welcome_mail_error_code"`
}

// MemberImportResultResponse is the result of a roster import.
type MemberImportResultResponse struct {
	DryRun  bool `json:"dry_run"`
	Total   int  `json:"total"`
	Valid   int  `json:"valid"`
	Invited int  `json:"invited"`
	Created int  `json:"created"`
	Skipped int  `json:"skipped"`
	Failed  int  `json:"failed"`
	// WelcomeMailsQueued and WelcomeMailsFailed count rows by welcome mail outcome.
	WelcomeMailsQueued int `json:"welcome_mails_queued"`
	WelcomeMailsFailed int `json:"welcome_mails_failed"`
	// fastbuild:bounded-list max=500 reason=one result per request row, bounded by the request
	Rows []MemberImportRowResultResponse `json:"rows"`
}

func optionalUint(value uint) *uint {
	if value == 0 {
		return nil
	}
	return &value
}

func toMemberImportResultResponse(result *MemberImportResult) MemberImportResultResponse {
	rows := make([]MemberImportRowResultResponse, 0, len(result.Rows))
	for _, row := range result.Rows {
		rows = append(rows, MemberImportRowResultResponse{
			Row: row.Row, Email: row.Email, Address: row.Address, Status: string(row.Status),
			SkipReason: optionalText(string(row.SkipReason)), ErrorCode: optionalText(row.ErrorCode),
			MailboxID: optionalUint(row.MailboxID), InvitationID: optionalUint(row.InvitationID),
			EmailSendStatus:   optionalText(row.EmailSendStatus),
			WelcomeMailStatus: optionalText(string(row.WelcomeStatus)), WelcomeMailErrorCode: optionalText(row.WelcomeErrorCode),
		})
	}
	return MemberImportResultResponse{
		DryRun: result.DryRun, Total: result.Total, Valid: result.Valid, Invited: result.Invited,
		Created: result.Created, Skipped: result.Skipped, Failed: result.Failed,
		WelcomeMailsQueued: result.WelcomeQueued, WelcomeMailsFailed: result.WelcomeFailed, Rows: rows,
	}
}

// ImportMembers checks or applies a roster import. Only owners and admins may.
func (h *Handler) ImportMembers(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	var request CreateMemberImportRequest
	if !httphandler.BindJSON(c, &request) {
		return
	}
	result, err := h.roster.ImportMembers(c.Request.Context(), viewer, request.input())
	if err != nil {
		response.HandleError(c, "Failed to import members", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMemberImportResultResponse(result))
}
