package mailbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// fakeDNSProvider keeps the records of one zone by provider ID, so the record editor can list,
// create, rewrite, and remove them. writeErr, when set, fails every write the way Cloudflare
// refuses one.
type fakeDNSProvider struct {
	*fakeZones
	byID     map[string]mailtransport.DNSRecord
	order    []string
	nextID   int
	writeErr error
	writes   int
}

func newFakeDNSProvider(zones *fakeZones) *fakeDNSProvider {
	return &fakeDNSProvider{fakeZones: zones, byID: map[string]mailtransport.DNSRecord{}}
}

func (p *fakeDNSProvider) put(record mailtransport.DNSRecord) {
	if _, ok := p.byID[record.ID]; !ok {
		p.order = append(p.order, record.ID)
	}
	p.byID[record.ID] = record
}

func (p *fakeDNSProvider) ListZoneRecords(context.Context, string) ([]mailtransport.DNSRecord, error) {
	records := make([]mailtransport.DNSRecord, 0, len(p.order))
	for _, id := range p.order {
		if record, ok := p.byID[id]; ok {
			records = append(records, record)
		}
	}
	return records, nil
}

func (p *fakeDNSProvider) CreateRecord(_ context.Context, _ string, record mailtransport.DNSRecord) (mailtransport.DNSRecord, error) {
	p.writes++
	if p.writeErr != nil {
		return mailtransport.DNSRecord{}, p.writeErr
	}
	p.nextID++
	record.ID = fmt.Sprintf("new%d", p.nextID)
	p.put(record)
	return record, nil
}

func (p *fakeDNSProvider) UpdateRecord(_ context.Context, _ string, record mailtransport.DNSRecord) error {
	p.writes++
	if p.writeErr != nil {
		return p.writeErr
	}
	p.put(record)
	return nil
}

func (p *fakeDNSProvider) DeleteRecord(_ context.Context, _, recordID string) error {
	p.writes++
	if p.writeErr != nil {
		return p.writeErr
	}
	delete(p.byID, recordID)
	return nil
}

// dnsFixture serves the record editor over an active managed zone example.com of organization 7.
// The zone holds two mail domains, example.com and news.example.com, the records Zmail set up for
// them, and two records of the organization's own.
type dnsFixture struct {
	*zoneHandlerFixture
	dns     *fakeDNSProvider
	zone    *domain.MailZone
	audited []auditstarter.Change
}

func newDNSFixture(t *testing.T, role domain.OrganizationRole) *dnsFixture {
	t.Helper()
	base := newZoneHandlerFixture(t, membership(role))
	fixture := &dnsFixture{zoneHandlerFixture: base, dns: newFakeDNSProvider(base.zones)}
	base.service.domainProvider = fixture.dns
	base.service.recordAudit = func(_ context.Context, change auditstarter.Change) {
		fixture.audited = append(fixture.audited, change)
	}
	fixture.zone = base.activeZone("example.com", "cf-zone")
	zoneID := fixture.zone.ID
	base.domains.domains[1] = &domain.MailDomain{
		ID: 1, OrganizationID: 7, Name: "example.com", MailZoneID: &zoneID,
		Capability: domain.MailDomainSendReceive, Status: domain.MailDomainStatusVerified,
	}
	base.domains.domains[2] = &domain.MailDomain{
		ID: 2, OrganizationID: 7, Name: "news.example.com", MailZoneID: &zoneID,
		Capability: domain.MailDomainSendOnly, Status: domain.MailDomainStatusVerified,
		Records: []domain.MailDomainRecord{
			{Purpose: domain.MailDomainRecordSending, Type: "MX", Name: "cf-bounce.news.example.com", Content: "route1.mx.cloudflare.net", Priority: 5},
		},
	}
	base.domains.nextID = 2
	for _, record := range []mailtransport.DNSRecord{
		{ID: "recmx", Type: "MX", Name: "example.com", Content: "route1.mx.cloudflare.net", Priority: 10, TTL: 1},
		{ID: "recspf", Type: "TXT", Name: "example.com", Content: `"v=spf1 include:_spf.mx.cloudflare.net ~all"`, TTL: 1},
		{ID: "recdmarc", Type: "TXT", Name: "_dmarc.example.com", Content: "v=DMARC1; p=none", TTL: 1},
		{ID: "recdkim", Type: "TXT", Name: "cf1._domainkey.news.example.com", Content: "v=DKIM1; k=rsa; p=MIIB", TTL: 1},
		{ID: "recbounce", Type: "MX", Name: "cf-bounce.news.example.com", Content: "route1.mx.cloudflare.net.", Priority: 5, TTL: 1},
		{ID: "recwww", Type: "A", Name: "www.example.com", Content: "192.0.2.1", TTL: 300, Proxied: true},
		{ID: "recnote", Type: "TXT", Name: "notes.example.com", Content: "secret-site-verification=abc123", TTL: 1},
	} {
		fixture.dns.put(record)
	}
	handler := NewHandler(base.service)
	base.router.GET("/mail-zones/:zone_id/dns-records", handler.ListMailDNSRecords)
	base.router.POST("/mail-zones/:zone_id/dns-records", handler.CreateMailDNSRecord)
	base.router.PATCH("/mail-zones/:zone_id/dns-records/:record_id", handler.UpdateMailDNSRecord)
	base.router.DELETE("/mail-zones/:zone_id/dns-records/:record_id", handler.DeleteMailDNSRecord)
	return fixture
}

