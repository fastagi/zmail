package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

const developerUser uint = 4242

// developerOf is a member of organizationID holding only the developer grant with this scope.
func developerOf(organizationID uint, grant domain.DeveloperGrant) domain.OrganizationContext {
	viewer := viewerOf(organizationID, developerUser, domain.OrganizationRoleMember)
	viewer.Features = domain.MemberFeatures{Developer: true}
	viewer.DeveloperGrant = &grant
	return viewer
}

// developerDomainFixture is organization 7 with two verified domains and one pending.
func developerDomainFixture(t *testing.T) *domainFixture {
	t.Helper()
	fixture := newDomainFixture(t)
	fixture.domains.domains[90] = &domain.MailDomain{ID: 90, OrganizationID: 7, Name: "fast-ui.com", Status: domain.MailDomainStatusVerified}
	fixture.domains.domains[91] = &domain.MailDomain{ID: 91, OrganizationID: 7, Name: "shu.ie", Status: domain.MailDomainStatusVerified}
	fixture.domains.domains[92] = &domain.MailDomain{ID: 92, OrganizationID: 7, Name: "pending.example", Status: domain.MailDomainStatusPending}
	return fixture
}

func transactionalFrom(key, address string) TemplateInput {
	input := transactionalInput()
	input.Key, input.Name, input.FromAddress = key, key, address
	return input
}

func TestDeveloperWritesTransactionalTemplatesOnlyInsideScope(t *testing.T) {
	fixture := developerDomainFixture(t)
	ctx := context.Background()
	developer := developerOf(7, domain.DeveloperGrant{Domains: []string{"fast-ui.com"}})

	own, err := fixture.service.CreateTemplate(ctx, developer, transactionalFrom("order-paid", "no-reply@fast-ui.com"))
	if err != nil {
		t.Fatalf("developer creates an in-scope template: %v", err)
	}
	if _, err = fixture.service.CreateTemplate(ctx, developer, transactionalFrom("internal", "x@shu.ie")); !errors.Is(err, domain.ErrDeveloperDomainNotAllowed) {
		t.Fatalf("out-of-scope create error = %v", err)
	}
	if _, err = fixture.service.UpdateTemplate(ctx, developer, own.ID, transactionalFrom("order-paid", "x@shu.ie")); !errors.Is(err, domain.ErrDeveloperDomainNotAllowed) {
		t.Fatalf("moving the sender out of scope error = %v", err)
	}
	changed := transactionalFrom("order-paid", "billing@fast-ui.com")
	if _, err = fixture.service.UpdateTemplate(ctx, developer, own.ID, changed); err != nil {
		t.Fatalf("in-scope update: %v", err)
	}

	foreign, err := fixture.service.CreateTemplate(ctx, as(7), transactionalFrom("alert", "ops@shu.ie"))
	if err != nil {
		t.Fatalf("owner creates: %v", err)
	}
	if _, err = fixture.service.UpdateTemplate(ctx, developer, foreign.ID, transactionalFrom("alert", "ops@fast-ui.com")); !errors.Is(err, domain.ErrDeveloperDomainNotAllowed) {
		t.Fatalf("editing an out-of-scope template error = %v", err)
	}
	if err = fixture.service.DeleteTemplate(ctx, developer, foreign.ID); !errors.Is(err, domain.ErrDeveloperDomainNotAllowed) {
		t.Fatalf("deleting an out-of-scope template error = %v", err)
	}
	if err = fixture.service.DeleteTemplate(ctx, developer, own.ID); err != nil {
		t.Fatalf("deleting an in-scope template: %v", err)
	}

	// The developer grant does not include standard templates.
	if _, err = fixture.service.CreateTemplate(ctx, developer, templateInput()); !errors.Is(err, domain.ErrOrganizationFeatureNotGranted) {
		t.Fatalf("standard template without the templates grant error = %v", err)
	}
}

