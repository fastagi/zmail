package mailbox

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// MailTemplateRequest creates or replaces a template.
type MailTemplateRequest struct {
	Name            string            `json:"name" binding:"required,max=100"`
	Subject         string            `json:"subject" binding:"required,max=998"`
	HTML            string            `json:"html" binding:"max=262144"`
	Text            string            `json:"text" binding:"max=262144"`
	SampleVariables map[string]string `json:"sample_variables" binding:"omitempty,max=100"`
	// Design is the visual editor's document; the console renders html and text from it.
	Design json.RawMessage `json:"design"`
}

func (r MailTemplateRequest) input() TemplateInput {
	return TemplateInput(r)
}

// MailTemplatePreviewRequest renders a stored template or unsaved source; it sends nothing.
type MailTemplatePreviewRequest struct {
	TemplateID uint                 `json:"template_id"`
	Source     *MailTemplateRequest `json:"source"`
	Variables  map[string]string    `json:"variables" binding:"omitempty,max=100"`
}

// MailTemplateTestRequest sends a template to test addresses from one of the organization's
// mailboxes.
type MailTemplateTestRequest struct {
	MailboxID uint              `json:"mailbox_id" binding:"required,min=1"`
	To        []string          `json:"to" binding:"required,min=1,max=5,dive,required,max=320"`
	Variables map[string]string `json:"variables" binding:"omitempty,max=100"`
}

// MailTemplateSendRequest sends a stored template in place of a subject and body.
type MailTemplateSendRequest struct {
	ID        uint              `json:"id" binding:"required,min=1"`
	Variables map[string]string `json:"variables" binding:"omitempty,max=100"`
}

