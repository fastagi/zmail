package mailbox

import (
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// maxMemberWelcomeBodyBytes bounds a welcome setting or preview request.
const maxMemberWelcomeBodyBytes = 32 * 1024

// UpdateMemberWelcomeSettingRequest replaces the organization's welcome mail setting.
type UpdateMemberWelcomeSettingRequest struct {
	Enabled  *bool  `json:"enabled" binding:"required"`
	Language string `json:"language" binding:"required,oneof=en-US zh-Hans"`
	Subject  string `json:"subject" binding:"max=800"`
	Body     string `json:"body" binding:"max=20000"`
}

// MemberWelcomePreviewRequest is welcome copy to render with sample values.
type MemberWelcomePreviewRequest struct {
	Language string `json:"language" binding:"required,oneof=en-US zh-Hans"`
	Subject  string `json:"subject" binding:"max=800"`
	Body     string `json:"body" binding:"max=20000"`
}

// MemberWelcomeCopyResponse is welcome copy: a subject and a plain-text body with {{variables}}.
type MemberWelcomeCopyResponse struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// MemberWelcomeSettingResponse is the organization's welcome mail setting.
type MemberWelcomeSettingResponse struct {
	Enabled    bool   `json:"enabled"`
	Language   string `json:"language"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	Customized bool   `json:"customized"`
	// fastbuild:bounded-list max=4 reason=the code-owned welcome mail variables
	Variables []string                             `json:"variables"`
	Defaults  map[string]MemberWelcomeCopyResponse `json:"defaults"`
	UpdatedAt *time.Time                           `json:"updated_at"`
}

// MemberWelcomePreviewResponse is a welcome mail rendered with sample values.
type MemberWelcomePreviewResponse struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

func toMemberWelcomeSettingResponse(setting MemberWelcomeSetting) MemberWelcomeSettingResponse {
	effective := setting.Effective()
	defaults := make(map[string]MemberWelcomeCopyResponse, len(memberWelcomeLanguages))
	for _, language := range memberWelcomeLanguages {
		standard := MemberWelcomeDefault(language)
		defaults[string(language)] = MemberWelcomeCopyResponse(standard)
	}
	return MemberWelcomeSettingResponse{
		Enabled: setting.Enabled, Language: string(setting.Language),
		Subject: effective.Subject, Body: effective.Body, Customized: setting.Customized(),
		Variables: append([]string(nil), MemberWelcomeVariables...), Defaults: defaults, UpdatedAt: setting.UpdatedAt,
	}
}

// GetMemberWelcomeSetting reads the organization's welcome mail setting; owners and admins only.
func (h *Handler) GetMemberWelcomeSetting(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	setting, err := h.service.MemberWelcomeSetting(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to read the welcome mail setting", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMemberWelcomeSettingResponse(setting))
}

// UpdateMemberWelcomeSetting replaces the organization's welcome mail setting.
func (h *Handler) UpdateMemberWelcomeSetting(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	var req UpdateMemberWelcomeSettingRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	setting, err := h.service.UpdateMemberWelcomeSetting(c.Request.Context(), viewer, MemberWelcomeInput{
		Enabled: *req.Enabled, Language: MemberWelcomeLanguage(req.Language), Subject: req.Subject, Body: req.Body,
	})
	if err != nil {
		respondOrganizeError(c, "Failed to update the welcome mail setting", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMemberWelcomeSettingResponse(setting))
}

// PreviewMemberWelcome renders welcome copy with sample values without saving or sending it.
func (h *Handler) PreviewMemberWelcome(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	var req MemberWelcomePreviewRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	preview, err := h.service.PreviewMemberWelcome(c.Request.Context(), viewer, MemberWelcomePreviewInput{
		Language: MemberWelcomeLanguage(req.Language), Subject: req.Subject, Body: req.Body,
	})
	if err != nil {
		respondOrganizeError(c, "Failed to preview the welcome mail", err)
		return
	}
	privateResponse(c)
	response.Success(c, MemberWelcomePreviewResponse(preview))
}

// ResendMemberWelcomeMailRequest names the mailbox whose welcome mail is sent again.
type ResendMemberWelcomeMailRequest struct {
	MailboxID uint `json:"mailbox_id" binding:"required,min=1"`
}

// MemberWelcomeMailResponse is a mailbox that can be welcomed with its latest welcome mail.
type MemberWelcomeMailResponse struct {
	MailboxID         uint       `json:"mailbox_id"`
	Address           string     `json:"address"`
	DisplayName       string     `json:"display_name"`
	OwnerUserID       *uint      `json:"owner_user_id"`
	InvitationPending bool       `json:"invitation_pending"`
	Status            string     `json:"status"`
	FailureCode       *string    `json:"failure_code"`
	QueuedAt          *time.Time `json:"queued_at"`
	SentAt            *time.Time `json:"sent_at"`
	ResendCount       int        `json:"resend_count"`
}

func toMemberWelcomeMailResponse(mail MemberWelcomeMail) MemberWelcomeMailResponse {
	return MemberWelcomeMailResponse{
		MailboxID: mail.MailboxID, Address: mail.Address, DisplayName: mail.DisplayName,
		OwnerUserID: mail.OwnerUserID, InvitationPending: mail.InvitationPending, Status: string(mail.Status),
		FailureCode: optionalText(mail.FailureCode), QueuedAt: mail.QueuedAt, SentAt: mail.SentAt,
		ResendCount: mail.ResendCount,
	}
}

// ListMemberWelcomeMails lists the mailboxes that can be welcomed with their latest welcome mail.
// fastbuild:bounded-list max=5000 reason=at most one entry per mailbox, capped by domain.MailboxesPerOrganizationCeiling
func (h *Handler) ListMemberWelcomeMails(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	mails, err := h.service.ListMemberWelcomeMails(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to list welcome mails", err)
		return
	}
	items := make([]MemberWelcomeMailResponse, 0, len(mails))
	for _, mail := range mails {
		items = append(items, toMemberWelcomeMailResponse(mail))
	}
	privateResponse(c)
	response.Success(c, items)
}

// ResendMemberWelcomeMail sends a mailbox's welcome mail again.
func (h *Handler) ResendMemberWelcomeMail(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	var req ResendMemberWelcomeMailRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	mail, err := h.service.ResendMemberWelcome(c.Request.Context(), viewer, req.MailboxID)
	if err != nil {
		var retry *RetryAfterError
		if errors.As(err, &retry) {
			c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.RetryAfter.Seconds())))))
		}
		respondOrganizeError(c, "Failed to resend the welcome mail", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMemberWelcomeMailResponse(mail))
}