func (f *dnsFixture) recordsPath() string {
	return fmt.Sprintf("/mail-zones/%d/dns-records", f.zone.ID)
}

func (f *dnsFixture) recordPath(id string) string {
	return fmt.Sprintf("/mail-zones/%d/dns-records/%s", f.zone.ID, id)
}

func TestListMarksMailRecordsLocked(t *testing.T) {
	fixture := newDNSFixture(t, domain.OrganizationRoleMember)

	recorder, body := fixture.do(http.MethodGet, fixture.recordsPath(), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list = %d %v", recorder.Code, body)
	}
	items, _ := body["data"].([]any)
	if len(items) != 7 {
		t.Fatalf("records = %v", body["data"])
	}
	want := map[string]string{
		"recmx": "mail_receiving", "recspf": "mail_spf", "recdmarc": "mail_dmarc", "recdkim": "mail_dkim",
		"recbounce": "mail_sending", "recwww": "", "recnote": "",
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		id := item["id"].(string)
		reason, expected := want[id]
		if !expected {
			t.Fatalf("unexpected record %v", item)
		}
		if item["locked"] != (reason != "") {
			t.Fatalf("%s locked = %v", id, item["locked"])
		}
		got, present := item["locked_reason"]
		if reason == "" && present {
			t.Fatalf("%s carries a lock reason: %v", id, item)
		}
		if reason != "" && got != reason {
			t.Fatalf("%s locked_reason = %v, want %s", id, got, reason)
		}
	}
	www := findRecordItem(items, "recwww")
	if www["type"] != "A" || www["name"] != "www.example.com" || www["content"] != "192.0.2.1" ||
		www["ttl"] != float64(300) || www["proxied"] != true {
		t.Fatalf("www = %v", www)
	}
	if _, present := www["priority"]; present {
		t.Fatalf("an A record carries a priority: %v", www)
	}
	if mx := findRecordItem(items, "recmx"); mx["priority"] != float64(10) {
		t.Fatalf("mx = %v", mx)
	}
}

func findRecordItem(items []any, id string) map[string]any {
	for _, raw := range items {
		if item := raw.(map[string]any); item["id"] == id {
			return item
		}
	}
	return nil
}

