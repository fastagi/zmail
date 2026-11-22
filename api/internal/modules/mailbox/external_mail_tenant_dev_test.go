package mailbox

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// Tests for acceptance round 3, defect D1: the fake connector serves development tenants on a running
// stack, and no production configuration reaches them.

const devTenant = "acme.invalid"

func devTenantGoogle(adminSubject, key string) ExternalMailTenantInput {
	return ExternalMailTenantInput{Provider: domain.ExternalMailProviderGmail, Grant: ExternalMailTenantGrant{
		ServiceAccountKey: key, AdminSubject: adminSubject,
	}}
}

// newDevTenantFixture authorizes the development tenant acme.invalid through the service, as
// POST /v1/external-mail-tenants does, with no AddTenant call.
func newDevTenantFixture(t *testing.T) (*externalFixture, domain.OrganizationContext, *domain.ExternalMailTenant) {
	t.Helper()
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.service.settings.ExternalMail.TenantsEnabled = true
	owner := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleOwner)
	result, err := f.service.AuthorizeExternalMailTenant(context.Background(), owner, devTenantGoogle("it@"+devTenant, FakeDevTenantSecret))
	if err != nil || !result.Created {
		t.Fatalf("authorize development tenant = %+v, %v", result, err)
	}
	if result.Tenant.Tenant.TenantIdentifier != devTenant {
		t.Fatalf("tenant identifier = %q", result.Tenant.Tenant.TenantIdentifier)
	}
	return f, owner, result.Tenant.Tenant
}

// otherProcess swaps the service's fake for a fresh one, as the separate sync worker process has.
func (f *externalFixture) otherProcess() {
	f.service.externalConnectors = fakeExternalMailRegistry(NewFakeExternalMailSource())
}

// TestFakeDevTenantAuthorization: the fake accepts only its synthetic credential with a reserved
// .invalid identifier, in the Google and the Microsoft form; anything else is still refused.
func TestFakeDevTenantAuthorization(t *testing.T) {
	f, owner, _ := newDevTenantFixture(t)
	ctx := context.Background()
	outlook := ExternalMailTenantInput{Provider: domain.ExternalMailProviderOutlook, Grant: ExternalMailTenantGrant{
		TenantID: "globex.invalid", ClientID: "fake", ClientSecret: FakeDevTenantSecret,
	}}
	if result, err := f.service.AuthorizeExternalMailTenant(ctx, owner, outlook); err != nil || !result.Created ||
		result.Tenant.Tenant.TenantIdentifier != "globex.invalid" {
		t.Fatalf("Microsoft form = %+v, %v", result, err)
	}
	for name, input := range map[string]ExternalMailTenantInput{
		"other key":    devTenantGoogle("it@"+devTenant, `{"type":"service_account"}`),
		"real domain":  devTenantGoogle("it@acme.example", FakeDevTenantSecret),
		"bad domain":   devTenantGoogle("it@acme..invalid", FakeDevTenantSecret),
		"bare suffix":  devTenantGoogle("it@.invalid", FakeDevTenantSecret),
		"other secret": {Provider: domain.ExternalMailProviderOutlook, Grant: ExternalMailTenantGrant{TenantID: devTenant, ClientID: "fake", ClientSecret: "secret"}},
		"refused key":  devTenantGoogle("it@"+devTenant, "refuse"),
		"underscore":   {Provider: domain.ExternalMailProviderOutlook, Grant: ExternalMailTenantGrant{TenantID: "under_score.invalid", ClientID: "fake", ClientSecret: FakeDevTenantSecret}},
	} {
		if _, err := f.service.AuthorizeExternalMailTenant(ctx, owner, input); !errors.Is(err, ErrExternalMailAuthorizationFailed) {
			t.Errorf("%s: err = %v, want the authorization refused", name, err)
		}
	}
}

