package mailbox

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// transactionalInput is a valid transactional template sending from the log provider's instance
// domain, which the service fixture serves as mail.example.
func transactionalInput() TemplateInput {
	return TemplateInput{
		Kind:        domain.MailTemplateTransactional,
		Name:        "Signup code",
		Key:         "signup-code",
		FromAddress: " No-Reply@Mail.Example ",
		FromName:    "Acme",
		Subject:     "Your code for {{product|Acme}}",
		HTML:        "<p>Code {{code}} for {{product|Acme}}</p>",
		Text:        "Code {{code}}",
		VariableDefinitions: []domain.TransactionalVariable{
			{Name: "code", Secret: true, Required: true},
			{Name: "product"},
		},
		SampleVariables: map[string]string{"code": "123456"},
	}
}

// transactionalFixture serves the instance domain with the log provider, so transactional
// templates may send from it.
func transactionalFixture(t *testing.T) *serviceFixture {
	t.Helper()
	fixture := newServiceFixture(t)
	fixture.service.settings.Provider = config.MailboxProviderLog
	return fixture
}

func viewerAs(organizationID uint, role domain.OrganizationRole) domain.OrganizationContext {
	return viewerOf(organizationID, testCreator, role)
}

func TestTransactionalTemplateIsStoredWithItsFields(t *testing.T) {
	fixture := transactionalFixture(t)
	template, err := fixture.service.CreateTemplate(context.Background(), as(1), transactionalInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if template.Kind != domain.MailTemplateTransactional || template.Key != "signup-code" ||
		template.FromAddress != "no-reply@mail.example" || template.FromName != "Acme" || len(template.Variables) != 2 {
		t.Fatalf("template = %+v", template)
	}
	response := toMailTemplateResponse(template)
	if response.Kind != "transactional" || response.Key == nil || *response.Key != "signup-code" ||
		response.From == nil || response.From.Address != "no-reply@mail.example" || len(response.VariableDefinitions) != 2 {
		t.Fatalf("response = %+v", response)
	}

	standard, err := fixture.service.CreateTemplate(context.Background(), as(1), templateInput())
	if err != nil {
		t.Fatalf("CreateTemplate standard: %v", err)
	}
	summary := toMailTemplateResponse(standard)
	if standard.Kind != domain.MailTemplateStandard || summary.Key != nil || summary.From != nil ||
		summary.VariableDefinitions == nil || len(summary.VariableDefinitions) != 0 {
		t.Fatalf("standard response = %+v", summary)
	}
}

func TestTransactionalTemplateRequiresDeclaredPlaceholders(t *testing.T) {
	fixture := transactionalFixture(t)
	undeclared := transactionalInput()
	undeclared.Text = "Code {{code}}, ref {{ref}}"
	if _, err := fixture.service.CreateTemplate(context.Background(), as(1), undeclared); !errors.Is(err, ErrMailTemplateInvalid) {
		t.Fatalf("undeclared placeholder error = %v", err)
	}
	unused := transactionalInput()
	unused.VariableDefinitions = append(unused.VariableDefinitions, domain.TransactionalVariable{Name: "ref", Required: true})
	if _, err := fixture.service.CreateTemplate(context.Background(), as(1), unused); !errors.Is(err, ErrMailTemplateInvalid) {
		t.Fatalf("unused declaration error = %v", err)
	}
	for name, change := range map[string]func(*TemplateInput){
		"no key":       func(i *TemplateInput) { i.Key = "" },
		"bad key":      func(i *TemplateInput) { i.Key = "Signup_Code" },
		"long key":     func(i *TemplateInput) { i.Key = "a" + strings.Repeat("b", 64) },
		"no sender":    func(i *TemplateInput) { i.FromAddress = "" },
		"sender name":  func(i *TemplateInput) { i.FromAddress = "Acme <no-reply@mail.example>" },
		"from name":    func(i *TemplateInput) { i.FromName = `Eve "x" <eve@evil.test>` },
		"bad var name": func(i *TemplateInput) { i.VariableDefinitions[0].Name = "1code" },
		"duplicate": func(i *TemplateInput) {
			i.VariableDefinitions[1] = domain.TransactionalVariable{Name: "code", Required: true}
		},
		"undeclared all": func(i *TemplateInput) { i.VariableDefinitions = nil },
	} {
		input := transactionalInput()
		input.VariableDefinitions = append([]domain.TransactionalVariable(nil), input.VariableDefinitions...)
		change(&input)
		if _, err := fixture.service.CreateTemplate(context.Background(), as(1), input); !errors.Is(err, ErrMailTemplateInvalid) {
			t.Errorf("%s: error = %v; want invalid", name, err)
		}
	}
}

func TestTransactionalTemplateDeclaresEveryPlainVariable(t *testing.T) {
	fixture := transactionalFixture(t)
	input := transactionalInput()
	input.HTML = ""
	input.Subject = "Your code"
	input.Text = "Code {{code}}"
	input.VariableDefinitions = []domain.TransactionalVariable{{Name: "code", Secret: true, Required: true}}
	if _, err := fixture.service.CreateTemplate(context.Background(), as(1), input); err != nil {
		t.Fatalf("one secret variable: %v", err)
	}
}

func TestStandardTemplateRefusesTransactionalFields(t *testing.T) {
	fixture := transactionalFixture(t)
	for name, change := range map[string]func(*TemplateInput){
		"key":       func(i *TemplateInput) { i.Key = "welcome" },
		"sender":    func(i *TemplateInput) { i.FromAddress = "no-reply@mail.example" },
		"from name": func(i *TemplateInput) { i.FromName = "Acme" },
		"variables": func(i *TemplateInput) {
			i.VariableDefinitions = []domain.TransactionalVariable{{Name: "code", Required: true}}
		},
		"unknown kind": func(i *TemplateInput) { i.Kind = "marketing" },
	} {
		input := templateInput()
		change(&input)
		if _, err := fixture.service.CreateTemplate(context.Background(), as(1), input); !errors.Is(err, ErrMailTemplateInvalid) {
			t.Errorf("%s: error = %v; want invalid", name, err)
		}
	}
}

func TestSecretVariableCannotHaveFallback(t *testing.T) {
	fixture := transactionalFixture(t)
	withFallback := transactionalInput()
	withFallback.Text = "Code {{code|000000}}"
	if _, err := fixture.service.CreateTemplate(context.Background(), as(1), withFallback); !errors.Is(err, ErrMailTemplateInvalid) {
		t.Fatalf("secret with fallback error = %v", err)
	}
	optionalSecret := transactionalInput()
	optionalSecret.VariableDefinitions[0].Required = false
	optionalSecret.Text = "Code {{code|none}}"
	optionalSecret.HTML = "<p>Code {{code|none}} for {{product|Acme}}</p>"
	if _, err := fixture.service.CreateTemplate(context.Background(), as(1), optionalSecret); !errors.Is(err, ErrMailTemplateInvalid) {
		t.Fatalf("optional secret error = %v", err)
	}
}

func TestOptionalVariableNeedsFallback(t *testing.T) {
	fixture := transactionalFixture(t)
	bare := transactionalInput()
	bare.Subject = "Your code for {{product}}"
	if _, err := fixture.service.CreateTemplate(context.Background(), as(1), bare); !errors.Is(err, ErrMailTemplateInvalid) {
		t.Fatalf("optional without fallback error = %v", err)
	}
	required := transactionalInput()
	required.VariableDefinitions[1].Required = true
	if _, err := fixture.service.CreateTemplate(context.Background(), as(1), required); !errors.Is(err, ErrMailTemplateInvalid) {
		t.Fatalf("required with fallback error = %v", err)
	}
}

func TestTransactionalTemplateSenderMustBeVerified(t *testing.T) {
	fixture := newDomainFixture(t)
	ctx := context.Background()
	fixture.domains.domains[90] = &domain.MailDomain{ID: 90, OrganizationID: 7, Name: "pending.example.com", Status: domain.MailDomainStatusPending}
	fixture.domains.domains[91] = &domain.MailDomain{ID: 91, OrganizationID: 7, Name: "acme.example.com", Status: domain.MailDomainStatusVerified}
	fixture.domains.domains[92] = &domain.MailDomain{ID: 92, OrganizationID: 8, Name: "other.example.com", Status: domain.MailDomainStatusVerified}

	for address, want := range map[string]error{
		"no-reply@pending.example.com": ErrMailDomainNotVerified,
		"no-reply@other.example.com":   ErrMailDomainNotVerified,
		"no-reply@unknown.example.com": ErrMailDomainNotVerified,
		// The instance domain is allowed only with the log provider; this fixture has none.
		"no-reply@mail.example":     ErrMailDomainNotVerified,
		"no-reply@acme.example.com": nil,
	} {
		input := transactionalInput()
		input.Key = strings.ReplaceAll(strings.Split(address, "@")[1], ".", "-")
		input.Name = address
		input.FromAddress = address
		if _, err := fixture.service.CreateTemplate(ctx, as(7), input); !errors.Is(err, want) && (want != nil || err != nil) {
			t.Errorf("%s: error = %v; want %v", address, err, want)
		}
	}

	fixture.service.settings.Provider = config.MailboxProviderLog
	input := transactionalInput()
	input.Key = "instance"
	if _, err := fixture.service.CreateTemplate(ctx, as(7), input); err != nil {
		t.Fatalf("log provider with the instance domain: %v", err)
	}
}

func TestMemberCannotWriteTransactionalTemplate(t *testing.T) {
	fixture := transactionalFixture(t)
	ctx := context.Background()
	member := viewerAs(1, domain.OrganizationRoleMember)
	if _, err := fixture.service.CreateTemplate(ctx, member, transactionalInput()); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member create error = %v", err)
	}
	stored, err := fixture.service.CreateTemplate(ctx, viewerAs(1, domain.OrganizationRoleAdmin), transactionalInput())
	if err != nil {
		t.Fatalf("admin create: %v", err)
	}
	if _, err = fixture.service.UpdateTemplate(ctx, member, stored.ID, transactionalInput()); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member update error = %v", err)
	}
	// A member cannot turn a transactional template into a standard one to slip past the check.
	if _, err = fixture.service.UpdateTemplate(ctx, member, stored.ID, templateInput()); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member update to standard error = %v", err)
	}
	if err = fixture.service.DeleteTemplate(ctx, member, stored.ID); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member delete error = %v", err)
	}
	if _, err = fixture.service.GetTemplate(ctx, 1, stored.ID); err != nil {
		t.Fatalf("template after refused writes: %v", err)
	}

	standard, err := fixture.service.CreateTemplate(ctx, member, templateInput())
	if err != nil {
		t.Fatalf("member creates a standard template: %v", err)
	}
	changed := templateInput()
	changed.Subject = "Changed"
	if _, err = fixture.service.UpdateTemplate(ctx, member, standard.ID, changed); err != nil {
		t.Fatalf("member updates a standard template: %v", err)
	}
	if err = fixture.service.DeleteTemplate(ctx, member, standard.ID); err != nil {
		t.Fatalf("member deletes a standard template: %v", err)
	}
	if err = fixture.service.DeleteTemplate(ctx, viewerAs(1, domain.OrganizationRoleOwner), stored.ID); err != nil {
		t.Fatalf("owner deletes a transactional template: %v", err)
	}
}

