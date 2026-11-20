package mailbox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ledgerRouter serves the ledger routes as viewer, with the error mapping of a real server.
func (f *ledgerFixture) ledgerRouter(viewer domain.OrganizationContext) *gin.Engine {
	router := accessRouter(f.service, viewer)
	handler := NewHandler(f.service)
	router.GET("/platform-accounts", handler.ListPlatformAccounts)
	router.POST("/platform-accounts", handler.CreatePlatformAccount)
	router.PUT("/platform-accounts/:account_id", handler.UpdatePlatformAccount)
	router.DELETE("/platform-accounts/:account_id", handler.DeletePlatformAccount)
	router.GET("/platform-accounts/:account_id/events", handler.ListPlatformAccountEvents)
	router.POST("/platform-account-handovers", handler.HandOverPlatformAccounts)
	return router
}

func (f *ledgerFixture) call(t *testing.T, viewer domain.OrganizationContext, method, path, body string) (int, map[string]any, http.Header) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	f.ledgerRouter(viewer).ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded, recorder.Header()
}

func dataHandles(body map[string]any) []string {
	items, _ := body["data"].([]any)
	handles := make([]string, 0, len(items))
	for _, item := range items {
		handles = append(handles, fmt.Sprint(item.(map[string]any)["handle"]))
	}
	return handles
}

func TestPlatformAccountHTTPFlow(t *testing.T) {
	f := newLedgerFixture(t)

	status, body, header := f.call(t, f.admin, http.MethodPost, "/platform-accounts", fmt.Sprintf(
		`{"platform":"YouTube","handle":"@acme","profile_url":"https://youtube.com/@acme","role_id":null,"mailbox_id":%d,"owner_user_id":%d,"status":"invited","notes":"launch"}`,
		f.personal.ID, accessB))
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, body)
	}
	if header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", header.Get("Cache-Control"))
	}
	created := body["data"].(map[string]any)
	for key, want := range map[string]any{
		"platform": "YouTube", "handle": "@acme", "profile_url": "https://youtube.com/@acme", "role_id": nil,
		"mailbox_id": float64(f.personal.ID), "mailbox_address": f.personal.Address, "owner_user_id": float64(accessB),
		"status": "invited", "notes": "launch",
	} {
		if created[key] != want {
			t.Fatalf("created[%s] = %v; want %v (%v)", key, created[key], want, created)
		}
	}
	id := uint(created["id"].(float64))

	status, body, _ = f.call(t, f.b, http.MethodGet, "/platform-accounts?q="+f.personal.Address+"&per_page=5", "")
	if status != http.StatusOK || strings.Join(dataHandles(body), ",") != "blog,@acme" {
		t.Fatalf("B searching b@ = %d %v; want blog and @acme", status, body)
	}
	meta := body["meta"].(map[string]any)
	if meta["total"] != float64(2) || meta["per_page"] != float64(5) {
		t.Fatalf("meta = %v", meta)
	}
	status, body, _ = f.call(t, f.d, http.MethodGet, "/platform-accounts", "")
	if status != http.StatusOK || strings.Join(dataHandles(body), ",") != "studio" {
		t.Fatalf("D listing = %d %v; want only studio", status, body)
	}
	if studio := body["data"].([]any)[0].(map[string]any); studio["mailbox_address"] != nil {
		t.Fatalf("D sees studio's address %v; want null", studio["mailbox_address"])
	}
	status, body, _ = f.call(t, f.admin, http.MethodGet, fmt.Sprintf("/platform-accounts?status=invited&owner_user_id=%d&mailbox_id=%d", accessB, f.personal.ID), "")
	if status != http.StatusOK || strings.Join(dataHandles(body), ",") != "@acme" {
		t.Fatalf("filtered listing = %d %v", status, body)
	}

	status, body, _ = f.call(t, f.admin, http.MethodPut, fmt.Sprintf("/platform-accounts/%d", id),
		`{"platform":"YouTube","handle":"@acme","status":"active"}`)
	if status != http.StatusOK || body["data"].(map[string]any)["mailbox_id"] != nil || body["data"].(map[string]any)["profile_url"] != nil {
		t.Fatalf("replace = %d %v; want omitted fields cleared", status, body)
	}

	status, body, _ = f.call(t, f.admin, http.MethodPost, "/platform-account-handovers",
		fmt.Sprintf(`{"from_user_id":%d,"to_user_id":%d}`, accessD, accessC))
	if status != http.StatusCreated {
		t.Fatalf("handover = %d %v", status, body)
	}
	handover := body["data"].(map[string]any)
	if handover["from_user_id"] != float64(accessD) || handover["to_user_id"] != float64(accessC) ||
		fmt.Sprint(handover["account_ids"]) != fmt.Sprintf("[%d]", f.studio.ID) {
		t.Fatalf("handover = %v", handover)
	}

	status, body, _ = f.call(t, f.c, http.MethodGet, fmt.Sprintf("/platform-accounts/%d/events", f.studio.ID), "")
	if status != http.StatusOK {
		t.Fatalf("events = %d %v", status, body)
	}
	events := body["data"].([]any)
	first := events[0].(map[string]any)
	if len(events) != 2 || first["kind"] != "owner_changed" || first["from"] != "4" || first["to"] != "3" ||
		first["actor_user_id"] != float64(accessAdmin) || first["account_id"] != float64(f.studio.ID) {
		t.Fatalf("events = %v", events)
	}
	if created := events[1].(map[string]any); created["kind"] != "created" || created["from"] != nil || created["to"] != "active" {
		t.Fatalf("created event = %v", created)
	}

	status, _, _ = f.call(t, f.admin, http.MethodDelete, fmt.Sprintf("/platform-accounts/%d", id), "")
	if status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
}

