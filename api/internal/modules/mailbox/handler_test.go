package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
	"github.com/fastagi/zmail/api/pkg/response"
)

// handlerFixture serves the handlers directly, with the organization context a real request
// would get from the organization_context middleware.
type handlerFixture struct {
	router  *gin.Engine
	service *service
	repo    *fakeRepository
}

func newHandlerFixture(t *testing.T, organization *domain.OrganizationContext) *handlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := newFakeRepository()
	svc := NewService(repo, repo, newFakeContacts(), &stubBroadcasts{}, nil, nil, nil, &fakeTransport{}, &fakeNotifier{}, Settings{
		Domain: "mail.example", InboundTolerance: 5 * time.Minute, DailySendLimit: 5,
	})
	handler := NewHandler(svc)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	response.DefaultErrorMapper.Register(
		domain.ErrOrganizationContextRequired, http.StatusBadRequest, domain.CodeOrganizationContextRequired)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		if organization != nil {
			c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), *organization))
		}
		c.Next()
	})
	router.GET("/mailbox-domains", handler.ListDomains)
	router.GET("/mailboxes", handler.ListMailboxes)
	router.POST("/mailboxes", handler.CreateMailbox)
	router.DELETE("/mailboxes/:mailbox_id", handler.DeleteMailbox)
	router.POST("/mailboxes/:mailbox_id/messages", handler.SendMessage)
	router.GET("/mail-messages", handler.ListLog)
	router.POST("/mail-broadcasts/:broadcast_id/sends", handler.SendBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/cancellations", handler.CancelBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/pauses", handler.PauseBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/resumptions", handler.ResumeBroadcast)
	return &handlerFixture{router: router, service: svc, repo: repo}
}

func (f *handlerFixture) do(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder, decoded
}

func membership(role domain.OrganizationRole) *domain.OrganizationContext {
	return &domain.OrganizationContext{
		OrganizationID: 7, OrganizationName: "Acme", OrganizationSlug: "acme",
		MembershipID: 70, UserID: 700, Role: role,
	}
}

func TestOnlyManagersCreateAndDeleteMailboxes(t *testing.T) {
	for _, role := range []domain.OrganizationRole{domain.OrganizationRoleOwner, domain.OrganizationRoleAdmin} {
		fixture := newHandlerFixture(t, membership(role))
		created, body := fixture.do(http.MethodPost, "/mailboxes", `{"local_part":"helpdesk"}`)
		if created.Code != http.StatusCreated {
			t.Fatalf("%s create = %d %s", role, created.Code, created.Body.String())
		}
		stored := fixture.repo.mailboxes[1]
		if stored == nil || stored.OrganizationID != 7 || stored.CreatedBy != 700 {
			t.Fatalf("%s stored mailbox = %+v; want organization 7 created by user 700", role, stored)
		}
		if data, _ := body["data"].(map[string]any); data["address"] != "helpdesk@mail.example" {
			t.Fatalf("%s response = %v", role, body)
		}
		if deleted, _ := fixture.do(http.MethodDelete, "/mailboxes/1", ""); deleted.Code != http.StatusNoContent {
			t.Fatalf("%s delete = %d", role, deleted.Code)
		}
	}

	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	// A manager of the same organization created this one.
	if _, err := fixture.service.CreateMailbox(t.Context(), 7, 1, "helpdesk", "", ""); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	created, body := fixture.do(http.MethodPost, "/mailboxes", `{"local_part":"sales"}`)
	if created.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("member create = %d %v; want 403 %s", created.Code, body, domain.CodePermissionDenied)
	}
	deleted, body := fixture.do(http.MethodDelete, "/mailboxes/1", "")
	if deleted.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("member delete = %d %v; want 403", deleted.Code, body)
	}
	if len(fixture.repo.mailboxes) != 1 {
		t.Fatalf("mailboxes = %d; a member's requests must change nothing", len(fixture.repo.mailboxes))
	}
}

