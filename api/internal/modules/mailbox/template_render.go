package mailbox

import "github.com/fastagi/zmail/api/internal/modules/mailbox/broadcast"

const (
	maxTemplateVariableName  = broadcast.MaxTemplateVariableName
	maxTemplateFallback      = broadcast.MaxTemplateFallback
	maxTemplateVariables     = broadcast.MaxTemplateVariables
	maxTemplateVariableValue = broadcast.MaxTemplateVariableValue
)

type templatePart struct {
	literal     string
	variable    string
	fallback    string
	hasFallback bool
}

type (
	templateEscaping = broadcast.TemplateEscaping
	templateSource   = broadcast.TemplateSource
	renderedTemplate = broadcast.RenderedTemplate
)

const (
	escapeNone = broadcast.EscapeNone
	escapeHTML = broadcast.EscapeHTML
	escapeLine = broadcast.EscapeLine
)

type parsedTemplate struct {
	subject, html, text []templatePart
	inner               *broadcast.ParsedTemplate
}

func fromBroadcastParts(parts []broadcast.TemplatePart) []templatePart {
	if parts == nil {
		return nil
	}
	out := make([]templatePart, len(parts))
	for i, part := range parts {
		out[i] = templatePart{
			literal:     part.Literal,
			variable:    part.Variable,
			fallback:    part.Fallback,
			hasFallback: part.HasFallback,
		}
	}
	return out
}

func toBroadcastParts(parts []templatePart) []broadcast.TemplatePart {
	if parts == nil {
		return nil
	}
	out := make([]broadcast.TemplatePart, len(parts))
	for i, part := range parts {
		out[i] = broadcast.TemplatePart{
			Literal:     part.literal,
			Variable:    part.variable,
			Fallback:    part.fallback,
			HasFallback: part.hasFallback,
		}
	}
	return out
}

func parseTemplate(source string) ([]templatePart, bool) {
	parts, ok := broadcast.ParseTemplate(source)
	if !ok {
		return nil, false
	}
	return fromBroadcastParts(parts), true
}

func validVariableName(name string) bool {
	return broadcast.ValidVariableName(name)
}

func renderTemplate(parts []templatePart, values map[string]string, escaping templateEscaping, missing map[string]struct{}) string {
	return broadcast.RenderTemplate(toBroadcastParts(parts), values, escaping, missing)
}

func parseTemplateSource(source templateSource) (*parsedTemplate, bool) {
	parsed, ok := broadcast.ParseTemplateSource(source)
	if !ok {
		return nil, false
	}
	return &parsedTemplate{
		subject: fromBroadcastParts(parsed.Subject),
		html:    fromBroadcastParts(parsed.HTML),
		text:    fromBroadcastParts(parsed.Text),
		inner:   parsed,
	}, true
}

func (p *parsedTemplate) variables() []string {
	return p.inner.Variables()
}

func (p *parsedTemplate) render(values map[string]string) renderedTemplate {
	return p.inner.Render(values)
}

func sortedKeys(set map[string]struct{}) []string {
	return broadcast.SortedKeys(set)
}

func cleanTemplateValues(values map[string]string) (map[string]string, bool) {
	return broadcast.CleanTemplateValues(values)
}