func TestAllDomainsScopeFollowsVerifiedDomainsOnly(t *testing.T) {
	fixture := developerDomainFixture(t)
	ctx := context.Background()
	developer := developerOf(7, domain.DeveloperGrant{AllDomains: true})
	if _, err := fixture.service.CreateTemplate(ctx, developer, transactionalFrom("internal", "x@shu.ie")); err != nil {
		t.Fatalf("all domains covers a verified domain: %v", err)
	}
	if err := fixture.service.requireDeveloperSender(ctx, developer, "x@pending.example"); !errors.Is(err, domain.ErrDeveloperDomainNotAllowed) {
		t.Fatalf("pending domain error = %v", err)
	}
	if err := fixture.service.requireDeveloperSender(ctx, developer, "x@mail.example"); !errors.Is(err, domain.ErrDeveloperDomainNotAllowed) {
		t.Fatalf("the shared domain is never in scope: %v", err)
	}
	if err := fixture.service.requireDeveloperSender(ctx, as(7), "x@pending.example"); err != nil {
		t.Fatalf("owners are unrestricted: %v", err)
	}
	plain := viewerOf(7, developerUser, domain.OrganizationRoleMember)
	if err := fixture.service.requireDeveloperSender(ctx, plain, "x@fast-ui.com"); !errors.Is(err, domain.ErrOrganizationFeatureNotGranted) {
		t.Fatalf("a member without a stored grant error = %v", err)
	}
}