// TestFakeDevTenantDirectoryPages: the directory is the same in every process and has 31 rows in
// order, user01@ to user30@ and refused@, over pages.
func TestFakeDevTenantDirectoryPages(t *testing.T) {
	f, owner, tenant := newDevTenantFixture(t)
	f.otherProcess()
	var addresses []string
	cursor := ""
	pages := 0
	for {
		page, err := f.service.ExternalMailTenantDirectory(context.Background(), owner, tenant.ID, cursor, 10)
		if err != nil {
			t.Fatalf("directory page %d: %v", pages, err)
		}
		pages++
		for _, entry := range page.Entries {
			addresses = append(addresses, entry.Entry.Address)
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if pages != 4 || len(addresses) != FakeDevTenantDirectorySize+1 || addresses[0] != "user01@"+devTenant ||
		addresses[29] != "user30@"+devTenant || addresses[30] != FakeDevTenantRefusedLocal+"@"+devTenant {
		t.Fatalf("%d pages: %v", pages, addresses)
	}
}

// TestFakeDevTenantMailboxReceivesFakeDeliveries: a mailbox assigned to a member receives mail through
// the development delivery route; a worker pass in another process keeps the tenant and the
// assignment working; the refused@ mailbox loses access alone.
func TestFakeDevTenantMailboxReceivesFakeDeliveries(t *testing.T) {
	f, _, tenant := newDevTenantFixture(t)
	ctx := context.Background()
	results, err := f.service.AssignExternalMailTenantMailboxes(ctx, f.admin, tenant.ID, []ExternalMailTenantAssignmentInput{
		{Address: "user05@" + devTenant, Assign: TenantAssignPersonal, UserID: accessC},
		{Address: FakeDevTenantRefusedLocal + "@" + devTenant, Assign: TenantAssignPersonal, UserID: accessC},
	})
	if err != nil || len(results) != 2 || results[0].Connection == nil || results[1].Connection == nil {
		t.Fatalf("assign = %+v, %v", results, err)
	}
	assigned, refused := results[0].Connection, results[1].Connection

	status, body := f.deliverFake(t, f.c, assigned.ID, `{"from":"carol@sender.test","subject":"Tenant hello","text":"Body","message_id":"t-1@sender.test"}`)
	if status != http.StatusCreated {
		t.Fatalf("delivery = %d %v", status, body)
	}
	messages, _, err := f.service.ListMessages(ctx, f.c, assigned.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 50})
	if err != nil || len(messages) != 1 || messages[0].Subject != "Tenant hello" {
		t.Fatalf("assigned mailbox = %+v, %v", messages, err)
	}
	if status, _ := f.deliverFake(t, f.b, assigned.ID, `{"from":"carol@sender.test","text":"x"}`); status != http.StatusNotFound {
		t.Fatalf("another member delivering = %d, want 404", status)
	}

	f.otherProcess()
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if got := f.reloadConnection(t, assigned.ID); got.Status != domain.ExternalMailStatusActive {
		t.Fatalf("the assignment is %q after a worker pass in another process", got.Status)
	}
	if got := f.reloadConnection(t, refused.ID); got.Status == domain.ExternalMailStatusActive {
		t.Fatal("the refused@ mailbox kept syncing")
	}
	current, err := f.service.externalMail.FindExternalMailTenant(ctx, accessOrg, tenant.ID)
	if err != nil || current.Status != domain.ExternalMailTenantActive {
		t.Fatalf("tenant = %+v, %v; want it active", current, err)
	}
}

// TestFakeDevTenantUnreachableInProduction: configuration refuses `fake` in production (see
// config.TestExternalMailFakeRefusedInProduction); every configuration production accepts builds
// real connectors only, so neither the synthetic credential nor the development delivery route is
// reachable, and the real Google tenant source refuses the synthetic credential without a network call.
func TestFakeDevTenantUnreachableInProduction(t *testing.T) {
	production := config.ExternalMailConfig{
		Providers: []string{config.ExternalMailProviderGmail, config.ExternalMailProviderOutlook},
		OAuth: map[string]config.ExternalMailOAuthClient{
			config.ExternalMailProviderGmail:   {ClientID: "client", ClientSecret: "secret"},
			config.ExternalMailProviderOutlook: {ClientID: "client", ClientSecret: "secret"},
		},
		TenantsEnabled: true,
	}
	if production.Source() == config.ExternalMailSourceFake {
		t.Fatal("a real provider list selected the fake source")
	}
	registry := newExternalMailRegistry(production)
	if len(registry.list()) != 2 {
		t.Fatalf("connectors = %+v", registry.list())
	}
	for _, connector := range registry.list() {
		if _, fake := connector.Source.(*FakeExternalMailSource); fake {
			t.Fatalf("%s: fake mail source", connector.ID)
		}
		if _, fake := connector.Tenant.(*FakeExternalMailSource); fake || connector.Tenant == nil {
			t.Fatalf("%s: tenant source %T", connector.ID, connector.Tenant)
		}
	}

	f := newExternalFixture(t, production.Providers...)
	f.service.settings.ExternalMail = production
	f.service.externalConnectors = registry
	if f.service.fakeExternalMailSource() != nil {
		t.Fatal("the fake source is reachable with real connectors")
	}
	if hasRoute(f.fakeDeliveryRouter(f.b), http.MethodPost, fakeDeliveryRoute) {
		t.Fatal("the development delivery route is registered with real connectors")
	}
	owner := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleOwner)
	if _, err := f.service.AuthorizeExternalMailTenant(context.Background(), owner, devTenantGoogle("it@"+devTenant, FakeDevTenantSecret)); err == nil {
		t.Fatal("the real Google tenant source accepted the synthetic credential")
	}
	if n := f.count(t, `SELECT count(*) FROM external_mail_tenants`); n != 0 {
		t.Fatalf("%d tenants stored", n)
	}
}