func TestDNSRecordLockAppliesToTarget(t *testing.T) {
	fixture := newDNSFixture(t, domain.OrganizationRoleAdmin)

	renamed, body := fixture.do(http.MethodPatch, fixture.recordPath("recnote"), `{"name":"_dmarc.example.com"}`)
	if renamed.Code != http.StatusConflict || body["error_code"] != domain.CodeMailDNSRecordLocked {
		t.Fatalf("rename onto DMARC = %d %v", renamed.Code, body)
	}
	if fixture.dns.byID["recnote"].Name != "notes.example.com" {
		t.Fatalf("the record changed: %v", fixture.dns.byID["recnote"])
	}

	created, body := fixture.do(http.MethodPost, fixture.recordsPath(),
		`{"type":"MX","name":"@","content":"mx.other.example","priority":20}`)
	if created.Code != http.StatusConflict || body["error_code"] != domain.CodeMailDNSRecordLocked {
		t.Fatalf("MX at the mail domain = %d %v", created.Code, body)
	}

	edited, body := fixture.do(http.MethodPatch, fixture.recordPath("recspf"), `{"content":"v=spf1 -all"}`)
	if edited.Code != http.StatusConflict || body["error_code"] != domain.CodeMailDNSRecordLocked {
		t.Fatalf("editing SPF = %d %v", edited.Code, body)
	}
	if fixture.dns.writes != 0 {
		t.Fatalf("a locked change reached the provider: %d writes", fixture.dns.writes)
	}
}

func TestDeleteLockedRecord409(t *testing.T) {
	fixture := newDNSFixture(t, domain.OrganizationRoleOwner)

	locked, body := fixture.do(http.MethodDelete, fixture.recordPath("recmx"), "")
	if locked.Code != http.StatusConflict || body["error_code"] != domain.CodeMailDNSRecordLocked {
		t.Fatalf("delete MX = %d %v", locked.Code, body)
	}
	if _, ok := fixture.dns.byID["recmx"]; !ok {
		t.Fatalf("the locked record was removed")
	}

	removed, body := fixture.do(http.MethodDelete, fixture.recordPath("recwww"), "")
	if removed.Code != http.StatusNoContent {
		t.Fatalf("delete www = %d %v", removed.Code, body)
	}
	if _, ok := fixture.dns.byID["recwww"]; ok {
		t.Fatalf("the record is still there")
	}

	fixture.dns.put(mailtransport.DNSRecord{ID: "reccaa", Type: "CAA", Name: "example.com", Content: `0 issue "ca.example"`, TTL: 1})
	unsupported, body := fixture.do(http.MethodDelete, fixture.recordPath("reccaa"), "")
	if unsupported.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailDNSRecordInvalid {
		t.Fatalf("delete CAA = %d %v", unsupported.Code, body)
	}

	missing, body := fixture.do(http.MethodDelete, fixture.recordPath("recwww"), "")
	if missing.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailDNSRecordNotFound {
		t.Fatalf("delete again = %d %v", missing.Code, body)
	}
}

func TestDNSRecordValidation(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"bad IPv4", `{"type":"A","name":"api","content":"999.1.1.1"}`, "content"},
		{"IPv6 in an A record", `{"type":"A","name":"api","content":"2001:db8::1"}`, "content"},
		{"name outside the zone", `{"type":"A","name":"www.other.org","content":"192.0.2.7"}`, "name"},
		{"CNAME at the apex", `{"type":"CNAME","name":"@","content":"target.example.net"}`, "name"},
		{"CNAME beside an A record", `{"type":"CNAME","name":"www","content":"target.example.net"}`, "name"},
		{"TXT of 2049 characters", `{"type":"TXT","name":"long","content":"` + strings.Repeat("a", 2049) + `"}`, "content"},
		{"MX without priority", `{"type":"MX","name":"sub","content":"mx.example.net"}`, "priority"},
		{"proxied TXT", `{"type":"TXT","name":"proxy","content":"hello","proxied":true}`, "proxied"},
		{"TTL 30", `{"type":"A","name":"api","content":"192.0.2.7","ttl":30}`, "ttl"},
		{"unknown type", `{"type":"SRV","name":"api","content":"1 1 1 a.example.com"}`, "type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newDNSFixture(t, domain.OrganizationRoleAdmin)
			recorder, body := fixture.do(http.MethodPost, fixture.recordsPath(), tc.body)
			if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailDNSRecordInvalid {
				t.Fatalf("create = %d %v", recorder.Code, body)
			}
			fields, _ := body["errors"].(map[string]any)
			if _, ok := fields[tc.field]; !ok {
				t.Fatalf("errors = %v, want field %s", body["errors"], tc.field)
			}
			if fixture.dns.writes != 0 {
				t.Fatalf("an invalid record reached the provider")
			}
		})
	}

	t.Run("merged update", func(t *testing.T) {
		fixture := newDNSFixture(t, domain.OrganizationRoleAdmin)
		recorder, body := fixture.do(http.MethodPatch, fixture.recordPath("recwww"), `{"type":"TXT"}`)
		if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailDNSRecordInvalid {
			t.Fatalf("update = %d %v", recorder.Code, body)
		}
		fields, _ := body["errors"].(map[string]any)
		if _, ok := fields["proxied"]; !ok {
			t.Fatalf("errors = %v, want proxied: the record stays proxied", body["errors"])
		}
	})
}