func TestTemplateKindIsImmutable(t *testing.T) {
	fixture := transactionalFixture(t)
	ctx := context.Background()
	transactional, err := fixture.service.CreateTemplate(ctx, as(1), transactionalInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	standard, err := fixture.service.CreateTemplate(ctx, as(1), templateInput())
	if err != nil {
		t.Fatalf("CreateTemplate standard: %v", err)
	}
	if _, err = fixture.service.UpdateTemplate(ctx, as(1), transactional.ID, templateInput()); !errors.Is(err, ErrMailTemplateKindImmutable) {
		t.Fatalf("transactional to standard error = %v", err)
	}
	if _, err = fixture.service.UpdateTemplate(ctx, as(1), standard.ID, transactionalInput()); !errors.Is(err, ErrMailTemplateKindImmutable) {
		t.Fatalf("standard to transactional error = %v", err)
	}
	changed := transactionalInput()
	changed.Key = "signup"
	changed.FromName = ""
	updated, err := fixture.service.UpdateTemplate(ctx, as(1), transactional.ID, changed)
	if err != nil || updated.Key != "signup" || updated.FromName != "" || updated.Kind != domain.MailTemplateTransactional {
		t.Fatalf("UpdateTemplate = %+v, %v", updated, err)
	}
}

func TestTemplateKeyUniquePerOrganization(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	svc := NewService(repo, &fakeMemberships{}, repo, repo, repo, repo, nil, nil, nil, &fakeTransport{}, &fakeNotifier{}, Settings{
		Domain: "mail.example", Provider: config.MailboxProviderLog, DailySendLimit: 3,
	}, nil, nil, nil)

	first, err := svc.CreateTemplate(ctx, as(1), transactionalInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if stored, findErr := repo.FindTemplate(ctx, 1, first.ID); findErr != nil || stored.Key != "signup-code" ||
		stored.FromAddress != "no-reply@mail.example" || len(stored.Variables) != 2 || !stored.Variables[0].Secret {
		t.Fatalf("stored = %+v, %v", stored, findErr)
	}
	if found, findErr := repo.FindTemplateByKey(ctx, 1, "signup-code"); findErr != nil || found.ID != first.ID {
		t.Fatalf("FindTemplateByKey = %+v, %v", found, findErr)
	}
	if _, err = repo.FindTemplateByKey(ctx, 2, "signup-code"); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("FindTemplateByKey in another organization = %v", err)
	}

	again := transactionalInput()
	again.Name = "Another code"
	if _, err = svc.CreateTemplate(ctx, as(1), again); !errors.Is(err, ErrMailTemplateKeyTaken) {
		t.Fatalf("duplicate key error = %v", err)
	}
	if _, err = svc.CreateTemplate(ctx, as(2), again); err != nil {
		t.Fatalf("same key in another organization: %v", err)
	}
	other := transactionalInput()
	other.Name, other.Key = "Reset", "reset"
	second, err := svc.CreateTemplate(ctx, as(1), other)
	if err != nil {
		t.Fatalf("second key: %v", err)
	}
	other.Key = "signup-code"
	if _, err = svc.UpdateTemplate(ctx, as(1), second.ID, other); !errors.Is(err, ErrMailTemplateKeyTaken) {
		t.Fatalf("rename onto a taken key error = %v", err)
	}
	// The name check still answers name taken.
	byName := transactionalInput()
	byName.Key = "fresh"
	if _, err = svc.CreateTemplate(ctx, as(1), byName); !errors.Is(err, ErrMailTemplateNameTaken) {
		t.Fatalf("duplicate name error = %v", err)
	}
}

func TestListTemplatesFiltersByKind(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	svc := NewService(repo, &fakeMemberships{}, repo, repo, repo, repo, nil, nil, nil, &fakeTransport{}, &fakeNotifier{}, Settings{
		Domain: "mail.example", Provider: config.MailboxProviderLog, DailySendLimit: 3,
	}, nil, nil, nil)
	if _, err := svc.CreateTemplate(ctx, as(1), transactionalInput()); err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if _, err := svc.CreateTemplate(ctx, as(1), templateInput()); err != nil {
		t.Fatalf("CreateTemplate standard: %v", err)
	}
	for kind, want := range map[domain.MailTemplateKind][]string{
		"":                               {"Signup code", "Welcome email"},
		domain.MailTemplateStandard:      {"Welcome email"},
		domain.MailTemplateTransactional: {"Signup code"},
	} {
		listed, err := svc.ListTemplates(ctx, 1, kind)
		if err != nil {
			t.Fatalf("ListTemplates(%q): %v", kind, err)
		}
		var names []string
		for _, template := range listed {
			names = append(names, template.Name)
		}
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Errorf("ListTemplates(%q) = %v; want %v", kind, names, want)
		}
	}
}