func TestTemplateListMarksTemplatesOutsideTheDeveloperScope(t *testing.T) {
	fixture := developerDomainFixture(t)
	ctx := context.Background()
	inside, err := fixture.service.CreateTemplate(ctx, as(7), transactionalFrom("inside", "a@fast-ui.com"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := fixture.service.CreateTemplate(ctx, as(7), transactionalFrom("outside", "a@shu.ie"))
	if err != nil {
		t.Fatal(err)
	}
	standard, err := fixture.service.CreateTemplate(ctx, as(7), templateInput())
	if err != nil {
		t.Fatal(err)
	}
	templates := []*domain.MailTemplate{inside, outside, standard}
	developer := developerOf(7, domain.DeveloperGrant{Domains: []string{"fast-ui.com"}})
	marked := fixture.service.TemplatesOutsideSendingScope(ctx, developer, templates)
	if len(marked) != 1 || !marked[outside.ID] {
		t.Fatalf("marked = %v; want only %d", marked, outside.ID)
	}
	if got := fixture.service.TemplatesOutsideSendingScope(ctx, as(7), templates); len(got) != 0 {
		t.Fatalf("owners see nothing marked: %v", got)
	}
}

func TestDeveloperReadsSuppressionsAndLanes(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	id := f.suppress(t, 10, "gone@example.com", domain.MailSuppressionReasonBounced, f.clock)
	developer := developerOf(10, domain.DeveloperGrant{Domains: []string{"mail.example"}})
	if _, _, err := f.service.ListSendLanes(context.Background(), developer); err != nil {
		t.Fatalf("ListSendLanes(developer) = %v", err)
	}
	if _, _, err := f.service.ListSuppressions(context.Background(), developer, domain.MailSuppressionFilter{}); err != nil {
		t.Fatalf("ListSuppressions(developer) = %v", err)
	}
	if err := f.service.RemoveSuppression(context.Background(), developer, id); err != nil {
		t.Fatalf("RemoveSuppression(developer) = %v", err)
	}
	if f.suppressionCount(t) != 0 {
		t.Fatal("the developer's removal did not remove the entry")
	}
}

// TestDeveloperReachesSuppressionAndLaneRoutes covers the HTTP gate: the handler fixture's key
// owner is a developer member.
func TestDeveloperReachesSuppressionAndLaneRoutes(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	id := f.suppress(t, 10, "gone@example.com", domain.MailSuppressionReasonBounced, f.clock)
	for _, route := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/v1/mail-send-lanes", http.StatusOK},
		{http.MethodGet, "/v1/mail-suppressions", http.StatusOK},
		{http.MethodDelete, fmt.Sprintf("/v1/mail-suppressions/%d", id), http.StatusNoContent},
	} {
		recorder, _ := f.request(route.method, route.path, "", "", "")
		if recorder.Code != route.want {
			t.Fatalf("%s %s = %d %s; want %d", route.method, route.path, recorder.Code, recorder.Body.String(), route.want)
		}
	}
	if f.suppressionCount(t) != 0 {
		t.Fatal("the developer's removal did not remove the entry")
	}
}

func developerActor(grant *domain.DeveloperGrant, features domain.MemberFeatures) TransactionalActor {
	actor := liveActor()
	viewer := viewerOf(10, txUser, domain.OrganizationRoleMember)
	viewer.Features, viewer.DeveloperGrant = features, grant
	actor.Viewer = viewer
	return actor
}

func TestTransactionalSendFollowsTheKeyOwnersCurrentScope(t *testing.T) {
	f := newTxFixture(t, nil)
	ctx := context.Background()

	inScope := developerActor(&domain.DeveloperGrant{Domains: []string{"mail.example"}}, domain.MemberFeatures{Developer: true})
	f.send(t, inScope, codeInput("dev-1", "a@example.com", "111111"))

	narrowed := developerActor(&domain.DeveloperGrant{Domains: []string{"fast-ui.com"}}, domain.MemberFeatures{Developer: true})
	if _, _, err := f.service.SendTransactional(ctx, narrowed, codeInput("dev-2", "a@example.com", "222222")); !errors.Is(err, domain.ErrDeveloperDomainNotAllowed) {
		t.Fatalf("send after the scope was narrowed error = %v", err)
	}

	apiOnly := developerActor(nil, domain.MemberFeatures{APIKeys: true})
	if _, _, err := f.service.SendTransactional(ctx, apiOnly, codeInput("dev-3", "a@example.com", "333333")); !errors.Is(err, domain.ErrOrganizationFeatureNotGranted) {
		t.Fatalf("send with API keys but no developer grant error = %v", err)
	}

	viewer := viewerOf(10, txUser, domain.OrganizationRoleMember)
	viewer.Features = domain.MemberFeatures{APIKeys: true}
	if _, err := f.service.SendTransactionalTest(ctx, viewer, TransactionalTestInput{Template: "signup-code", Mode: domain.APIKeyModeTest}); !errors.Is(err, domain.ErrOrganizationFeatureNotGranted) {
		t.Fatalf("console test send without the grant error = %v", err)
	}
}

func TestTemplateRoutesServeDevelopersAndMarkTheirScope(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	f.grant = &domain.DeveloperGrant{Domains: []string{"fast-ui.com"}}
	recorder, body := f.request(http.MethodGet, "/v1/mail-templates?kind=transactional", "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list = %d %s", recorder.Code, recorder.Body.String())
	}
	items, _ := body["data"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["outside_sending_scope"] != true {
		t.Fatalf("templates = %v; want signup-code marked outside the scope", items)
	}
	created, body := f.request(http.MethodPost, "/v1/mail-templates", "", "",
		`{"kind":"transactional","name":"Paid","key":"paid","from_address":"no-reply@mail.example","subject":"Paid","html":"<p>Paid</p>"}`)
	if created.Code != http.StatusForbidden || body["error_code"] != domain.CodeDeveloperDomainNotAllowed {
		t.Fatalf("out-of-scope create = %d %s", created.Code, created.Body.String())
	}
}

func TestTransactionalWebhookNamesItsSendingDomain(t *testing.T) {
	publisher := &recordingPublisher{}
	cfg := webhookConfig(true)
	cfg.Mailbox.Domain = "mail.example"
	notifier := NewWebhookNotifier(cfg, publisher)
	custom := secretMessage()
	custom.FromAddress = "No-Reply@Fast-UI.com"
	for _, message := range []*domain.TransactionalMessage{custom, secretMessage()} {
		if err := notifier.NotifyTransactional(context.Background(), TransactionalNotification{
			Type: WebhookEmailSent, Message: message,
		}); err != nil {
			t.Fatalf("NotifyTransactional: %v", err)
		}
		message.ID++
	}
	if len(publisher.events) != 2 {
		t.Fatalf("published %d events", len(publisher.events))
	}
	want := []domain.WebhookEventOrigin{
		{Programmatic: true, SenderDomain: "fast-ui.com", SenderDomainVerified: true},
		{Programmatic: true, SenderDomain: "mail.example", SenderDomainVerified: false},
	}
	for index, event := range publisher.events {
		if event.Origin == nil || *event.Origin != want[index] {
			t.Fatalf("event %d origin = %+v; want %+v", index, event.Origin, want[index])
		}
	}
}
