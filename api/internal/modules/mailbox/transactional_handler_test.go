package mailbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/pkg/response"
)

// txHandlerFixture serves the starter's real routes. Its api_key middleware stands in for the
// apikey starter's: X-API-Key "live", "test", or "noscope" picks a key of organization 10's
// member, with the context values the real middleware sets.
type txHandlerFixture struct {
	*txFixture
	engine *gin.Engine
	// role is the caller's organization role; member unless a test changes it.
	role domain.OrganizationRole
}

func newTxHandlerFixture(t *testing.T, edit func(*Settings)) *txHandlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &txHandlerFixture{txFixture: newTxFixture(t, edit), role: domain.OrganizationRoleMember}
	handler := NewHandler(f.service)
	response.DefaultErrorMapper.Register(domain.ErrInvalidInput, http.StatusBadRequest, domain.CodeInvalidInput)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)

	f.engine = gin.New()
	routes := router.New(f.engine).Prefix("/v1")
	keys := map[string]struct {
		id     uint
		mode   domain.APIKeyMode
		scopes []string
	}{
		"live":    {31, domain.APIKeyModeLive, []string{ScopeMailTransactional}},
		"test":    {32, domain.APIKeyModeTest, []string{"mail:send", ScopeMailTransactional}},
		"noscope": {33, domain.APIKeyModeLive, []string{ScopeMailSend, ScopeMailRead}},
	}
	routes.MiddlewareGroup("api_key", func(c *gin.Context) {
		key, ok := keys[c.GetHeader("X-API-Key")]
		if !ok {
			response.AbortUnauthorized(c, "Invalid or missing API key")
			return
		}
		c.Set("apiKeyID", key.id)
		c.Set("apiKeyScopes", key.scopes)
		c.Set("apiKeyMode", key.mode)
		c.Set("userID", uint(txUser))
		c.Next()
	})
	routes.MiddlewareGroup("auth", func(c *gin.Context) {
		c.Set("userID", uint(txUser))
		c.Next()
	})
	routes.MiddlewareGroup("organization_context", func(c *gin.Context) {
		viewer := viewerOf(10, txUser, f.role)
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), viewer))
		c.Next()
	})
	handler.RegisterRoutes(routes)
	return f
}