func TestTransactionalTemplateRefusedForMailboxSendTestAndBroadcast(t *testing.T) {
	fixture := newBroadcastFixture(t, 10)
	ctx := context.Background()
	fixture.service.settings.Provider = config.MailboxProviderLog
	template, err := fixture.service.CreateTemplate(ctx, as(1), transactionalInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	values := map[string]string{"code": "1"}

	_, _, err = fixture.service.SendMessage(ctx, as(1), fixture.mailbox.ID, SendInput{
		To: []string{"bob@example.com"}, Template: &TemplateSendInput{ID: template.ID, Variables: values},
	})
	if !errors.Is(err, ErrMailTemplateKindMismatch) {
		t.Fatalf("mailbox send error = %v", err)
	}
	if _, err = fixture.service.SendTemplateTest(ctx, as(1), template.ID, fixture.mailbox.ID, []string{"qa@example.com"}, values); !errors.Is(err, ErrMailTemplateKindMismatch) {
		t.Fatalf("template test error = %v", err)
	}
	input := BroadcastInput{
		Name: "News", MailboxID: fixture.mailbox.ID, TemplateID: template.ID, ListID: fixture.list.ID,
		UnsubscribeLabel: "Leave",
	}
	if _, err = fixture.service.CreateBroadcast(ctx, as(1), input); !errors.Is(err, ErrMailTemplateKindMismatch) {
		t.Fatalf("broadcast create error = %v", err)
	}
	draft := fixture.draft(t)
	if _, err = fixture.service.UpdateBroadcast(ctx, as(1), draft.ID, input); !errors.Is(err, ErrMailTemplateKindMismatch) {
		t.Fatalf("broadcast update error = %v", err)
	}
	// A draft that already points at a transactional template is refused when it starts.
	fixture.contact(t, "ann@example.com", "Ann", domain.MailContactSubscribed, true)
	if err = fixture.repo.db.Exec(`UPDATE mail_broadcasts SET template_id = ? WHERE id = ?`, template.ID, draft.ID).Error; err != nil {
		t.Fatalf("point draft at the transactional template: %v", err)
	}
	if _, err = fixture.service.SendBroadcast(ctx, as(1), draft.ID, nil); !errors.Is(err, ErrMailTemplateKindMismatch) {
		t.Fatalf("broadcast send error = %v", err)
	}
	if len(fixture.transport.sent) != 0 {
		t.Fatalf("provider calls = %d; want none", len(fixture.transport.sent))
	}
}

func TestDeleteDomainUsedByTransactionalTemplate(t *testing.T) {
	repo := openDomainRepository(t)
	ctx := context.Background()
	mailDomain := &domain.MailDomain{OrganizationID: 1, Name: "acme.example.com", ZoneID: "z", ZoneName: "example.com", CreatedBy: 1}
	if err := repo.CreateDomain(ctx, mailDomain, 10); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	template := &domain.MailTemplate{
		OrganizationID: 1, Name: "Code", Subject: "Code", TextBody: "{{code}}", CreatedBy: 1,
		Kind: domain.MailTemplateTransactional, Key: "code", FromAddress: "no-reply@acme.example.com",
		Variables: []domain.TransactionalVariable{{Name: "code", Required: true}},
	}
	if err := repo.CreateTemplate(ctx, template, 10); err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	// A sender on a look-alike domain does not hold this one.
	lookAlike := *template
	lookAlike.ID, lookAlike.Name, lookAlike.Key, lookAlike.FromAddress = 0, "Other", "other", "no-reply@xacme.example.com"
	if err := repo.CreateTemplate(ctx, &lookAlike, 10); err != nil {
		t.Fatalf("CreateTemplate look-alike: %v", err)
	}
	if err := repo.DeleteDomain(ctx, 1, mailDomain.ID); !errors.Is(err, ErrMailDomainInUse) {
		t.Fatalf("delete while a transactional template sends from it = %v", err)
	}
	if err := repo.DeleteTemplate(ctx, 1, template.ID); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	if err := repo.DeleteDomain(ctx, 1, mailDomain.ID); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}
}

