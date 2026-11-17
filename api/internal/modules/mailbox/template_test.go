package mailbox

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestParseTemplateAcceptsPlaceholdersAndRejectsTypos(t *testing.T) {
	for _, source := range []string{
		"", "plain", "Hi {{name}}", "Hi {{ name }}!", "{{name|there}}", "{{ name | dear customer }}",
		"{{first_name}} {{last_name}}", "a {single} brace and }} alone", "{{name|}}",
	} {
		if _, ok := parseTemplate(source); !ok {
			t.Errorf("parseTemplate(%q) rejected", source)
		}
	}
	for _, source := range []string{
		"{{", "Hi {{name", "{{}}", "{{ }}", "{{1st}}", "{{first-name}}", "{{na me}}", "{{a{{b}}",
		"{{name\n}}", "{{_hidden}}", "{{" + strings.Repeat("a", 65) + "}}",
		"{{name|" + strings.Repeat("x", 501) + "}}",
	} {
		if _, ok := parseTemplate(source); ok {
			t.Errorf("parseTemplate(%q) accepted", source)
		}
	}
}

func TestRenderTemplateFillsEscapesAndReportsMissing(t *testing.T) {
	parsed, ok := parseTemplateSource(templateSource{
		Subject: "Hi {{ name }}, about {{topic}}",
		HTML:    "<p>{{name|there}} &mdash; {{code}}</p>",
		Text:    "Hello {{name|there}}, code {{code}}",
	})
	if !ok {
		t.Fatal("source rejected")
	}
	if got := parsed.variables(); !slices.Equal(got, []string{"code", "name", "topic"}) {
		t.Fatalf("variables = %v", got)
	}

	rendered := parsed.render(map[string]string{
		"name": `Ann <script>"x"</script>`, "topic": "line one\nline two", "code": "A&B",
	})
	if rendered.Subject != `Hi Ann <script>"x"</script>, about line one line two` {
		t.Fatalf("subject = %q", rendered.Subject)
	}
	if rendered.HTML != "<p>Ann &lt;script&gt;&#34;x&#34;&lt;/script&gt; &mdash; A&amp;B</p>" {
		t.Fatalf("html = %q", rendered.HTML)
	}
	if rendered.Text != `Hello Ann <script>"x"</script>, code A&B` || len(rendered.Missing) != 0 {
		t.Fatalf("text = %q, missing %v", rendered.Text, rendered.Missing)
	}

	// An empty value counts as absent: the fallback applies, and without one it is missing. The
	// subject uses name without a fallback, so name is missing there.
	fallback := parsed.render(map[string]string{"name": ""})
	if fallback.HTML != "<p>there &mdash; </p>" || !slices.Equal(fallback.Missing, []string{"code", "name", "topic"}) {
		t.Fatalf("fallback render = %+v", fallback)
	}
}

func templateInput() TemplateInput {
	return TemplateInput{
		Name:            "  Welcome   email ",
		Subject:         "Welcome, {{first_name|friend}}",
		HTML:            "<p>Hi {{first_name|friend}}, your code is {{code}}.</p>",
		Text:            "Hi {{first_name|friend}}, your code is {{code}}.",
		SampleVariables: map[string]string{"first_name": "Ann", "code": "1234"},
	}
}