func (f *txHandlerFixture) request(method, path, key, idempotencyKey, body string) (*httptest.ResponseRecorder, map[string]any) {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("X-API-Key", key)
	}
	if idempotencyKey != "" {
		request.Header.Set(IdempotencyKeyHeader, idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	f.engine.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder, decoded
}

const codeBody = `{"template":"signup-code","to":" LiLei@Example.COM ","variables":{"code":"482913","app":"Acme"}}`

func TestTransactionalSendRoute(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	created, body := f.request(http.MethodPost, "/v1/transactional-messages", "live", "signup-u_8812", codeBody)
	if created.Code != http.StatusAccepted {
		t.Fatalf("send = %d %s", created.Code, created.Body.String())
	}
	data, _ := body["data"].(map[string]any)
	id := uint(data["id"].(float64))
	if location := created.Header().Get("Location"); location != "/v1/transactional-messages/"+strconv.Itoa(int(id)) {
		t.Fatalf("Location = %q", location)
	}
	if created.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", created.Header().Get("Cache-Control"))
	}
	want := map[string]any{
		"mode": "live", "simulated": false, "template": "signup-code", "to": "lilei@example.com",
		"to_masked": "li••@example.com", "subject": "Acme verification code: ••••••", "status": "queued",
		"queued_reason": nil, "failure_code": nil, "last_event": nil, "attempts": float64(0),
		"idempotency_key": "signup-u_8812", "sent_at": nil,
	}
	for field, value := range want {
		if data[field] != value {
			t.Errorf("%s = %#v; want %#v", field, data[field], value)
		}
	}
	if from, _ := data["from"].(map[string]any); from["address"] != "no-reply@mail.example" || from["name"] != "Acme" {
		t.Errorf("from = %v", data["from"])
	}
	if variables, _ := data["variables"].(map[string]any); variables["code"] != "••••••" || variables["app"] != "Acme" {
		t.Errorf("variables = %v", data["variables"])
	}
	if events, ok := data["events"].([]any); !ok || len(events) != 0 {
		t.Errorf("events = %#v; want an empty list", data["events"])
	}
	if strings.Contains(created.Body.String(), "482913") {
		t.Fatal("the response holds the secret")
	}

	replay, body := f.request(http.MethodPost, "/v1/transactional-messages", "live", "signup-u_8812", codeBody)
	if replayData, _ := body["data"].(map[string]any); replay.Code != http.StatusOK || replayData["id"] != data["id"] ||
		replay.Header().Get("Location") != created.Header().Get("Location") {
		t.Fatalf("replay = %d %s", replay.Code, replay.Body.String())
	}
	conflict, body := f.request(http.MethodPost, "/v1/transactional-messages", "live", "signup-u_8812",
		strings.Replace(codeBody, "482913", "000000", 1))
	if conflict.Code != http.StatusConflict || body["error_code"] != domain.CodeMailIdempotencyConflict {
		t.Fatalf("conflict = %d %s", conflict.Code, conflict.Body.String())
	}

	read, body := f.request(http.MethodGet, fmt.Sprintf("/v1/transactional-messages/%d", id), "live", "", "")
	if readData, _ := body["data"].(map[string]any); read.Code != http.StatusOK || readData["to"] != "lilei@example.com" {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	otherMode, body := f.request(http.MethodGet, fmt.Sprintf("/v1/transactional-messages/%d", id), "test", "", "")
	if otherMode.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailTransactionalMessageNotFound {
		t.Fatalf("test key read = %d %s", otherMode.Code, otherMode.Body.String())
	}
}

func TestTransactionalSendRouteRefusals(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	tests := []struct {
		name, method, path, key, idempotencyKey, body string
		status                                        int
		code                                          string
		field                                         string
	}{
		{"no key", http.MethodPost, "/v1/transactional-messages", "", "k", codeBody, http.StatusUnauthorized, "", ""},
		{"missing scope", http.MethodPost, "/v1/transactional-messages", "noscope", "k", codeBody, http.StatusForbidden, domain.CodePermissionDenied, ""},
		{"missing scope to read", http.MethodGet, "/v1/transactional-messages/1", "noscope", "", "", http.StatusForbidden, domain.CodePermissionDenied, ""},
		{"test key on outbound mail", http.MethodPost, "/v1/outbound-mail-messages", "test", "k",
			`{"from":"team@mail.example","to":["a@example.com"],"subject":"s","text":"t"}`, http.StatusForbidden, domain.CodePermissionDenied, ""},
		{"missing Idempotency-Key", http.MethodPost, "/v1/transactional-messages", "live", "", codeBody, http.StatusUnprocessableEntity, domain.CodeMailIdempotencyKeyInvalid, ""},
		{"missing recipient", http.MethodPost, "/v1/transactional-messages", "live", "k", `{"template":"signup-code"}`, http.StatusUnprocessableEntity, domain.CodeValidationFailed, ""},
		{"malformed template key", http.MethodPost, "/v1/transactional-messages", "live", "k",
			`{"template":"Signup Code","to":"a@example.com"}`, http.StatusUnprocessableEntity, domain.CodeValidationFailed, "template"},
		{"unknown template", http.MethodPost, "/v1/transactional-messages", "live", "k",
			`{"template":"missing","to":"a@example.com"}`, http.StatusNotFound, domain.CodeMailTemplateNotFound, ""},
		{"undeclared variable", http.MethodPost, "/v1/transactional-messages", "live", "k",
			`{"template":"signup-code","to":"a@example.com","variables":{"code":"1","app":"A","extra":"x"}}`,
			http.StatusUnprocessableEntity, domain.CodeMailMessageInvalid, "variables.extra"},
		{"display name", http.MethodPost, "/v1/transactional-messages", "live", "k",
			`{"template":"signup-code","to":"Li <li@example.com>","variables":{"code":"1","app":"A"}}`,
			http.StatusUnprocessableEntity, domain.CodeMailMessageInvalid, "to"},
		{"empty secret", http.MethodPost, "/v1/transactional-messages", "live", "k",
			`{"template":"signup-code","to":"a@example.com","variables":{"code":"","app":"A"}}`,
			http.StatusUnprocessableEntity, domain.CodeMailTemplateVariablesMissing, "variables.code"},
		{"body over 64 KiB", http.MethodPost, "/v1/transactional-messages", "live", "k",
			`{"template":"signup-code","to":"a@example.com","variables":{"app":"` + strings.Repeat("a", 65<<10) + `"}}`,
			http.StatusRequestEntityTooLarge, "COMMON.REQUEST_TOO_LARGE", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, body := f.request(test.method, test.path, test.key, test.idempotencyKey, test.body)
			if recorder.Code != test.status || (test.code != "" && body["error_code"] != test.code) {
				t.Fatalf("= %d %s; want %d %s", recorder.Code, recorder.Body.String(), test.status, test.code)
			}
			if test.field != "" {
				if errors, _ := body["errors"].(map[string]any); errors[test.field] == nil {
					t.Fatalf("errors = %v; want %s", body["errors"], test.field)
				}
			}
		})
	}
	if f.count(t) != 0 {
		t.Fatalf("rows = %d; refusals store nothing", f.count(t))
	}
}