func TestTemplateRoutesMapTransactionalErrors(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	fixture.service.settings.Provider = config.MailboxProviderLog
	ctx := context.Background()
	mailbox, err := fixture.service.CreateMailbox(ctx, as(7), sharedWithHandlerUser("hello"))
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	template, err := fixture.service.CreateTemplate(ctx, viewerOf(7, handlerUser, domain.OrganizationRoleOwner), transactionalInput())
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	body := `{"kind":"transactional","name":"Other","key":"signup-code","from_address":"no-reply@mail.example",` +
		`"subject":"Code","text":"Code {{code}}","variable_definitions":[{"name":"code","secret":true,"required":true}]}`
	recorder, decoded := fixture.do(http.MethodPost, "/mail-templates", body)
	if recorder.Code != http.StatusConflict || decoded["error_code"] != domain.CodeMailTemplateKeyTaken {
		t.Fatalf("key taken = %d %v", recorder.Code, decoded)
	}
	recorder, decoded = fixture.do(http.MethodPut, "/mail-templates/"+itoa(template.ID), `{"name":"Signup code","subject":"Hi","text":"Hi"}`)
	if recorder.Code != http.StatusConflict || decoded["error_code"] != domain.CodeMailTemplateKindImmutable {
		t.Fatalf("kind immutable = %d %v", recorder.Code, decoded)
	}
	recorder, decoded = fixture.do(http.MethodPost, "/mailboxes/"+itoa(mailbox.ID)+"/messages",
		`{"to":["bob@example.com"],"template":{"id":`+itoa(template.ID)+`,"variables":{"code":"1"}}}`)
	if recorder.Code != http.StatusUnprocessableEntity || decoded["error_code"] != domain.CodeMailTemplateKindMismatch {
		t.Fatalf("kind mismatch = %d %v", recorder.Code, decoded)
	}

	recorder, decoded = fixture.do(http.MethodGet, "/mail-templates?kind=transactional", "")
	data, _ := decoded["data"].([]any)
	if recorder.Code != http.StatusOK || len(data) != 1 {
		t.Fatalf("list transactional = %d %v", recorder.Code, decoded)
	}
	first, _ := data[0].(map[string]any)
	from, _ := first["from"].(map[string]any)
	if first["kind"] != "transactional" || first["key"] != "signup-code" || from["address"] != "no-reply@mail.example" {
		t.Fatalf("summary = %v", first)
	}
	if recorder, _ = fixture.do(http.MethodGet, "/mail-templates?kind=marketing", ""); recorder.Code != http.StatusUnprocessableEntity && recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind = %d", recorder.Code)
	}
}

