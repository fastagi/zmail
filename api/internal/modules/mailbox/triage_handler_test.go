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

// triageRouter serves the triage and inbox handlers as viewer, with a real server's error mapping.
func triageRouter(svc *service, viewer domain.OrganizationContext) *gin.Engine {
	router := accessRouter(svc, viewer)
	handler := NewHandler(svc)
	router.GET("/mail-triage-setting", handler.GetTriageSetting)
	router.PUT("/mail-triage-setting", handler.UpdateTriageSetting)
	router.PUT("/mailboxes/:mailbox_id/triage-setting", handler.SetMailboxTriageSetting)
	router.PUT("/mailboxes/:mailbox_id/messages/:message_id/triage", handler.UpdateMessageTriage)
	router.POST("/mailboxes/:mailbox_id/messages/:message_id/reply-drafts", handler.CreateReplyDraft)
	router.GET("/inbox-messages", handler.ListInboxMessages)
	router.GET("/inbox-message-counts", handler.InboxMessageCounts)
	return router
}

// triageCall sends one request as viewer and decodes the envelope.
func triageCall(t *testing.T, svc *service, viewer domain.OrganizationContext, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	triageRouter(svc, viewer).ServeHTTP(recorder, request)
	var envelope map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &envelope)
	return recorder, envelope
}

func TestTriageHTTPFlow(t *testing.T) {
	f := newTriageFixture(t)

	recorder, body := triageCall(t, f.service, f.b, http.MethodGet, "/mail-triage-setting", "")
	setting, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || setting["available"] != true || setting["enabled"] != false ||
		setting["daily_message_cap"] != float64(domain.DefaultMailTriageDailyMessageCap) || setting["instance_daily_cap"] != float64(100) {
		t.Fatalf("GET setting = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("setting headers = %v", recorder.Header())
	}
	on := `{"enabled": true, "daily_message_cap": 100, "draft_enabled": true}`
	if recorder, _ = triageCall(t, f.service, f.b, http.MethodPut, "/mail-triage-setting", on); recorder.Code != http.StatusForbidden {
		t.Fatalf("member PUT setting = %d", recorder.Code)
	}
	if recorder, _ = triageCall(t, f.service, f.admin, http.MethodPut, "/mail-triage-setting", `{"enabled": true}`); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT setting without every field = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder, body = triageCall(t, f.service, f.admin, http.MethodPut, "/mail-triage-setting", on)
	if setting, _ = body["data"].(map[string]any); recorder.Code != http.StatusOK || setting["enabled"] != true || setting["draft_enabled"] != true {
		t.Fatalf("admin PUT setting = %d %s", recorder.Code, recorder.Body.String())
	}

	teamSwitch := fmt.Sprintf("/mailboxes/%d/triage-setting", f.team.ID)
	if recorder, _ = triageCall(t, f.service, f.b, http.MethodPut, teamSwitch, `{"enabled": true}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("send member PUT team switch = %d", recorder.Code)
	}
	recorder, body = triageCall(t, f.service, f.admin, http.MethodPut, teamSwitch, `{"enabled": true}`)
	if mailbox, _ := body["data"].(map[string]any); recorder.Code != http.StatusOK || mailbox["ai_triage_enabled"] != true {
		t.Fatalf("admin PUT team switch = %d %s", recorder.Code, recorder.Body.String())
	}

	message := f.receiveText(t, f.team, "http@sender.test", "Contract", "Can you sign by Friday?")
	triagePath := fmt.Sprintf("/mailboxes/%d/messages/%d/triage", f.team.ID, message.ID)
	recorder, body = triageCall(t, f.service, f.c, http.MethodPut, triagePath, `{"category": "fyi"}`)
	if triage, _ := body["data"].(map[string]any); recorder.Code != http.StatusOK || triage["category"] != "fyi" || triage["source"] != "manual" {
		t.Fatalf("view member PUT triage = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder, body = triageCall(t, f.service, f.c, http.MethodPut, triagePath, `{"category": "deadline", "due_at": "2099-01-01T00:00:00Z"}`)
	if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeValidationFailed {
		t.Fatalf("PUT triage far due = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder, _ = triageCall(t, f.service, f.d, http.MethodPut, triagePath, `{"category": "fyi"}`); recorder.Code != http.StatusNotFound {
		t.Fatalf("outsider PUT triage = %d", recorder.Code)
	}

	draftPath := fmt.Sprintf("/mailboxes/%d/messages/%d/reply-drafts", f.team.ID, message.ID)
	if recorder, _ = triageCall(t, f.service, f.c, http.MethodPost, draftPath, ""); recorder.Code != http.StatusForbidden {
		t.Fatalf("view member POST draft = %d", recorder.Code)
	}
	recorder, body = triageCall(t, f.service, f.b, http.MethodPost, draftPath, "")
	draft, _ := body["data"].(map[string]any)
	basis, _ := draft["basis"].([]any)
	if recorder.Code != http.StatusOK || draft["body"] == "" || len(basis) != 1 || recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("send member POST draft = %d %s", recorder.Code, recorder.Body.String())
	}
	if first, _ := basis[0].(map[string]any); first["kind"] != "message" || first["id"] != float64(message.ID) || first["label"] != "Contract" {
		t.Fatalf("basis = %v", basis)
	}

	off := `{"enabled": false, "daily_message_cap": 100, "draft_enabled": false}`
	if recorder, _ = triageCall(t, f.service, f.admin, http.MethodPut, "/mail-triage-setting", off); recorder.Code != http.StatusOK {
		t.Fatalf("admin PUT off = %d", recorder.Code)
	}
	recorder, body = triageCall(t, f.service, f.c, http.MethodPut, triagePath, `{"category": "fyi"}`)
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailTriageUnavailable {
		t.Fatalf("PUT triage with the organization off = %d %s", recorder.Code, recorder.Body.String())
	}
}
