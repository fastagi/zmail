package mailtransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	if query := r.URL.Query().Get("name"); query != "" {
		if answer, ok := f.answers[key+"?name="+query]; ok {
			_, _ = io.WriteString(w, answer)
			return
		}
	}
	answer, ok := f.answers[key]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":7003,"message":"Could not route to `+r.URL.Path+`"}]}`)
		return
	}
	if strings.HasPrefix(answer, "!") {
		w.WriteHeader(http.StatusBadRequest)
		answer = answer[1:]
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
	err := client.CreateRecord(context.Background(), "zone-1", DNSRecord{Type: "TXT", Name: "_dmarc.news.example.com", Content: "v=DMARC1; p=none"})
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