func TestTemplatesAreKeptPerOrganization(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()

	template, err := fixture.service.CreateTemplate(ctx, 1, testCreator, templateInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if template.Name != "Welcome email" || template.OrganizationID != 1 || template.CreatedBy != testCreator {
		t.Fatalf("template = %+v", template)
	}
	if got := TemplateVariables(template); !slices.Equal(got, []string{"code", "first_name"}) {
		t.Fatalf("variables = %v", got)
	}
	if _, err = fixture.service.CreateTemplate(ctx, 1, testCreator, templateInput()); !errors.Is(err, ErrMailTemplateNameTaken) {
		t.Fatalf("duplicate name error = %v", err)
	}
	if _, err = fixture.service.CreateTemplate(ctx, 2, testCreator, templateInput()); err != nil {
		t.Fatalf("same name in another organization: %v", err)
	}
	if _, err = fixture.service.GetTemplate(ctx, 2, template.ID); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("another organization's template error = %v", err)
	}

	changed := templateInput()
	changed.Subject = "Changed {{first_name}}"
	updated, err := fixture.service.UpdateTemplate(ctx, 1, template.ID, changed)
	if err != nil || updated.Subject != "Changed {{first_name}}" {
		t.Fatalf("UpdateTemplate = %+v, %v", updated, err)
	}
	if _, err = fixture.service.UpdateTemplate(ctx, 2, template.ID, changed); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("update from another organization error = %v", err)
	}
	if err = fixture.service.DeleteTemplate(ctx, 2, template.ID); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("delete from another organization error = %v", err)
	}
	if err = fixture.service.DeleteTemplate(ctx, 1, template.ID); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
}

func TestTemplateValidation(t *testing.T) {
	fixture := newServiceFixture(t)
	for name, change := range map[string]func(*TemplateInput){
		"no name":          func(i *TemplateInput) { i.Name = "  " },
		"long name":        func(i *TemplateInput) { i.Name = strings.Repeat("n", 101) },
		"no subject":       func(i *TemplateInput) { i.Subject = " " },
		"two-line subject": func(i *TemplateInput) { i.Subject = "a\nb" },
		"no body":          func(i *TemplateInput) { i.HTML, i.Text = " ", "" },
		"syntax":           func(i *TemplateInput) { i.HTML = "<p>{{first name}}</p>" },
		"unclosed":         func(i *TemplateInput) { i.Text = "Hi {{first_name" },
		"sample name":      func(i *TemplateInput) { i.SampleVariables = map[string]string{"bad-name": "x"} },
	} {
		input := templateInput()
		change(&input)
		if _, err := fixture.service.CreateTemplate(context.Background(), 1, testCreator, input); !errors.Is(err, ErrMailTemplateInvalid) {
			t.Errorf("%s: error = %v; want invalid", name, err)
		}
	}
}

func TestPreviewUsesSamplesAndReportsMissing(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	template, err := fixture.service.CreateTemplate(ctx, 1, testCreator, templateInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}

	stored, err := fixture.service.PreviewTemplate(ctx, 1, PreviewInput{TemplateID: template.ID, Variables: map[string]string{"code": "9"}})
	if err != nil || stored.Subject != "Welcome, Ann" || stored.Text != "Hi Ann, your code is 9." || len(stored.Missing) != 0 {
		t.Fatalf("stored preview = %+v, %v", stored, err)
	}

	source := templateInput()
	source.SampleVariables = nil
	unsaved, err := fixture.service.PreviewTemplate(ctx, 1, PreviewInput{Source: &source})
	if err != nil || unsaved.Subject != "Welcome, friend" || !slices.Equal(unsaved.Missing, []string{"code"}) {
		t.Fatalf("unsaved preview = %+v, %v", unsaved, err)
	}

	for _, input := range []PreviewInput{{}, {TemplateID: template.ID, Source: &source}} {
		if _, err = fixture.service.PreviewTemplate(ctx, 1, input); !errors.Is(err, ErrMailTemplateInvalid) {
			t.Errorf("preview %+v error = %v", input, err)
		}
	}
	if _, err = fixture.service.PreviewTemplate(ctx, 2, PreviewInput{TemplateID: template.ID}); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("another organization's preview error = %v", err)
	}
	if len(fixture.transport.sent) != 0 {
		t.Fatal("a preview sent mail")
	}
}