func TestRelativeNameQualified(t *testing.T) {
	fixture := newDNSFixture(t, domain.OrganizationRoleAdmin)

	relative, body := fixture.do(http.MethodPost, fixture.recordsPath(), `{"type":"A","name":"WWW2","content":"192.0.2.8"}`)
	if relative.Code != http.StatusCreated {
		t.Fatalf("create www2 = %d %v", relative.Code, body)
	}
	data := body["data"].(map[string]any)
	if data["name"] != "www2.example.com" || data["ttl"] != float64(1) || data["locked"] != false {
		t.Fatalf("created = %v", data)
	}
	if stored := fixture.dns.byID[data["id"].(string)]; stored.Name != "www2.example.com" {
		t.Fatalf("stored = %v", stored)
	}

	apex, body := fixture.do(http.MethodPost, fixture.recordsPath(),
		`{"type":"TXT","name":"@","content":"site-verification=xyz","ttl":3600}`)
	if apex.Code != http.StatusCreated {
		t.Fatalf("create @ = %d %v", apex.Code, body)
	}
	if data := body["data"].(map[string]any); data["name"] != "example.com" || data["ttl"] != float64(3600) {
		t.Fatalf("created = %v", data)
	}
}

func TestDNSRecordPatchMergesOntoCurrent(t *testing.T) {
	fixture := newDNSFixture(t, domain.OrganizationRoleAdmin)

	recorder, body := fixture.do(http.MethodPatch, fixture.recordPath("recwww"), `{"content":"192.0.2.9"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update = %d %v", recorder.Code, body)
	}
	want := mailtransport.DNSRecord{ID: "recwww", Type: "A", Name: "www.example.com", Content: "192.0.2.9", TTL: 300, Proxied: true}
	if got := fixture.dns.byID["recwww"]; got != want {
		t.Fatalf("stored = %+v, want %+v", got, want)
	}

	recorder, body = fixture.do(http.MethodPatch, fixture.recordPath("missing"), `{"content":"192.0.2.9"}`)
	if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailDNSRecordNotFound {
		t.Fatalf("update a missing record = %d %v", recorder.Code, body)
	}
}

func TestDNSRecordsOnPendingZone(t *testing.T) {
	fixture := newDNSFixture(t, domain.OrganizationRoleAdmin)
	fixture.mailZones.zones[fixture.zone.ID].Status = domain.MailZoneStatusPending

	recorder, body := fixture.do(http.MethodPost, fixture.recordsPath(), `{"type":"A","name":"app","content":"192.0.2.10"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create on a pending zone = %d %v", recorder.Code, body)
	}
}

func TestProviderRejectionMapped(t *testing.T) {
	fixture := newDNSFixture(t, domain.OrganizationRoleAdmin)
	fixture.dns.writeErr = &mailtransport.DomainError{
		Operation: "create record", Status: http.StatusBadRequest, Code: 81057, Message: "An identical record already exists.",
	}

	recorder, body := fixture.do(http.MethodPost, fixture.recordsPath(), `{"type":"A","name":"api","content":"192.0.2.7"}`)
	if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailDNSRecordRejected {
		t.Fatalf("create = %d %v", recorder.Code, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "An identical record already exists.") {
		t.Fatalf("the provider's reason is missing: %v", body)
	}
	if len(fixture.audited) != 0 {
		t.Fatalf("a refused write was audited: %v", fixture.audited)
	}
}

func TestDNSWriteAudited(t *testing.T) {
	fixture := newDNSFixture(t, domain.OrganizationRoleAdmin)

	created, body := fixture.do(http.MethodPost, fixture.recordsPath(), `{"type":"TXT","name":"audit","content":"private-token-1"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %v", created.Code, body)
	}
	id := body["data"].(map[string]any)["id"].(string)
	if updated, body := fixture.do(http.MethodPatch, fixture.recordPath(id), `{"content":"private-token-2"}`); updated.Code != http.StatusOK {
		t.Fatalf("update = %d %v", updated.Code, body)
	}
	if removed, body := fixture.do(http.MethodDelete, fixture.recordPath(id), ""); removed.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %v", removed.Code, body)
	}

	if len(fixture.audited) != 3 {
		t.Fatalf("audited = %v", fixture.audited)
	}
	for index, action := range []string{"create", "update", "delete"} {
		change := fixture.audited[index]
		if change.Action != action || change.Resource != "mail_dns_records" || change.TargetType != "mail_dns_record" ||
			change.TargetID != id || change.Result != domain.AuditResultSuccess {
			t.Fatalf("change %d = %+v", index, change)
		}
		for _, field := range []string{"zone", "type", "name"} {
			if _, ok := change.Changes[field]; !ok {
				t.Fatalf("change %d lacks %s: %+v", index, field, change.Changes)
			}
		}
		if _, ok := change.Changes["content"]; ok {
			t.Fatalf("change %d records content: %+v", index, change.Changes)
		}
		if printed := fmt.Sprintf("%+v", change); strings.Contains(printed, "private-token") {
			t.Fatalf("change %d leaks the content: %s", index, printed)
		}
	}
	if zone := fixture.audited[0].Changes["zone"].After; zone != "example.com" {
		t.Fatalf("zone = %v", zone)
	}
	if name := fixture.audited[0].Changes["name"].After; name != "audit.example.com" {
		t.Fatalf("name = %v", name)
	}
}

func TestDNSRecordsMemberReadOnly(t *testing.T) {
	fixture := newDNSFixture(t, domain.OrganizationRoleMember)

	for _, request := range []struct{ method, path, body string }{
		{http.MethodPost, fixture.recordsPath(), `{"type":"A","name":"api","content":"192.0.2.7"}`},
		{http.MethodPatch, fixture.recordPath("recwww"), `{"content":"192.0.2.9"}`},
		{http.MethodDelete, fixture.recordPath("recwww"), ""},
	} {
		recorder, body := fixture.do(request.method, request.path, request.body)
		if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
			t.Fatalf("%s %s = %d %v", request.method, request.path, recorder.Code, body)
		}
	}
	if fixture.dns.writes != 0 {
		t.Fatalf("a member's write reached the provider")
	}

	other := fixture.mailZones.store(domain.MailZone{
		OrganizationID: 8, Name: "other.test", CloudflareZoneID: "cf-other", Status: domain.MailZoneStatusActive,
	})
	foreign, body := fixture.do(http.MethodGet, fmt.Sprintf("/mail-zones/%d/dns-records", other.ID), "")
	if foreign.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailZoneNotFound {
		t.Fatalf("another organization's zone = %d %v", foreign.Code, body)
	}
}

func TestLockReason(t *testing.T) {
	domains := []*domain.MailDomain{{Name: "example.com"}}
	cases := []struct {
		record mailtransport.DNSRecord
		want   string
	}{
		{mailtransport.DNSRecord{Type: "MX", Name: "Example.com.", Content: "mx.other.example"}, "mail_receiving"},
		{mailtransport.DNSRecord{Type: "TXT", Name: "example.com", Content: "V=SPF1 -all"}, "mail_spf"},
		{mailtransport.DNSRecord{Type: "TXT", Name: "example.com", Content: "verification=1"}, ""},
		{mailtransport.DNSRecord{Type: "CNAME", Name: "_dmarc.example.com", Content: "dmarc.example.net"}, "mail_dmarc"},
		{mailtransport.DNSRecord{Type: "TXT", Name: "s1._domainkey.example.com", Content: "v=DKIM1"}, "mail_dkim"},
		{mailtransport.DNSRecord{Type: "MX", Name: "sub.example.com", Content: "mx.other.example"}, ""},
	}
	for _, tc := range cases {
		if got := lockReason(tc.record, domains); got != tc.want {
			t.Fatalf("lockReason(%+v) = %q, want %q", tc.record, got, tc.want)
		}
	}
}