func TestMemberTemplateRouteIsForbiddenForTransactional(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	fixture.service.settings.Provider = config.MailboxProviderLog
	body := `{"kind":"transactional","name":"Code","key":"signup-code","from_address":"no-reply@mail.example",` +
		`"subject":"Code","text":"Code {{code}}","variable_definitions":[{"name":"code","secret":true,"required":true}]}`
	recorder, decoded := fixture.do(http.MethodPost, "/mail-templates", body)
	if recorder.Code != http.StatusForbidden || decoded["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("member create = %d %v", recorder.Code, decoded)
	}
}

func TestTransactionalTemplateWithoutPlaceholdersNeedsNoDeclarations(t *testing.T) {
	fixture := transactionalFixture(t)
	input := transactionalInput()
	input.Subject, input.HTML, input.Text = "Welcome", "<p>Welcome aboard</p>", "Welcome aboard"
	input.VariableDefinitions = nil
	input.SampleVariables = nil
	template, err := fixture.service.CreateTemplate(context.Background(), as(1), input)
	if err != nil {
		t.Fatalf("CreateTemplate without placeholders: %v", err)
	}
	if response := toMailTemplateResponse(template); response.VariableDefinitions == nil || len(response.VariableDefinitions) != 0 {
		t.Fatalf("variable_definitions = %v; want an empty list", response.VariableDefinitions)
	}
}

