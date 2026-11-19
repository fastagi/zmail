package mailtransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

type recordedCall struct {
	Method string
	Path   string
	Query  string
	Body   string
}

// fakeCloudflare answers each "METHOD path" with a canned result and records every call.
type fakeCloudflare struct {
	mu      sync.Mutex
	calls   []recordedCall
	answers map[string]string
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body)})
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-token" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`)
		return
	}
	key := r.Method + " " + r.URL.Path
	answer, ok := "", false
	for _, field := range []string{"name", "page"} {
		if query := r.URL.Query().Get(field); query != "" && !ok {
			answer, ok = f.answers[key+"?"+field+"="+query]
		}
	}
	if !ok {
		answer, ok = f.answers[key]
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":7003,"message":"Could not route to `+r.URL.Path+`"}]}`)
		return
	}
	// "!" answers with 400; "!NNN" answers with that HTTP status.
	if strings.HasPrefix(answer, "!") {
		status := http.StatusBadRequest
		if code, err := strconv.Atoi(answer[1:min(4, len(answer))]); err == nil {
			status = code
			answer = answer[4:]
		} else {
			answer = answer[1:]
		}
		w.WriteHeader(status)
	}
	_, _ = io.WriteString(w, answer)
}

func newDomainsServer(t *testing.T, answers map[string]string) (*CloudflareDomains, *fakeCloudflare) {
	t.Helper()
	fake := &fakeCloudflare{answers: answers}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := newCloudflareDomains(config.MailboxConfig{CloudflareAccountID: "acct", CloudflareAPIToken: "test-token"}, server.Client(), server.URL)
	return client, fake
}

func cfResult(result string) string {
	return `{"success":true,"errors":[],"result":` + result + `}`
}

func TestFindZoneWalksUpToTheClosestZone(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"GET /zones?name=mail.news.example.com": cfResult(`[]`),
		"GET /zones?name=news.example.com":      cfResult(`[]`),
		"GET /zones?name=example.com":           cfResult(`[{"id":"zone-1","name":"Example.com"}]`),
	})
	zone, err := client.FindZone(context.Background(), "mail.news.example.com")
	if err != nil || zone.ID != "zone-1" || zone.Name != "example.com" {
		t.Fatalf("zone = %+v, %v", zone, err)
	}
	if len(fake.calls) != 3 || !strings.Contains(fake.calls[0].Query, "account.id=acct") {
		t.Fatalf("calls = %+v", fake.calls)
	}
}

func TestFindZoneReportsAnUnreachableDomain(t *testing.T) {
	client, _ := newDomainsServer(t, map[string]string{
		"GET /zones?name=example.com": cfResult(`[]`),
	})
	if _, err := client.FindZone(context.Background(), "example.com"); !errors.Is(err, ErrZoneNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureSendingReusesAnExistingSendingDomain(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"GET /zones/zone-1/email/sending/subdomains": cfResult(`[{"id":"t-1","name":"news.example.com","enabled":true}]`),
	})
	tag, err := client.EnsureSending(context.Background(), "zone-1", "news.example.com")
	if err != nil || tag != "t-1" || len(fake.calls) != 1 {
		t.Fatalf("tag = %q, %v, calls = %+v", tag, err, fake.calls)
	}
}

func TestEnsureSendingCreatesTheSendingDomain(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"GET /zones/zone-1/email/sending/subdomains":  cfResult(`[{"tag":"t-0","name":"other.example.com","enabled":true}]`),
		"POST /zones/zone-1/email/sending/subdomains": cfResult(`{"tag":"t-2","name":"news.example.com","enabled":true}`),
	})
	tag, err := client.EnsureSending(context.Background(), "zone-1", "news.example.com")
	if err != nil || tag != "t-2" {
		t.Fatalf("tag = %q, %v", tag, err)
	}
	if fake.calls[1].Body != `{"name":"news.example.com"}` {
		t.Fatalf("create body = %s", fake.calls[1].Body)
	}
}

