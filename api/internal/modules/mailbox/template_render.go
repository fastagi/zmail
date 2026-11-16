package mailbox

import (
	"html"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxTemplateVariableName  = 64
	maxTemplateFallback      = 500
	maxTemplateVariables     = 100
	maxTemplateVariableValue = 2_000
)

// templatePart is one piece of parsed template source: literal text, or a variable with an
// optional fallback.
type templatePart struct {
	literal     string
	variable    string
	fallback    string
	hasFallback bool
}

// parseTemplate splits source into literal text and `{{name}}` or `{{name|fallback}}`
// placeholders. Whitespace around the name and the fallback is ignored. Any `{{` that does not
// open a well-formed placeholder makes the source invalid, so a typo is caught when the template
// is saved instead of being sent to recipients.
func parseTemplate(source string) ([]templatePart, bool) {
	var parts []templatePart
	for {
		start := strings.Index(source, "{{")
		if start < 0 {
			if source != "" {
				parts = append(parts, templatePart{literal: source})
			}
			return parts, true
		}
		if start > 0 {
			parts = append(parts, templatePart{literal: source[:start]})
		}
		rest := source[start+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return nil, false
		}
		body := rest[:end]
		if strings.Contains(body, "{{") || strings.ContainsAny(body, "\r\n") {
			return nil, false
		}
		part := templatePart{}
		name := body
		if index := strings.IndexByte(body, '|'); index >= 0 {
			name = body[:index]
			part.fallback = strings.TrimSpace(body[index+1:])
			part.hasFallback = true
			if utf8.RuneCountInString(part.fallback) > maxTemplateFallback {
				return nil, false
			}
		}
		part.variable = strings.TrimSpace(name)
		if !validVariableName(part.variable) {
			return nil, false
		}
		parts = append(parts, part)
		source = rest[end+2:]
	}
}

// validVariableName accepts a letter followed by letters, digits, and underscores.
func validVariableName(name string) bool {
	if name == "" || len(name) > maxTemplateVariableName {
		return false
	}
	for index, char := range name {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z':
		case index > 0 && (char >= '0' && char <= '9' || char == '_'):
		default:
			return false
		}
	}
	return true
}

// templateEscaping says how a value is written into one part of a message.
type templateEscaping int

const (
	escapeNone templateEscaping = iota
	escapeHTML
	// escapeLine is for the subject: line breaks in a value become spaces.
	escapeLine
)

// renderTemplate fills the placeholders of already parsed source. A variable without a value
// uses its fallback; without either it is reported as missing and rendered as nothing.
func renderTemplate(parts []templatePart, values map[string]string, escaping templateEscaping, missing map[string]struct{}) string {
	var out strings.Builder
	for _, part := range parts {
		if part.variable == "" {
			out.WriteString(part.literal)
			continue
		}
		value, ok := values[part.variable]
		if !ok || value == "" {
			if !part.hasFallback {
				missing[part.variable] = struct{}{}
				continue
			}
			value = part.fallback
		}
		switch escaping {
		case escapeHTML:
			value = html.EscapeString(value)
		case escapeLine:
			value = strings.Join(strings.Fields(value), " ")
		case escapeNone:
		}
		out.WriteString(value)
	}
	return out.String()
}

// templateSource is the editable content of a template.
type templateSource struct {
	Subject string
	HTML    string
	Text    string
}

// parsedTemplate is template source that passed the syntax check.
type parsedTemplate struct {
	subject, html, text []templatePart
}

func parseTemplateSource(source templateSource) (*parsedTemplate, bool) {
	subject, ok := parseTemplate(source.Subject)
	if !ok {
		return nil, false
	}
	htmlParts, ok := parseTemplate(source.HTML)
	if !ok {
		return nil, false
	}
	text, ok := parseTemplate(source.Text)
	if !ok {
		return nil, false
	}
	return &parsedTemplate{subject: subject, html: htmlParts, text: text}, true
}

// variables lists every variable the template uses, sorted, each once.
func (p *parsedTemplate) variables() []string {
	seen := map[string]struct{}{}
	for _, parts := range [][]templatePart{p.subject, p.html, p.text} {
		for _, part := range parts {
			if part.variable != "" {
				seen[part.variable] = struct{}{}
			}
		}
	}
	return sortedKeys(seen)
}

// renderedTemplate is a template with its variables filled in.
type renderedTemplate struct {
	Subject string
	HTML    string
	Text    string
	// Missing lists variables that had neither a value nor a fallback, sorted.
	Missing []string
}

func (p *parsedTemplate) render(values map[string]string) renderedTemplate {
	missing := map[string]struct{}{}
	rendered := renderedTemplate{
		Subject: strings.TrimSpace(renderTemplate(p.subject, values, escapeLine, missing)),
		HTML:    renderTemplate(p.html, values, escapeHTML, missing),
		Text:    renderTemplate(p.text, values, escapeNone, missing),
	}
	rendered.Missing = sortedKeys(missing)
	return rendered
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// cleanTemplateValues checks variable values supplied by a caller: at most 100 named values,
// each a valid name with a bounded value. Values are cleaned like any message text.
func cleanTemplateValues(values map[string]string) (map[string]string, bool) {
	if len(values) > maxTemplateVariables {
		return nil, false
	}
	cleaned := make(map[string]string, len(values))
	for name, value := range values {
		if !validVariableName(name) || utf8.RuneCountInString(value) > maxTemplateVariableValue {
			return nil, false
		}
		cleaned[name] = cleanText(value)
	}
	return cleaned, true
}