// MailTemplateSummaryResponse is the listing shape of a template; it carries no bodies.
type MailTemplateSummaryResponse struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Subject   string    `json:"subject"`
	Variables []string  `json:"variables"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MailTemplateResponse is the full shape of a template.
type MailTemplateResponse struct {
	MailTemplateSummaryResponse
	HTMLBody        string            `json:"html_body"`
	TextBody        string            `json:"text_body"`
	SampleVariables map[string]string `json:"sample_variables"`
	// Design is null for a template written in HTML.
	Design json.RawMessage `json:"design"`
}

// MailTemplatePreviewResponse is a rendered template.
type MailTemplatePreviewResponse struct {
	Subject   string   `json:"subject"`
	HTMLBody  string   `json:"html_body"`
	TextBody  string   `json:"text_body"`
	Variables []string `json:"variables"`
	// Missing lists variables with neither a value nor a fallback; a real send would be refused.
	Missing []string `json:"missing"`
}

func toMailTemplateSummary(template *domain.MailTemplate) MailTemplateSummaryResponse {
	return MailTemplateSummaryResponse{
		ID:        template.ID,
		Name:      template.Name,
		Subject:   template.Subject,
		Variables: TemplateVariables(template),
		CreatedAt: template.CreatedAt,
		UpdatedAt: template.UpdatedAt,
	}
}

func toMailTemplateResponse(template *domain.MailTemplate) MailTemplateResponse {
	samples := template.SampleVariables
	if samples == nil {
		samples = map[string]string{}
	}
	return MailTemplateResponse{
		MailTemplateSummaryResponse: toMailTemplateSummary(template),
		HTMLBody:                    template.HTMLBody,
		TextBody:                    template.TextBody,
		SampleVariables:             samples,
		Design:                      designJSON(template.Design),
	}
}

func designJSON(design string) json.RawMessage {
	if design == "" {
		return json.RawMessage("null")
	}
	return json.RawMessage(design)
}

// ListTemplates returns every template of the organization by name, without bodies.
// fastbuild:bounded-list max=200 reason=an organization keeps at most MaxMailTemplatesPerOrganization templates
func (h *Handler) ListTemplates(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	templates, err := h.service.ListTemplates(c.Request.Context(), organizationID)
	if err != nil {
		response.HandleError(c, "Failed to list mail templates", err)
		return
	}
	summaries := make([]MailTemplateSummaryResponse, 0, len(templates))
	for _, template := range templates {
		summaries = append(summaries, toMailTemplateSummary(template))
	}
	privateResponse(c)
	response.Success(c, summaries)
}

func (h *Handler) GetTemplate(c *gin.Context) {
	organizationID, templateID, ok := templateScope(c)
	if !ok {
		return
	}
	template, err := h.service.GetTemplate(c.Request.Context(), organizationID, templateID)
	if err != nil {
		response.HandleError(c, "Failed to get mail template", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailTemplateResponse(template))
}

func (h *Handler) CreateTemplate(c *gin.Context) {
	organization, ok := organizationScope(c)
	if !ok {
		return
	}
	var req MailTemplateRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	template, err := h.service.CreateTemplate(
		c.Request.Context(), organization.OrganizationID, organization.UserID, req.input())
	if err != nil {
		response.HandleError(c, "Failed to create mail template", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailTemplateResponse(template))
}

func (h *Handler) UpdateTemplate(c *gin.Context) {
	organizationID, templateID, ok := templateScope(c)
	if !ok {
		return
	}
	var req MailTemplateRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	template, err := h.service.UpdateTemplate(c.Request.Context(), organizationID, templateID, req.input())
	if err != nil {
		response.HandleError(c, "Failed to update mail template", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailTemplateResponse(template))
}

func (h *Handler) DeleteTemplate(c *gin.Context) {
	organizationID, templateID, ok := templateScope(c)
	if !ok {
		return
	}
	if err := h.service.DeleteTemplate(c.Request.Context(), organizationID, templateID); err != nil {
		response.HandleError(c, "Failed to delete mail template", err)
		return
	}
	privateResponse(c)
	response.NoContent(c)
}

func (h *Handler) PreviewTemplate(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	var req MailTemplatePreviewRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	input := PreviewInput{TemplateID: req.TemplateID, Variables: req.Variables}
	if req.Source != nil {
		source := req.Source.input()
		input.Source = &source
	}
	preview, err := h.service.PreviewTemplate(c.Request.Context(), organizationID, input)
	if err != nil {
		response.HandleError(c, "Failed to preview mail template", err)
		return
	}
	privateResponse(c)
	response.Success(c, MailTemplatePreviewResponse{
		Subject:   preview.Subject,
		HTMLBody:  preview.HTML,
		TextBody:  preview.Text,
		Variables: preview.Variables,
		Missing:   preview.Missing,
	})
}

func (h *Handler) SendTemplateTest(c *gin.Context) {
	viewer, templateID, ok := viewerScope(c, "template_id")
	if !ok {
		return
	}
	var req MailTemplateTestRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, err := h.service.SendTemplateTest(
		c.Request.Context(), viewer, templateID, req.MailboxID, req.To, req.Variables)
	respondSent(c, message, err == nil, err)
}

func templateScope(c *gin.Context) (organizationID, templateID uint, ok bool) {
	organizationID, ok = currentOrganizationID(c)
	if !ok {
		return 0, 0, false
	}
	templateID, ok = httphandler.ParseID(c, "template_id")
	return organizationID, templateID, ok
}

// respondMissingVariables answers a template send that lacked values, naming each variable as a
// field error under template.variables.
func respondMissingVariables(c *gin.Context, err error) bool {
	var missing *MissingVariablesError
	if !errors.As(err, &missing) {
		return false
	}
	fields := make(map[string][]string, len(missing.Names))
	for _, name := range missing.Names {
		fields["template.variables."+name] = []string{"is required"}
	}
	c.JSON(http.StatusUnprocessableEntity, response.ValidationErrorResponse{
		Code:      http.StatusUnprocessableEntity,
		ErrorCode: domain.CodeMailTemplateVariablesMissing,
		Message:   "Template variables are missing",
		Errors:    fields,
		RequestID: c.GetString("request_id"),
	})
	return true
}