func TestSendingRecordsAreFullyQualified(t *testing.T) {
	client, _ := newDomainsServer(t, map[string]string{
		"GET /zones/zone-1/email/sending/subdomains/t-1/dns": cfResult(`[
			{"type":"TXT","name":"cf-bounce.news","content":"\"v=spf1 include:_spf.mx.cloudflare.net ~all\""},
			{"type":"MX","name":"cf-bounce.news.example.com.","content":"route1.mx.cloudflare.net","priority":7},
			{"type":"TXT","name":"@","content":"x"}]`),
		"GET /zones/zone-1": cfResult(`{"id":"zone-1","name":"example.com"}`),
	})
	records, err := client.SendingRecords(context.Background(), "zone-1", "t-1")
	if err != nil || len(records) != 3 {
		t.Fatalf("records = %+v, %v", records, err)
	}
	if records[0].Name != "cf-bounce.news.example.com" || records[1].Name != "cf-bounce.news.example.com" ||
		records[1].Priority != 7 || records[2].Name != "example.com" {
		t.Fatalf("records = %+v", records)
	}
}

func TestEnableRoutingNamesTheDomain(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"POST /zones/zone-1/email/routing/dns": cfResult(`{"id":"r","enabled":true,"name":"news.example.com","status":"ready"}`),
	})
	if err := client.EnableRouting(context.Background(), "zone-1", "news.example.com"); err != nil {
		t.Fatalf("EnableRouting: %v", err)
	}
	if fake.calls[0].Body != `{"name":"news.example.com"}` {
		t.Fatalf("body = %s", fake.calls[0].Body)
	}
}

func TestRoutingRulesDeliverOneAddressToTheWorker(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"POST /zones/zone-1/email/routing/rules":          cfResult(`{"id":"rule-9"}`),
		"DELETE /zones/zone-1/email/routing/rules/rule-9": cfResult(`{"id":"rule-9"}`),
	})
	id, err := client.CreateRoutingRule(context.Background(), "zone-1", "hello@news.example.com", "zmail-email-worker")
	if err != nil || id != "rule-9" {
		t.Fatalf("rule = %q, %v", id, err)
	}
	var rule cloudflareRule
	if err := json.Unmarshal([]byte(fake.calls[0].Body), &rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rule.Enabled || len(rule.Matchers) != 1 || rule.Matchers[0] != (cloudflareMatcher{Type: "literal", Field: "to", Value: "hello@news.example.com"}) ||
		len(rule.Actions) != 1 || rule.Actions[0].Type != "worker" || rule.Actions[0].Value[0] != "zmail-email-worker" {
		t.Fatalf("rule = %+v", rule)
	}
	if err := client.DeleteRoutingRule(context.Background(), "zone-1", "rule-9"); err != nil {
		t.Fatalf("DeleteRoutingRule: %v", err)
	}
}

func TestCreateRecordWritesAnAutomaticTTL(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"POST /zones/zone-1/dns_records": cfResult(`{"id":"rec"}`),
	})
	_, err := client.CreateRecord(context.Background(), "zone-1", DNSRecord{Type: "TXT", Name: "_dmarc.news.example.com", Content: "v=DMARC1; p=none"})
	if err != nil || fake.calls[0].Body != `{"type":"TXT","name":"_dmarc.news.example.com","content":"v=DMARC1; p=none","ttl":1}` {
		t.Fatalf("err = %v, body = %s", err, fake.calls[0].Body)
	}
}

func TestProviderErrorsCarryTheProviderMessage(t *testing.T) {
	client, _ := newDomainsServer(t, map[string]string{
		"POST /zones/zone-1/email/routing/dns": `!{"success":false,"errors":[{"code":2007,"message":"Invalid domain"}]}`,
	})
	err := client.EnableRouting(context.Background(), "zone-1", "news.example.com")
	var failure *DomainError
	if !errors.As(err, &failure) || failure.Status != http.StatusBadRequest || failure.Code != 2007 || failure.Message != "Invalid domain" {
		t.Fatalf("err = %#v", err)
	}
	wrong := newCloudflareDomains(config.MailboxConfig{CloudflareAPIToken: "wrong"}, client.client, client.baseURL)
	if err := wrong.EnableRouting(context.Background(), "zone-1", "x.example.com"); !errors.As(err, &failure) || failure.Message != "Authentication error" {
		t.Fatalf("auth err = %v", err)
	}
}