func TestTemplateRouteRequiresEveryDefinitionField(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	fixture.service.settings.Provider = config.MailboxProviderLog
	for name, definition := range map[string]string{
		"secret":   `{"name":"code","required":true}`,
		"required": `{"name":"code","secret":true}`,
		"name":     `{"secret":true,"required":true}`,
	} {
		body := `{"kind":"transactional","name":"Code","key":"signup-code","from_address":"no-reply@mail.example",` +
			`"subject":"Code","text":"Code {{code}}","variable_definitions":[` + definition + `]}`
		recorder, decoded := fixture.do(http.MethodPost, "/mail-templates", body)
		if recorder.Code != http.StatusUnprocessableEntity || decoded["error_code"] != "COMMON.VALIDATION_FAILED" {
			t.Errorf("missing %s = %d %v", name, recorder.Code, decoded)
		}
	}
	body := `{"kind":"transactional","name":"Code","key":"signup-code","from_address":"no-reply@mail.example",` +
		`"subject":"Code","text":"Code {{code}}","variable_definitions":[{"name":"code","secret":false,"required":true}]}`
	if recorder, decoded := fixture.do(http.MethodPost, "/mail-templates", body); recorder.Code != http.StatusCreated {
		t.Fatalf("explicit false secret = %d %v", recorder.Code, decoded)
	}
}