func TestMembersReadAndSendTheOrganizationsMail(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if _, err := fixture.service.CreateMailbox(t.Context(), 7, 1, "helpdesk", "Helpdesk", ""); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if _, err := fixture.service.CreateMailbox(t.Context(), 8, 1, "elsewhere", "", ""); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	listed, body := fixture.do(http.MethodGet, "/mailboxes", "")
	mailboxes, _ := body["data"].([]any)
	if listed.Code != http.StatusOK || len(mailboxes) != 1 {
		t.Fatalf("member list = %d %v; want only the organization's mailbox", listed.Code, body)
	}
	if listed.Header().Get("Vary") != "Authorization, Organization-Id" || listed.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache headers = %q / %q", listed.Header().Get("Vary"), listed.Header().Get("Cache-Control"))
	}

	sent, body := fixture.do(http.MethodPost, "/mailboxes/1/messages", `{"to":["bob@example.com"],"subject":"Hi","text":"Body"}`)
	if sent.Code != http.StatusCreated {
		t.Fatalf("member send = %d %v", sent.Code, body)
	}
	// Another organization's mailbox is reported as missing, not forbidden.
	other, body := fixture.do(http.MethodPost, "/mailboxes/2/messages", `{"to":["bob@example.com"],"subject":"Hi","text":"Body"}`)
	if other.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailboxNotFound {
		t.Fatalf("send from another organization's mailbox = %d %v; want 404", other.Code, body)
	}

	log, body := fixture.do(http.MethodGet, "/mail-messages", "")
	if entries, _ := body["data"].([]any); log.Code != http.StatusOK || len(entries) != 1 {
		t.Fatalf("log = %d %v; want the one message of this organization", log.Code, body)
	}
}

func TestMailboxRoutesRequireAnOrganizationContext(t *testing.T) {
	fixture := newHandlerFixture(t, nil)
	for _, request := range [][2]string{
		{http.MethodGet, "/mailbox-domains"}, {http.MethodGet, "/mailboxes"},
		{http.MethodPost, "/mailboxes"}, {http.MethodDelete, "/mailboxes/1"},
		{http.MethodPost, "/mailboxes/1/messages"}, {http.MethodGet, "/mail-messages"},
	} {
		recorder, body := fixture.do(request[0], request[1], `{}`)
		if recorder.Code != http.StatusBadRequest || body["error_code"] != domain.CodeOrganizationContextRequired {
			t.Fatalf("%s %s = %d %v; want 400 %s", request[0], request[1], recorder.Code, body, domain.CodeOrganizationContextRequired)
		}
	}
}

func TestOnlyOwnersAndAdminsControlBroadcasts(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	for _, path := range []string{
		"/mail-broadcasts/3/sends", "/mail-broadcasts/3/cancellations",
		"/mail-broadcasts/3/pauses", "/mail-broadcasts/3/resumptions",
	} {
		recorder, body := fixture.do(http.MethodPost, path, "{}")
		if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
			t.Fatalf("member POST %s = %d %v; want 403", path, recorder.Code, body)
		}
	}
}

// fakeResolver answers public DNS lookups from fixed tables, so no test reaches real DNS.
type fakeResolver struct {
	mx      map[string][]*net.MX
	ns      map[string][]*net.NS
	mxErr   error
	nsErr   error
	nsCalls []string
}

func (r *fakeResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if r.mxErr != nil {
		return nil, r.mxErr
	}
	return r.mx[name], nil
}

func (r *fakeResolver) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	r.nsCalls = append(r.nsCalls, name)
	if r.nsErr != nil {
		return nil, r.nsErr
	}
	return r.ns[name], nil
}

// zoneHandlerFixture serves the domain and zone handlers over the in-memory domain fakes.
type zoneHandlerFixture struct {
	*domainFixture
	router   *gin.Engine
	resolver *fakeResolver
}