func TestDomainProviderNeedsCloudflareCredentials(t *testing.T) {
	cfg := &config.Config{}
	cfg.Mailbox.Provider = config.MailboxProviderLog
	if _, err := NewDomainProvider(cfg).FindZone(context.Background(), "example.com"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("log provider = %v", err)
	}
	cfg.Mailbox.Provider = config.MailboxProviderCloudflare
	cfg.Mailbox.CloudflareAccountID = "acct"
	cfg.Mailbox.CloudflareAPIToken = "token"
	if _, ok := NewDomainProvider(cfg).(*CloudflareDomains); !ok {
		t.Fatal("cloudflare provider was not built")
	}
}

func TestCreateZoneSendsFullType(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"POST /zones": cfResult(`{"id":"zone-7","name":"example.org","status":"pending",
			"name_servers":["ada.ns.cloudflare.com","bob.ns.cloudflare.com"],
			"original_name_servers":["ns1.registrar.example","ns2.registrar.example"]}`),
	})
	zone, err := client.CreateZone(context.Background(), "example.org")
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	var body struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := json.Unmarshal([]byte(fake.calls[0].Body), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Name != "example.org" || body.Type != "full" || body.Account.ID != "acct" ||
		!strings.Contains(fake.calls[0].Body, `"type":"full"`) {
		t.Fatalf("body = %s", fake.calls[0].Body)
	}
	if zone.ID != "zone-7" || zone.Name != "example.org" || zone.Status != "pending" ||
		strings.Join(zone.NameServers, ",") != "ada.ns.cloudflare.com,bob.ns.cloudflare.com" ||
		strings.Join(zone.OriginalNameServers, ",") != "ns1.registrar.example,ns2.registrar.example" {
		t.Fatalf("zone = %+v", zone)
	}
}

func TestCreateZoneExists(t *testing.T) {
	client, _ := newDomainsServer(t, map[string]string{
		"POST /zones": `!{"success":false,"errors":[{"code":1061,"message":"example.org already exists"}]}`,
	})
	_, err := client.CreateZone(context.Background(), "example.org")
	var failure *DomainError
	if !errors.Is(err, ErrZoneExists) || !errors.As(err, &failure) || failure.Code != 1061 {
		t.Fatalf("err = %v", err)
	}
}

func TestGetZoneReadsStatusAndNameServers(t *testing.T) {
	client, _ := newDomainsServer(t, map[string]string{
		"GET /zones/zone-7": cfResult(`{"id":"zone-7","name":"example.org","status":"active",
			"name_servers":["ada.ns.cloudflare.com"],"original_name_servers":null}`),
	})
	zone, err := client.GetZone(context.Background(), "zone-7")
	if err != nil || zone.Status != "active" || len(zone.NameServers) != 1 || len(zone.OriginalNameServers) != 0 {
		t.Fatalf("zone = %+v, %v", zone, err)
	}
}

func TestActivationCheckLimited(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"PUT /zones/zone-7/activation_check": `!429{"success":false,"errors":[{"code":10000,"message":"rate limited"}]}`,
		"PUT /zones/zone-8/activation_check": `!{"success":false,"errors":[{"code":1224,"message":"You may only perform this action once per hour."}]}`,
		"PUT /zones/zone-9/activation_check": cfResult(`{"id":"zone-9"}`),
	})
	if err := client.RequestActivationCheck(context.Background(), "zone-7"); !errors.Is(err, ErrActivationCheckLimited) {
		t.Fatalf("429 err = %v", err)
	}
	if err := client.RequestActivationCheck(context.Background(), "zone-8"); !errors.Is(err, ErrActivationCheckLimited) {
		t.Fatalf("1224 err = %v", err)
	}
	if err := client.RequestActivationCheck(context.Background(), "zone-9"); err != nil {
		t.Fatalf("RequestActivationCheck: %v", err)
	}
	if fake.calls[0].Method != http.MethodPut {
		t.Fatalf("calls = %+v", fake.calls)
	}
}