func TestSendByTemplateRendersAndRecordsTheTemplate(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	template, err := fixture.service.CreateTemplate(ctx, 1, testCreator, templateInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}

	message, _, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, SendInput{
		To:       []string{"bob@example.com"},
		Template: &TemplateSendInput{ID: template.ID, Variables: map[string]string{"code": "<42>"}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	// A real send never uses sample values: first_name falls back, not "Ann".
	if message.Subject != "Welcome, friend" || message.TemplateID != template.ID ||
		message.HTMLBody != "<p>Hi friend, your code is &lt;42&gt;.</p>" || message.TextBody != "Hi friend, your code is <42>." {
		t.Fatalf("message = %+v", message)
	}
	if sent := fixture.transport.sent[0]; sent.Subject != message.Subject || sent.HTMLBody != message.HTMLBody {
		t.Fatalf("provider got %+v", sent)
	}

	var missing *MissingVariablesError
	_, _, err = fixture.service.SendMessage(ctx, 1, mailbox.ID, SendInput{To: []string{"bob@example.com"}, Template: &TemplateSendInput{ID: template.ID}})
	if !errors.As(err, &missing) || !slices.Equal(missing.Names, []string{"code"}) || !errors.Is(err, ErrMailTemplateVariablesMissing) {
		t.Fatalf("missing variables error = %v", err)
	}
	_, _, err = fixture.service.SendMessage(ctx, 1, mailbox.ID, SendInput{
		To: []string{"bob@example.com"}, Subject: "both", Template: &TemplateSendInput{ID: template.ID},
	})
	if !errors.Is(err, ErrMailMessageInvalid) {
		t.Fatalf("template with a subject error = %v", err)
	}
	other := fixture.mailbox(t, 2, "other")
	_, _, err = fixture.service.SendMessage(ctx, 2, other.ID, SendInput{
		To: []string{"bob@example.com"}, Template: &TemplateSendInput{ID: template.ID, Variables: map[string]string{"code": "1"}},
	})
	if !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("another organization's template error = %v", err)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("provider calls = %d; want only the first send", len(fixture.transport.sent))
	}
}

func TestTemplateTestSendUsesSampleValues(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	template, err := fixture.service.CreateTemplate(ctx, 1, testCreator, templateInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	message, err := fixture.service.SendTemplateTest(ctx, 1, template.ID, mailbox.ID, []string{"qa@example.com"}, map[string]string{"code": "77"})
	if err != nil || message.Subject != "Welcome, Ann" || message.TextBody != "Hi Ann, your code is 77." {
		t.Fatalf("SendTemplateTest = %+v, %v", message, err)
	}
	stranger := fixture.mailbox(t, 2, "stranger")
	if _, err = fixture.service.SendTemplateTest(ctx, 1, template.ID, stranger.ID, []string{"qa@example.com"}, nil); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("test from another organization's mailbox error = %v", err)
	}
}

func TestSendRouteNamesMissingTemplateVariables(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if _, err := fixture.service.CreateMailbox(context.Background(), 7, testCreator, "hello", "", ""); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	template, err := fixture.service.CreateTemplate(context.Background(), 7, testCreator, templateInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	recorder, body := fixture.do(http.MethodPost, "/mailboxes/1/messages",
		`{"to":["bob@example.com"],"template":{"id":`+itoa(template.ID)+`}}`)
	if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailTemplateVariablesMissing {
		t.Fatalf("send = %d %v", recorder.Code, body)
	}
	fields, _ := body["errors"].(map[string]any)
	if _, ok := fields["template.variables.code"]; !ok || len(fields) != 1 {
		t.Fatalf("field errors = %v", fields)
	}

	recorder, body = fixture.do(http.MethodPost, "/mailboxes/1/messages",
		`{"to":["bob@example.com"],"template":{"id":`+itoa(template.ID)+`,"variables":{"code":"5"}}}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusCreated || data["subject"] != "Welcome, friend" || data["template_id"] != float64(template.ID) {
		t.Fatalf("send = %d %v", recorder.Code, body)
	}
}

func itoa(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}
