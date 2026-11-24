package broadcast

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestValidVariableName(t *testing.T) {
	t.Parallel()

	valid := []string{"a", "first_name", "Order2026", "A_1_b", strings.Repeat("a", MaxTemplateVariableName)}
	for _, name := range valid {
		if !ValidVariableName(name) {
			t.Fatalf("expected %q to be valid", name)
		}
	}

	invalid := []string{"", "1name", "_name", "first-name", "first.name", "name space", strings.Repeat("a", MaxTemplateVariableName+1)}
	for _, name := range invalid {
		if ValidVariableName(name) {
			t.Fatalf("expected %q to be invalid", name)
		}
	}
}

func TestParseTemplate(t *testing.T) {
	t.Parallel()

	parts, ok := ParseTemplate("Hello {{ first_name | Friend }}, welcome to {{company}}!")
	if !ok {
		t.Fatalf("expected valid template")
	}
	if len(parts) != 5 {
		t.Fatalf("expected 5 parts, got %d: %+v", len(parts), parts)
	}
	if parts[0].Literal != "Hello " || parts[1].Variable != "first_name" || !parts[1].HasFallback || parts[1].Fallback != "Friend" {
		t.Fatalf("unexpected first placeholder: %+v", parts[:2])
	}
	if parts[3].Variable != "company" || parts[3].HasFallback {
		t.Fatalf("unexpected second placeholder: %+v", parts[3])
	}

	badSources := []string{
		"Unclosed {{name",
		"Nested {{a {{b}}}}",
		"Newline {{name\n}}",
		"Bad var {{123}}",
		"Empty {{   }}",
		"Overlong fallback {{name|" + strings.Repeat("x", MaxTemplateFallback+1) + "}}",
	}
	for _, src := range badSources {
		if _, ok := ParseTemplate(src); ok {
			t.Fatalf("expected ParseTemplate(%q) to fail", src)
		}
	}
}

func TestParseTemplateSourceRenderAndVariables(t *testing.T) {
	t.Parallel()

	parsed, ok := ParseTemplateSource(TemplateSource{
		Subject: "  Hi {{name|there}}, update for {{org}}  ",
		HTML:    "<p>Hello {{name|there}} from {{org}}</p><p>Code: {{code}}</p>",
		Text:    "Hello {{name|there}} from {{org}}. Code: {{code}}. Missing: {{extra}}",
	})
	if !ok {
		t.Fatalf("ParseTemplateSource failed")
	}

	wantVars := []string{"code", "extra", "name", "org"}
	if got := parsed.Variables(); !slices.Equal(got, wantVars) {
		t.Fatalf("Variables() = %v, want %v", got, wantVars)
	}

	rendered := parsed.Render(map[string]string{
		"name": "Alice\n<b>Smith</b>",
		"org":  "Acme & Co",
		"code": "42",
	})
	if rendered.Subject != "Hi Alice <b>Smith</b>, update for Acme & Co" {
		t.Fatalf("unexpected Subject: %q", rendered.Subject)
	}
	if rendered.HTML != "<p>Hello Alice\n&lt;b&gt;Smith&lt;/b&gt; from Acme &amp; Co</p><p>Code: 42</p>" {
		t.Fatalf("unexpected HTML: %q", rendered.HTML)
	}
	if rendered.Text != "Hello Alice\n<b>Smith</b> from Acme & Co. Code: 42. Missing: " {
		t.Fatalf("unexpected Text: %q", rendered.Text)
	}
	if !slices.Equal(rendered.Missing, []string{"extra"}) {
		t.Fatalf("unexpected Missing: %v", rendered.Missing)
	}

	// Fallback is used when a variable is empty or omitted.
	fallbackRendered := parsed.Render(map[string]string{"name": ""})
	if !strings.HasPrefix(fallbackRendered.Subject, "Hi there,") {
		t.Fatalf("expected fallback in subject, got %q", fallbackRendered.Subject)
	}
	if !slices.Equal(fallbackRendered.Missing, []string{"code", "extra", "org"}) {
		t.Fatalf("unexpected Missing with fallbacks: %v", fallbackRendered.Missing)
	}

	if _, ok := ParseTemplateSource(TemplateSource{Subject: "ok", HTML: "{{bad", Text: "ok"}); ok {
		t.Fatalf("expected ParseTemplateSource to reject invalid HTML")
	}
}

func TestCleanTemplateValues(t *testing.T) {
	t.Parallel()

	cleaned, ok := CleanTemplateValues(map[string]string{
		"name": "Alice",
		"note": "hello\x00world",
	})
	if !ok {
		t.Fatalf("expected valid values")
	}
	if cleaned["name"] != "Alice" || strings.ContainsRune(cleaned["note"], '\x00') {
		t.Fatalf("unexpected cleaned values: %+v", cleaned)
	}

	if _, ok := CleanTemplateValues(map[string]string{"1bad": "v"}); ok {
		t.Fatalf("expected invalid variable name to fail")
	}
	if _, ok := CleanTemplateValues(map[string]string{"ok": strings.Repeat("x", MaxTemplateVariableValue+1)}); ok {
		t.Fatalf("expected overlong variable value to fail")
	}

	tooMany := make(map[string]string, MaxTemplateVariables+1)
	for i := range MaxTemplateVariables + 1 {
		tooMany[fmt.Sprintf("v%d", i)] = "x"
	}
	if _, ok := CleanTemplateValues(tooMany); ok {
		t.Fatalf("expected more than MaxTemplateVariables to fail")
	}
}
