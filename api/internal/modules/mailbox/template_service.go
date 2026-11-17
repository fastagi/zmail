package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	maxTemplateNameLength = 100
	// maxTemplateDesignBytes bounds the visual editor's design document.
	maxTemplateDesignBytes = 256 * 1024
)

// ErrMailTemplateVariablesMissing means a template was sent without values for variables that
// have no fallback.
var ErrMailTemplateVariablesMissing = errors.New("mail template variables are missing")

// MissingVariablesError names the variables a template send lacked.
type MissingVariablesError struct {
	Names []string
}

func (e *MissingVariablesError) Error() string {
	return "mail template variables are missing: " + strings.Join(e.Names, ", ")
}

func (e *MissingVariablesError) Unwrap() error { return ErrMailTemplateVariablesMissing }

// TemplateInput is the editable content of a template.
type TemplateInput struct {
	Name            string
	Subject         string
	HTML            string
	Text            string
	SampleVariables map[string]string
	// Design is the visual editor's document; empty or JSON null when the template is HTML.
	Design json.RawMessage
}

// TemplateSendInput sends a stored template with values for its variables.
type TemplateSendInput struct {
	ID        uint
	Variables map[string]string
}

// PreviewInput renders a stored template (TemplateID) or unsaved source (Source) with values.
// Sample values of the source fill variables the caller does not give.
type PreviewInput struct {
	TemplateID uint
	Source     *TemplateInput
	Variables  map[string]string
}

// TemplatePreview is a rendered template. Nothing is sent.
type TemplatePreview struct {
	Subject   string
	HTML      string
	Text      string
	Variables []string
	Missing   []string
}

// TemplateVariables lists the variables a stored template uses.
func TemplateVariables(template *domain.MailTemplate) []string {
	parsed, ok := parseTemplateSource(templateSource{Subject: template.Subject, HTML: template.HTMLBody, Text: template.TextBody})
	if !ok {
		return []string{}
	}
	return parsed.variables()
}

func (s *service) ListTemplates(ctx context.Context, organizationID uint) ([]*domain.MailTemplate, error) {
	return s.templates.ListTemplates(ctx, organizationID)
}

func (s *service) GetTemplate(ctx context.Context, organizationID, id uint) (*domain.MailTemplate, error) {
	return s.templates.FindTemplate(ctx, organizationID, id)
}

func (s *service) CreateTemplate(ctx context.Context, organizationID, createdBy uint, input TemplateInput) (*domain.MailTemplate, error) {
	template, _, err := buildTemplate(input)
	if err != nil {
		return nil, err
	}
	template.OrganizationID, template.CreatedBy = organizationID, createdBy
	if err := s.templates.CreateTemplate(ctx, template, domain.MaxMailTemplatesPerOrganization); err != nil {
		return nil, err
	}
	return template, nil
}

func (s *service) UpdateTemplate(ctx context.Context, organizationID, id uint, input TemplateInput) (*domain.MailTemplate, error) {
	template, _, err := buildTemplate(input)
	if err != nil {
		return nil, err
	}
	template.ID, template.OrganizationID = id, organizationID
	if err := s.templates.UpdateTemplate(ctx, template); err != nil {
		return nil, err
	}
	return template, nil
}

func (s *service) DeleteTemplate(ctx context.Context, organizationID, id uint) error {
	return s.templates.DeleteTemplate(ctx, organizationID, id)
}

func (s *service) PreviewTemplate(ctx context.Context, organizationID uint, input PreviewInput) (*TemplatePreview, error) {
	var (
		template *domain.MailTemplate
		parsed   *parsedTemplate
		err      error
	)
	switch {
	case input.Source != nil && input.TemplateID == 0:
		template, parsed, err = buildTemplate(*input.Source)
	case input.Source == nil && input.TemplateID != 0:
		template, err = s.templates.FindTemplate(ctx, organizationID, input.TemplateID)
		if err == nil {
			parsed, err = parseStoredTemplate(template)
		}
	default:
		err = fmt.Errorf("%w: give either a template or its source", ErrMailTemplateInvalid)
	}
	if err != nil {
		return nil, err
	}
	values, ok := cleanTemplateValues(input.Variables)
	if !ok {
		return nil, fmt.Errorf("%w: variables", ErrMailTemplateInvalid)
	}
	rendered := parsed.render(mergeValues(template.SampleVariables, values))
	return &TemplatePreview{
		Subject:   rendered.Subject,
		HTML:      rendered.HTML,
		Text:      rendered.Text,
		Variables: parsed.variables(),
		Missing:   rendered.Missing,
	}, nil
}