func TestTransactionalRateLimitsSetRetryAfter(t *testing.T) {
	f := newTxHandlerFixture(t, func(settings *Settings) { settings.TransactionalKeyPerSecond = 4 })
	for i := 0; i < 3; i++ {
		if recorder, _ := f.request(http.MethodPost, "/v1/transactional-messages", "live", fmt.Sprintf("k%d", i),
			strings.Replace(codeBody, "482913", strconv.Itoa(i), 1)); recorder.Code != http.StatusAccepted {
			t.Fatalf("send %d = %d %s", i, recorder.Code, recorder.Body.String())
		}
	}
	recorder, body := f.request(http.MethodPost, "/v1/transactional-messages", "live", "k3", codeBody)
	if recorder.Code != http.StatusTooManyRequests || body["error_code"] != domain.CodeMailRecipientRateLimited {
		t.Fatalf("fourth to one recipient = %d %s", recorder.Code, recorder.Body.String())
	}
	if seconds, err := strconv.Atoi(recorder.Header().Get("Retry-After")); err != nil || seconds < 1 || seconds > 60 {
		t.Fatalf("Retry-After = %q", recorder.Header().Get("Retry-After"))
	}
	recorder, body = f.request(http.MethodPost, "/v1/transactional-messages", "live", "k4", codeBody)
	if recorder.Code != http.StatusTooManyRequests || body["error_code"] != domain.CodeRateLimited {
		t.Fatalf("fifth in the second = %d %s", recorder.Code, recorder.Body.String())
	}
	if seconds, err := strconv.Atoi(recorder.Header().Get("Retry-After")); err != nil || seconds != 1 {
		t.Fatalf("Retry-After = %q", recorder.Header().Get("Retry-After"))
	}
}

func TestTransactionalDisabledRoutes(t *testing.T) {
	f := newTxHandlerFixture(t, func(settings *Settings) { settings.TransactionalEnabled = false })
	for path, body := range map[string]string{
		"/v1/transactional-messages":   `not even json`,
		"/v1/transactional-test-sends": `{"template":"signup-code","mode":"test"}`,
	} {
		recorder, decoded := f.request(http.MethodPost, path, "live", "", body)
		if recorder.Code != http.StatusServiceUnavailable || decoded["error_code"] != domain.CodeMailTransactionalDisabled {
			t.Errorf("%s = %d %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTransactionalTestSendRoute(t *testing.T) {
	f := newTxHandlerFixture(t, func(settings *Settings) { settings.TransactionalRecipientPerMinute = 10 })
	recorder, body := f.request(http.MethodPost, "/v1/transactional-test-sends", "", "",
		`{"template":"signup-code","mode":"live","to":"victim@example.com","variables":{"app":"Given"}}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("test send = %d %s", recorder.Code, recorder.Body.String())
	}
	data, _ := body["data"].(map[string]any)
	if data["to"] != "owner.account@example.com" || data["subject"] != "Given verification code: ••••••" || data["mode"] != "live" {
		t.Fatalf("test send = %v; want the caller's own address", data)
	}
	bad, decoded := f.request(http.MethodPost, "/v1/transactional-test-sends", "", "", `{"template":"signup-code","mode":"both"}`)
	if bad.Code != http.StatusUnprocessableEntity || decoded["error_code"] != domain.CodeValidationFailed {
		t.Fatalf("bad mode = %d %s", bad.Code, bad.Body.String())
	}
	for i := 0; i < 4; i++ {
		if recorder, _ = f.request(http.MethodPost, "/v1/transactional-test-sends", "", "", `{"template":"signup-code","mode":"test"}`); recorder.Code != http.StatusAccepted {
			t.Fatalf("test send %d = %d %s", i+2, recorder.Code, recorder.Body.String())
		}
	}
	recorder, body = f.request(http.MethodPost, "/v1/transactional-test-sends", "", "", `{"template":"signup-code","mode":"test"}`)
	if recorder.Code != http.StatusTooManyRequests || body["error_code"] != domain.CodeRateLimited || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("sixth test send = %d %s", recorder.Code, recorder.Body.String())
	}
}

// The mailbox domain listing carries every lane limit and the transactional settings.
func TestMailboxDomainsReportLanesAndTransactionalSettings(t *testing.T) {
	f := newTxHandlerFixture(t, func(settings *Settings) {
		settings.DailySendLimit, settings.DailyBroadcastLimit, settings.DailyTransactionalLimit = 5, 0, 9
		settings.TransactionalRecipientPerMinute, settings.TransactionalKeyPerSecond = 4, 15
		settings.SimulatorDomain = "sim.example.com"
	})
	recorder, body := f.request(http.MethodGet, "/v1/mailbox-domains", "", "", "")
	items, _ := body["data"].([]any)
	if recorder.Code != http.StatusOK || len(items) != 1 {
		t.Fatalf("domains = %d %s", recorder.Code, recorder.Body.String())
	}
	item := items[0].(map[string]any)
	want := map[string]any{
		"daily_send_limit": float64(5), "daily_broadcast_limit": float64(5), "daily_transactional_limit": float64(9),
		"transactional_enabled": true, "transactional_recipient_limit_per_minute": float64(4),
		"transactional_key_limit_per_second": float64(15), "simulator_domain": "sim.example.com",
	}
	for field, value := range want {
		if item[field] != value {
			t.Errorf("%s = %#v; want %#v", field, item[field], value)
		}
	}

	off := newTxHandlerFixture(t, func(settings *Settings) { settings.TransactionalEnabled = false })
	_, body = off.request(http.MethodGet, "/v1/mailbox-domains", "", "", "")
	item = body["data"].([]any)[0].(map[string]any)
	if item["transactional_enabled"] != false || item["transactional_recipient_limit_per_minute"] != float64(3) ||
		item["transactional_key_limit_per_second"] != float64(20) || item["simulator_domain"] != "test.zmail.im" {
		t.Fatalf("defaults = %v", item)
	}
}
