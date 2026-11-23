package mailbox

import (
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
		defaults[string(language)] = MemberWelcomeCopyResponse{Subject: standard.Subject, Body: standard.Body}
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