func newZoneHandlerFixture(t *testing.T, organization *domain.OrganizationContext) *zoneHandlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	domains := newDomainFixture(t)
	resolver := &fakeResolver{mx: map[string][]*net.MX{}, ns: map[string][]*net.NS{}}
	domains.service.mxResolver = resolver
	domains.service.nsResolver = resolver
	handler := NewHandler(domains.service)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), *organization))
		c.Next()
	})
	router.POST("/mail-domains", handler.ConnectMailDomain)
	router.POST("/mail-domains/:domain_id/receiving", handler.EnableMailDomainReceiving)
	router.POST("/mail-domain-previews", handler.PreviewMailDomain)
	router.GET("/mail-zones", handler.ListMailZones)
	router.GET("/mail-zones/:zone_id", handler.GetMailZone)
	router.DELETE("/mail-zones/:zone_id", handler.DeleteMailZone)
	return &zoneHandlerFixture{domainFixture: domains, router: router, resolver: resolver}
}

func (f *zoneHandlerFixture) do(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder, decoded
}

// activeZone stores an active managed zone of organization 7 that the provider serves.
func (f *zoneHandlerFixture) activeZone(name, providerID string) *domain.MailZone {
	f.zones.zones[name] = providerID
	f.zones.zoneStatus[providerID] = "active"
	return f.mailZones.store(domain.MailZone{
		OrganizationID: 7, Name: name, CloudflareZoneID: providerID, Status: domain.MailZoneStatusActive,
		NameServers: fakeNameServers, OriginalNameServers: []string{"ns1.registrar.test", "ns2.registrar.test"},
	})
}

