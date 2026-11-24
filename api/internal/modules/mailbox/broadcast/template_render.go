package broadcast

import (
	"html"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MaxTemplateVariableName  = 64
	MaxTemplateFallback      = 500
	MaxTemplateVariables     = 100
	MaxTemplateVariableValue = 2_000
)

// TemplatePart is one piece of parsed template source: literal text, or a variable with an
// optional fallback.
type TemplatePart struct {
	Literal     string
	Variable    string
	Fallback    string
	HasFallback bool
}

// ParseTemplate splits source into literal text and `{{name}}` or `{{name|fallback}}`
// placeholders. Whitespace around the name and the fallback is ignored. Any `{{` that does not
// open a well-formed placeholder makes the source invalid, so a typo is caught when the template
// is saved instead of being sent to recipients.
func ParseTemplate(source string) ([]TemplatePart, bool) {
	var parts []TemplatePart
	for {
		start := strings.Index(source, "{{")
		if start < 0 {
			if source != "" {
				parts = append(parts, TemplatePart{Literal: source})
			}
			return parts, true
		}
		if start > 0 {
			parts = append(parts, TemplatePart{Literal: source[:start]})
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
		part := TemplatePart{}
		name := body
		if index := strings.IndexByte(body, '|'); index >= 0 {
			name = body[:index]
			part.Fallback = strings.TrimSpace(body[index+1:])
			part.HasFallback = true
			if utf8.RuneCountInString(part.Fallback) > MaxTemplateFallback {
				return nil, false
			}
		}
		part.Variable = strings.TrimSpace(name)
		if !ValidVariableName(part.Variable) {
			return nil, false
		}
		parts = append(parts, part)
		source = rest[end+2:]
	}
}

// ValidVariableName accepts a letter followed by letters, digits, and underscores.
func ValidVariableName(name string) bool {
	if name == "" || len(name) > MaxTemplateVariableName {
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

// TemplateEscaping says how a value is written into one part of a message.
type TemplateEscaping int

const (
	EscapeNone TemplateEscaping = iota
	EscapeHTML
	// EscapeLine is for the subject: line breaks in a value become spaces.
	EscapeLine
)

// RenderTemplate fills the placeholders of already parsed source. A variable without a value
// uses its fallback; without either it is reported as missing and rendered as nothing.
func RenderTemplate(parts []TemplatePart, values map[string]string, escaping TemplateEscaping, missing map[string]struct{}) string {
	var out strings.Builder
	for _, part := range parts {
		if part.Variable == "" {
			out.WriteString(part.Literal)
			continue
		}
		value, ok := values[part.Variable]
		if !ok || value == "" {
			if !part.HasFallback {
				missing[part.Variable] = struct{}{}
				continue
			}
			value = part.Fallback
		}
		switch escaping {
		case EscapeHTML:
			value = html.EscapeString(value)
		case EscapeLine:
			value = strings.Join(strings.Fields(value), " ")
		case EscapeNone:
		}
		out.WriteString(value)
	}
	return out.String()
}

// TemplateSource is the editable content of a template.
type TemplateSource struct {
	Subject string
	HTML    string
	Text    string
}

// ParsedTemplate is template source that passed the syntax check.
type ParsedTemplate struct {
	Subject []TemplatePart
	HTML    []TemplatePart
	Text    []TemplatePart
}

// ParseTemplateSource parses subject, HTML, and text parts of a template.
func ParseTemplateSource(source TemplateSource) (*ParsedTemplate, bool) {
	subject, ok := ParseTemplate(source.Subject)
	if !ok {
		return nil, false
	}
	htmlParts, ok := ParseTemplate(source.HTML)
	if !ok {
		return nil, false
	}
	text, ok := ParseTemplate(source.Text)
	if !ok {
		return nil, false
	}
	return &ParsedTemplate{Subject: subject, HTML: htmlParts, Text: text}, true
}

// Variables lists every variable the template uses, sorted, each once.
func (p *ParsedTemplate) Variables() []string {
	seen := map[string]struct{}{}
	for _, parts := range [][]TemplatePart{p.Subject, p.HTML, p.Text} {
		for _, part := range parts {
			if part.Variable != "" {
				seen[part.Variable] = struct{}{}
			}
		}
	}
	return SortedKeys(seen)
}

// RenderedTemplate is a template with its variables filled in.
type RenderedTemplate struct {
	Subject string
	HTML    string
	Text    string
	// Missing lists variables that had neither a value nor a fallback, sorted.
	Missing []string
}

// Render fills all parts of a parsed template with the given variable values.
func (p *ParsedTemplate) Render(values map[string]string) RenderedTemplate {
	missing := map[string]struct{}{}
	rendered := RenderedTemplate{
		Subject: strings.TrimSpace(RenderTemplate(p.Subject, values, EscapeLine, missing)),
		HTML:    RenderTemplate(p.HTML, values, EscapeHTML, missing),
		Text:    RenderTemplate(p.Text, values, EscapeNone, missing),
	}
	rendered.Missing = SortedKeys(missing)
	return rendered
}

// SortedKeys returns the keys of a string set in ascending lexicographical order.
func SortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// CleanTemplateValues checks variable values supplied by a caller: at most 100 named values,
// each a valid name with a bounded value. Values are cleaned like any message text.
func CleanTemplateValues(values map[string]string) (map[string]string, bool) {
	if len(values) > MaxTemplateVariables {
		return nil, false
	}
	cleaned := make(map[string]string, len(values))
	for name, value := range values {
		if !ValidVariableName(name) || utf8.RuneCountInString(value) > MaxTemplateVariableValue {
			return nil, false
		}
		cleaned[name] = cleanText(value)
	}
	return cleaned, true
}