// SendTemplateTest renders a template with its sample values, overridden by the given ones, and
// sends it from one of the organization's mailboxes like any other message.
func (s *service) SendTemplateTest(
	ctx context.Context,
	organizationID, templateID, mailboxID uint,
	to []string,
	values map[string]string,
) (*domain.MailMessage, error) {
	template, err := s.templates.FindTemplate(ctx, organizationID, templateID)
	if err != nil {
		return nil, err
	}
	mailbox, err := s.repo.FindMailbox(ctx, organizationID, mailboxID)
	if err != nil {
		return nil, err
	}
	cleaned, ok := cleanTemplateValues(values)
	if !ok {
		return nil, fmt.Errorf("%w: variables", ErrMailTemplateInvalid)
	}
	message, _, err := s.send(ctx, mailbox, SendInput{
		To:       to,
		Template: &TemplateSendInput{ID: template.ID, Variables: mergeValues(template.SampleVariables, cleaned)},
	})
	return message, err
}

// applyTemplate replaces a send's template reference with the rendered subject and bodies.
// A real send uses only the values it was given: sample values are for previews and tests.
func (s *service) applyTemplate(ctx context.Context, organizationID uint, input SendInput) (SendInput, uint, error) {
	if input.Subject != "" || input.Text != "" || input.HTML != "" {
		return input, 0, fmt.Errorf("%w: give either a template or a subject and body", ErrMailMessageInvalid)
	}
	template, err := s.templates.FindTemplate(ctx, organizationID, input.Template.ID)
	if err != nil {
		return input, 0, err
	}
	parsed, err := parseStoredTemplate(template)
	if err != nil {
		return input, 0, err
	}
	values, ok := cleanTemplateValues(input.Template.Variables)
	if !ok {
		return input, 0, fmt.Errorf("%w: template variables", ErrMailMessageInvalid)
	}
	rendered := parsed.render(values)
	if len(rendered.Missing) > 0 {
		return input, 0, &MissingVariablesError{Names: rendered.Missing}
	}
	input.Subject, input.HTML, input.Text = rendered.Subject, rendered.HTML, rendered.Text
	input.Template = nil
	return input, template.ID, nil
}

// buildTemplate validates editable content and parses it.
func buildTemplate(input TemplateInput) (*domain.MailTemplate, *parsedTemplate, error) {
	name := strings.Join(strings.Fields(input.Name), " ")
	if name == "" || utf8.RuneCountInString(name) > maxTemplateNameLength || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, nil, fmt.Errorf("%w: name", ErrMailTemplateInvalid)
	}
	subject := strings.TrimSpace(input.Subject)
	if subject == "" || len(subject) > domain.MaxMailSubjectLength || strings.ContainsAny(subject, "\r\n\x00") {
		return nil, nil, fmt.Errorf("%w: subject", ErrMailTemplateInvalid)
	}
	html, text := cleanText(input.HTML), cleanText(input.Text)
	if strings.TrimSpace(html) == "" && strings.TrimSpace(text) == "" {
		return nil, nil, fmt.Errorf("%w: body", ErrMailTemplateInvalid)
	}
	if len(html) > domain.MaxMailBodyBytes || len(text) > domain.MaxMailBodyBytes {
		return nil, nil, fmt.Errorf("%w: body", ErrMailTemplateInvalid)
	}
	samples, ok := cleanTemplateValues(input.SampleVariables)
	if !ok {
		return nil, nil, fmt.Errorf("%w: sample_variables", ErrMailTemplateInvalid)
	}
	design, ok := cleanDesign(input.Design)
	if !ok {
		return nil, nil, fmt.Errorf("%w: design", ErrMailTemplateInvalid)
	}
	template := &domain.MailTemplate{
		Name:            name,
		Subject:         cleanText(subject),
		HTMLBody:        html,
		TextBody:        text,
		SampleVariables: samples,
		Design:          design,
	}
	parsed, err := parseStoredTemplate(template)
	if err != nil {
		return nil, nil, err
	}
	return template, parsed, nil
}

func parseStoredTemplate(template *domain.MailTemplate) (*parsedTemplate, error) {
	parsed, ok := parseTemplateSource(templateSource{Subject: template.Subject, HTML: template.HTMLBody, Text: template.TextBody})
	if !ok {
		return nil, fmt.Errorf("%w: syntax", ErrMailTemplateInvalid)
	}
	return parsed, nil
}

// mergeValues returns base overlaid with override.
func mergeValues(base, override map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(override))
	for name, value := range base {
		merged[name] = value
	}
	for name, value := range override {
		merged[name] = value
	}
	return merged
}

// cleanDesign checks the visual editor's document without interpreting it: a JSON object of at
// most maxTemplateDesignBytes with a positive integer version. It returns the compact document,
// or "" when there is none.
func cleanDesign(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", true
	}
	if len(trimmed) > maxTemplateDesignBytes || trimmed[0] != '{' {
		return "", false
	}
	var head struct {
		Version json.Number `json:"version"`
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&head); err != nil {
		return "", false
	}
	version, err := head.Version.Int64()
	if err != nil || version < 1 {
		return "", false
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return "", false
	}
	return compact.String(), true
}