func TestPreviewSuggestsSendOnlyForForeignMX(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	fixture.resolver.mx["mail.example.org"] = []*net.MX{{Host: "MXBIZ1.qq.com.", Pref: 5}, {Host: "route1.mx.cloudflare.net.", Pref: 10}}
	fixture.resolver.mx["news.example.org"] = []*net.MX{{Host: "route1.mx.cloudflare.net.", Pref: 10}}

	recorder, body := fixture.do(http.MethodPost, "/mail-domain-previews", `{"name":" Mail.Example.org. "}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["name"] != "mail.example.org" || data["apex"] != "example.org" ||
		data["suggested_capability"] != "send_only" {
		t.Fatalf("preview = %d %v", recorder.Code, body)
	}
	if mx, _ := data["existing_mx"].([]any); len(mx) != 2 || mx[0] != "mxbiz1.qq.com" || mx[1] != "route1.mx.cloudflare.net" {
		t.Fatalf("existing_mx = %v", data["existing_mx"])
	}

	// Mail that already reaches Cloudflare keeps the default.
	_, body = fixture.do(http.MethodPost, "/mail-domain-previews", `{"name":"news.example.org"}`)
	if data, _ := body["data"].(map[string]any); data["suggested_capability"] != "send_receive" {
		t.Fatalf("cloudflare MX preview = %v", body)
	}
	invalid, body := fixture.do(http.MethodPost, "/mail-domain-previews", `{"name":"com"}`)
	if invalid.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailDomainInvalid {
		t.Fatalf("invalid preview = %d %v", invalid.Code, body)
	}
	if len(fixture.domains.domains) != 0 || len(fixture.zones.createdZones) != 0 {
		t.Fatalf("a preview changed state: domains %v, zones %v", fixture.domains.domains, fixture.zones.createdZones)
	}
}

func TestPreviewLookupFailureSuggestsSendReceive(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleMember))
	fixture.resolver.mxErr = &net.DNSError{Err: "no such host", Name: "example.org", IsNotFound: true}

	recorder, body := fixture.do(http.MethodPost, "/mail-domain-previews", `{"name":"example.org"}`)
	data, _ := body["data"].(map[string]any)
	mx, isList := data["existing_mx"].([]any)
	if recorder.Code != http.StatusOK || !isList || len(mx) != 0 || data["suggested_capability"] != "send_receive" {
		t.Fatalf("preview = %d %v", recorder.Code, body)
	}
}

func TestListZonesScopedToOrganization(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleMember))
	own := fixture.activeZone("example.org", "cf-own")
	other := fixture.mailZones.store(domain.MailZone{
		OrganizationID: 8, Name: "other.test", CloudflareZoneID: "cf-other", Status: domain.MailZoneStatusPending,
	})

	listed, body := fixture.do(http.MethodGet, "/mail-zones", "")
	items, _ := body["data"].([]any)
	if listed.Code != http.StatusOK || len(items) != 1 {
		t.Fatalf("list = %d %v", listed.Code, body)
	}
	if listed.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache = %q", listed.Header().Get("Cache-Control"))
	}
	item, _ := items[0].(map[string]any)
	servers, _ := item["name_servers"].([]any)
	original, _ := item["original_name_servers"].([]any)
	if item["id"] != float64(own.ID) || item["name"] != "example.org" || item["status"] != "active" ||
		len(servers) != 2 || len(original) != 2 {
		t.Fatalf("zone = %v", item)
	}
	if _, present := item["cloudflare_zone_id"]; present {
		t.Fatalf("the provider zone ID leaked: %v", item)
	}

	shown, body := fixture.do(http.MethodGet, fmt.Sprintf("/mail-zones/%d", own.ID), "")
	if data, _ := body["data"].(map[string]any); shown.Code != http.StatusOK || data["name"] != "example.org" {
		t.Fatalf("show = %d %v", shown.Code, body)
	}
	foreign, body := fixture.do(http.MethodGet, fmt.Sprintf("/mail-zones/%d", other.ID), "")
	if foreign.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailZoneNotFound {
		t.Fatalf("another organization's zone = %d %v", foreign.Code, body)
	}
}

func TestDeleteZoneInUse(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	zone := fixture.activeZone("example.org", "cf-own")
	fixture.connect(t, "mail.example.org")

	recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID), "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZoneInUse {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}
	if len(fixture.resolver.nsCalls) != 0 || len(fixture.zones.deletedZones) != 0 || fixture.mailZones.zones[zone.ID] == nil {
		t.Fatalf("ns lookups %v, deleted %v", fixture.resolver.nsCalls, fixture.zones.deletedZones)
	}
}

func TestDeleteZoneStillDelegated(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	zone := fixture.activeZone("example.org", "cf-own")
	fixture.resolver.ns["example.org"] = []*net.NS{{Host: "ns1.registrar.test."}, {Host: "ADA.ns.cloudflare.com."}}
	path := fmt.Sprintf("/mail-zones/%d", zone.ID)

	recorder, body := fixture.do(http.MethodDelete, path, "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZoneStillDelegated {
		t.Fatalf("delete while delegated = %d %v", recorder.Code, body)
	}
	if len(fixture.zones.deletedZones) != 0 || fixture.mailZones.zones[zone.ID] == nil {
		t.Fatalf("a delegated zone was removed: deleted %v", fixture.zones.deletedZones)
	}

	// Once the registrar points elsewhere the zone leaves Zmail and the provider account.
	fixture.resolver.ns["example.org"] = []*net.NS{{Host: "ns1.registrar.test."}}
	recorder, body = fixture.do(http.MethodDelete, path, "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}
	if !slices.Equal(fixture.zones.deletedZones, []string{"cf-own"}) || fixture.mailZones.zones[zone.ID] != nil {
		t.Fatalf("deleted %v, stored %v", fixture.zones.deletedZones, fixture.mailZones.zones)
	}
	gone, body := fixture.do(http.MethodDelete, path, "")
	if gone.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailZoneNotFound {
		t.Fatalf("delete again = %d %v", gone.Code, body)
	}
}

func TestDeleteZoneRefusesOnLookupError(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	zone := fixture.activeZone("example.org", "cf-own")
	fixture.resolver.nsErr = &net.DNSError{Err: "server misbehaving", Name: "example.org", IsTemporary: true}

	recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID), "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZoneStillDelegated {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}
	if len(fixture.zones.deletedZones) != 0 || fixture.mailZones.zones[zone.ID] == nil {
		t.Fatalf("zone removed without knowing its delegation: deleted %v", fixture.zones.deletedZones)
	}
}

func TestDeleteZoneToleratesAZoneGoneAtTheProvider(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	zone := fixture.activeZone("example.org", "cf-own")
	fixture.zones.deleteZoneErr = &mailtransport.DomainError{Operation: "delete zone", Status: http.StatusNotFound}
	if recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID), ""); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}

	// Any other provider refusal keeps the zone so the removal can be tried again.
	kept := fixture.activeZone("example.net", "cf-net")
	fixture.zones.deleteZoneErr = &mailtransport.DomainError{Operation: "delete zone", Status: http.StatusForbidden}
	recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", kept.ID), "")
	if recorder.Code != http.StatusBadGateway || body["error_code"] != domain.CodeMailDomainProviderFailed {
		t.Fatalf("provider refusal = %d %v", recorder.Code, body)
	}
	if fixture.mailZones.zones[kept.ID] == nil {
		t.Fatal("zone row removed although the provider kept the zone")
	}
}

func TestDeletePendingZoneSkipsNSCheck(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	zone := fixture.mailZones.store(domain.MailZone{
		OrganizationID: 7, Name: "example.org", CloudflareZoneID: "cf-pending", Status: domain.MailZoneStatusPending,
	})
	fixture.zones.zones["example.org"] = "cf-pending"
	// Nobody delegates to a pending zone; the public answer does not matter.
	fixture.resolver.nsErr = errors.New("lookup must not run")

	recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID), "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}
	if len(fixture.resolver.nsCalls) != 0 || !slices.Equal(fixture.zones.deletedZones, []string{"cf-pending"}) ||
		fixture.mailZones.zones[zone.ID] != nil {
		t.Fatalf("ns lookups %v, deleted %v, stored %v", fixture.resolver.nsCalls, fixture.zones.deletedZones, fixture.mailZones.zones)
	}
}

func TestReceivingOnPendingZone409(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	pending, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "mail.example.org", domain.MailDomainSendOnly)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	recorder, body := fixture.do(http.MethodPost, fmt.Sprintf("/mail-domains/%d/receiving", pending.ID), "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZonePending {
		t.Fatalf("receiving = %d %v", recorder.Code, body)
	}
	if stored := fixture.domains.domains[pending.ID]; stored.Capability != domain.MailDomainSendOnly {
		t.Fatalf("capability = %s", stored.Capability)
	}
}

func TestReceivingUpgradesSendOnly(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	ctx := context.Background()
	sendOnly, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "news.example.com", domain.MailDomainSendOnly)
	if err != nil || sendOnly.Status != domain.MailDomainStatusVerified {
		t.Fatalf("send-only domain = %+v, %v", sendOnly, err)
	}
	mailbox, err := fixture.service.CreateMailbox(ctx, 7, testCreator, "hello", "", "news.example.com")
	if err != nil || mailbox.RoutingRuleID != "" {
		t.Fatalf("mailbox = %+v, %v", mailbox, err)
	}
	removed, err := fixture.service.CreateMailbox(ctx, 7, testCreator, "gone", "", "news.example.com")
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if err := fixture.service.DeleteMailbox(ctx, 7, removed.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	path := fmt.Sprintf("/mail-domains/%d/receiving", sendOnly.ID)

	recorder, body := fixture.do(http.MethodPost, path, "")
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["capability"] != "send_receive" || data["status"] != "verified" {
		t.Fatalf("receiving = %d %v", recorder.Code, body)
	}
	if !slices.Equal(fixture.zones.routingFor, []string{"news.example.com"}) {
		t.Fatalf("routing enabled for %v", fixture.zones.routingFor)
	}
	if stored := fixture.domains.domains[sendOnly.ID]; stored.Capability != domain.MailDomainSendReceive {
		t.Fatalf("stored capability = %s", stored.Capability)
	}
	// The mailbox created while the domain only sent now receives through its own rule.
	routed := fixture.repo.mailboxes[mailbox.ID]
	if routed.RoutingRuleID == "" || fixture.zones.rules[routed.RoutingRuleID] != "hello@news.example.com" || len(fixture.zones.rules) != 1 {
		t.Fatalf("mailbox rule = %q, rules = %v", routed.RoutingRuleID, fixture.zones.rules)
	}

	// Asking again changes nothing.
	again, body := fixture.do(http.MethodPost, path, "")
	if data, _ := body["data"].(map[string]any); again.Code != http.StatusOK || data["capability"] != "send_receive" {
		t.Fatalf("receiving again = %d %v", again.Code, body)
	}
	if len(fixture.zones.routingFor) != 1 || len(fixture.zones.rules) != 1 {
		t.Fatalf("routing %v, rules %v", fixture.zones.routingFor, fixture.zones.rules)
	}
	// A later setup does not add a second rule for the same mailbox.
	if _, err := fixture.service.SetUpMailDomain(ctx, 7, sendOnly.ID); err != nil {
		t.Fatalf("SetUpMailDomain: %v", err)
	}
	if len(fixture.zones.rules) != 1 {
		t.Fatalf("rules after a second setup = %v", fixture.zones.rules)
	}
}

func TestConnectAcceptsCapability(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))

	recorder, body := fixture.do(http.MethodPost, "/mail-domains", `{"name":"news.example.com","capability":"send_only"}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusCreated || data["capability"] != "send_only" || data["zone"] != nil ||
		data["zone_check_limited"] != false {
		t.Fatalf("connect = %d %v", recorder.Code, body)
	}
	if len(fixture.zones.routingFor) != 0 {
		t.Fatalf("routing enabled for a send-only domain: %v", fixture.zones.routingFor)
	}

	recorder, body = fixture.do(http.MethodPost, "/mail-domains", `{"name":"mail.example.org"}`)
	data, _ = body["data"].(map[string]any)
	zone, _ := data["zone"].(map[string]any)
	servers, _ := zone["name_servers"].([]any)
	if recorder.Code != http.StatusCreated || data["capability"] != "send_receive" || zone["name"] != "example.org" ||
		zone["status"] != "pending" || len(servers) != 2 {
		t.Fatalf("connect into a new zone = %d %v", recorder.Code, body)
	}

	invalid, body := fixture.do(http.MethodPost, "/mail-domains", `{"name":"other.example.com","capability":"receive_only"}`)
	if invalid.Code != http.StatusUnprocessableEntity || body["error_code"] != "COMMON.VALIDATION_FAILED" {
		t.Fatalf("unknown capability = %d %v", invalid.Code, body)
	}
}

func TestZoneRoutesDisabledWithoutFlag(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	fixture.service.settings.CustomDomainsEnabled = false
	for _, request := range [][3]string{
		{http.MethodGet, "/mail-zones", ""},
		{http.MethodGet, "/mail-zones/1", ""},
		{http.MethodDelete, "/mail-zones/1", ""},
		{http.MethodPost, "/mail-domain-previews", `{"name":"example.org"}`},
		{http.MethodPost, "/mail-domains/1/receiving", ""},
	} {
		recorder, body := fixture.do(request[0], request[1], request[2])
		if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailDomainsDisabled {
			t.Fatalf("%s %s = %d %v; want 404 %s", request[0], request[1], recorder.Code, body, domain.CodeMailDomainsDisabled)
		}
	}
}

func TestZoneWriteRequiresAdmin(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleMember))
	zone := fixture.activeZone("example.org", "cf-own")
	mailDomain, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "news.example.com", domain.MailDomainSendOnly)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	for _, request := range [][2]string{
		{http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID)},
		{http.MethodPost, fmt.Sprintf("/mail-domains/%d/receiving", mailDomain.ID)},
	} {
		recorder, body := fixture.do(request[0], request[1], "")
		if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
			t.Fatalf("member %s %s = %d %v; want 403", request[0], request[1], recorder.Code, body)
		}
	}
	if fixture.mailZones.zones[zone.ID] == nil || fixture.domains.domains[mailDomain.ID].Capability != domain.MailDomainSendOnly {
		t.Fatal("a member's request changed state")
	}
	// Members read zones and preview domains.
	if listed, body := fixture.do(http.MethodGet, "/mail-zones", ""); listed.Code != http.StatusOK {
		t.Fatalf("member list = %d %v", listed.Code, body)
	}
}