func TestPlatformAccountHTTPErrors(t *testing.T) {
	f := newLedgerFixture(t)
	studio := fmt.Sprintf("/platform-accounts/%d", f.studio.ID)
	for name, test := range []struct {
		viewer     domain.OrganizationContext
		method     string
		path, body string
		status     int
		code       string
		field      string
	}{
		{f.admin, http.MethodPost, "/platform-accounts", `[]`, http.StatusBadRequest, domain.CodeInvalidInput, ""},
		{f.admin, http.MethodPost, "/platform-accounts", `{"platform":"x","handle":"y","mailbox_id":"one"}`, http.StatusBadRequest, domain.CodeInvalidInput, ""},
		{f.admin, http.MethodPost, "/platform-accounts", `{"platform":"x","handle":"` + strings.Repeat("y", 9000) + `"}`, http.StatusBadRequest, domain.CodeInvalidInput, ""},
		{f.b, http.MethodPost, "/platform-accounts", `{"platform":"x","handle":"y"}`, http.StatusForbidden, domain.CodePermissionDenied, ""},
		{f.admin, http.MethodPost, "/platform-accounts", `{"platform":"medium","handle":"BLOG"}`, http.StatusConflict, domain.CodePlatformAccountTaken, ""},
		{f.admin, http.MethodPost, "/platform-accounts", `{"platform":"x","handle":"y","profile_url":"ftp://x"}`, http.StatusUnprocessableEntity, domain.CodeValidationFailed, "profile_url"},
		{f.admin, http.MethodPut, "/platform-accounts/9999", `{"platform":"x","handle":"y"}`, http.StatusNotFound, domain.CodePlatformAccountNotFound, ""},
		{f.d, http.MethodPut, studio, `{"platform":"x","handle":"y"}`, http.StatusForbidden, domain.CodePermissionDenied, ""},
		{f.admin, http.MethodDelete, "/platform-accounts/9999", ``, http.StatusNotFound, domain.CodePlatformAccountNotFound, ""},
		{f.d, http.MethodGet, fmt.Sprintf("/platform-accounts/%d/events", f.blog.ID), ``, http.StatusNotFound, domain.CodePlatformAccountNotFound, ""},
		{f.b, http.MethodGet, "/platform-accounts?status=lost", ``, http.StatusBadRequest, domain.CodeInvalidInput, ""},
		{f.b, http.MethodGet, "/platform-accounts?role_id=abc", ``, http.StatusBadRequest, domain.CodeInvalidInput, ""},
		{f.b, http.MethodGet, "/platform-accounts?owner_user_id=0", ``, http.StatusBadRequest, domain.CodeInvalidInput, ""},
		{f.b, http.MethodGet, "/platform-accounts?q=" + strings.Repeat("q", 101), ``, http.StatusBadRequest, domain.CodeInvalidInput, ""},
		{f.admin, http.MethodPost, "/platform-account-handovers", `{"from_user_id":4}`, http.StatusUnprocessableEntity, domain.CodeValidationFailed, "to_user_id"},
		{f.c, http.MethodPost, "/platform-account-handovers", `{"from_user_id":4,"to_user_id":2}`, http.StatusForbidden, domain.CodePermissionDenied, ""},
	} {
		status, body, _ := f.call(t, test.viewer, test.method, test.path, test.body)
		what := fmt.Sprintf("case %d %s %s", name, test.method, test.path)
		if test.field != "" {
			expectFieldError(t, what, status, body, test.field)
			continue
		}
		expectRoleStatus(t, what, status, body, test.status, test.code)
	}
}