func TestDeleteZoneAndScanRecords(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"POST /zones/zone-7/dns_records/scan": cfResult(`{"recs_added":3}`),
		"DELETE /zones/zone-7":                cfResult(`{"id":"zone-7"}`),
	})
	if err := client.ScanRecords(context.Background(), "zone-7"); err != nil {
		t.Fatalf("ScanRecords: %v", err)
	}
	if err := client.DeleteZone(context.Background(), "zone-7"); err != nil {
		t.Fatalf("DeleteZone: %v", err)
	}
	var failure *DomainError
	if err := client.DeleteZone(context.Background(), "zone-gone"); !errors.As(err, &failure) || failure.Status != http.StatusNotFound {
		t.Fatalf("missing zone err = %v", err)
	}
	if len(fake.calls) != 3 {
		t.Fatalf("calls = %+v", fake.calls)
	}
}

func recordPage(from, to, totalPages int) string {
	rows := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		rows = append(rows, `{"id":"rec-`+strconv.Itoa(i)+`","type":"A","name":"h`+strconv.Itoa(i)+`.example.org.","content":"192.0.2.1","ttl":300,"proxied":true}`)
	}
	return `{"success":true,"errors":[],"result":[` + strings.Join(rows, ",") +
		`],"result_info":{"page":1,"per_page":100,"total_pages":` + strconv.Itoa(totalPages) + `}}`
}

func TestListZoneRecordsPaginates(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"GET /zones/zone-7/dns_records?page=1": recordPage(0, 100, 2),
		"GET /zones/zone-7/dns_records?page=2": recordPage(100, 150, 2),
	})
	records, err := client.ListZoneRecords(context.Background(), "zone-7")
	if err != nil || len(records) != 150 {
		t.Fatalf("records = %d, %v", len(records), err)
	}
	if records[149] != (DNSRecord{ID: "rec-149", Type: "A", Name: "h149.example.org", Content: "192.0.2.1", TTL: 300, Proxied: true}) {
		t.Fatalf("last record = %+v", records[149])
	}
	if len(fake.calls) != 2 || !strings.Contains(fake.calls[0].Query, "per_page=100") || !strings.Contains(fake.calls[1].Query, "page=2") {
		t.Fatalf("calls = %+v", fake.calls)
	}
}

func TestUpdateRecordPatchesByID(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"PATCH /zones/zone-7/dns_records/rec-1": cfResult(`{"id":"rec-1"}`),
	})
	err := client.UpdateRecord(context.Background(), "zone-7", DNSRecord{
		ID: "rec-1", Type: "MX", Name: "example.org", Content: "route1.mx.cloudflare.net", Priority: 13, TTL: 300,
	})
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	want := `{"type":"MX","name":"example.org","content":"route1.mx.cloudflare.net","priority":13,"ttl":300,"proxied":false}`
	if fake.calls[0].Method != http.MethodPatch || fake.calls[0].Body != want {
		t.Fatalf("call = %+v", fake.calls[0])
	}
}

func TestDeleteRecord(t *testing.T) {
	client, fake := newDomainsServer(t, map[string]string{
		"DELETE /zones/zone-7/dns_records/rec-1": cfResult(`{"id":"rec-1"}`),
	})
	if err := client.DeleteRecord(context.Background(), "zone-7", "rec-1"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if fake.calls[0].Method != http.MethodDelete || fake.calls[0].Path != "/zones/zone-7/dns_records/rec-1" {
		t.Fatalf("calls = %+v", fake.calls)
	}
}

func TestCreateRecordReturnsID(t *testing.T) {
	client, _ := newDomainsServer(t, map[string]string{
		"POST /zones/zone-1/dns_records": cfResult(`{"id":"rec-5","type":"TXT","name":"_dmarc.example.com","content":"v=DMARC1; p=none","ttl":1,"proxied":false}`),
	})
	record, err := client.CreateRecord(context.Background(), "zone-1", DNSRecord{Type: "TXT", Name: "_dmarc.example.com", Content: "v=DMARC1; p=none"})
	if err != nil || record.ID != "rec-5" || record.Type != "TXT" || record.TTL != 1 {
		t.Fatalf("record = %+v, %v", record, err)
	}
}

func TestUnconfiguredDomainsRejectZoneManagement(t *testing.T) {
	provider := unconfiguredDomains{}
	if _, err := provider.CreateZone(context.Background(), "example.org"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("CreateZone = %v", err)
	}
	if _, err := provider.ListZoneRecords(context.Background(), "zone-7"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ListZoneRecords = %v", err)
	}
}
