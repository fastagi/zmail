package triage

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/pkg/response"
)

type fakeTriagePort struct {
	setting domain.MailTriageSettingView
	err     error
}

func (f *fakeTriagePort) GetTriageSettingPort(_ context.Context, _ domain.OrganizationContext) (domain.MailTriageSettingView, error) {
	return f.setting, f.err
}

func (f *fakeTriagePort) SaveTriageSettingPort(_ context.Context, _ domain.OrganizationContext, input domain.MailTriageSettingInput) (domain.MailTriageSettingView, error) {
	if f.err != nil {
		return domain.MailTriageSettingView{}, f.err
	}
	f.setting.Enabled = input.Enabled
	f.setting.DailyMessageCap = input.DailyMessageCap
	f.setting.DraftEnabled = input.DraftEnabled
	f.setting.Saved = true
	f.setting.UpdatedAt = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	return f.setting, nil
}

func (f *fakeTriagePort) OverrideTriagePort(_ context.Context, _ domain.OrganizationContext, _, _ uint, input domain.MailTriageOverrideInput) (*domain.MailTriage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.MailTriage{
		Category: input.Category,
		DueAt:    input.DueAt,
		Source:   domain.MailTriageSourceManual,
		Reason:   "manual override",
	}, nil
}

func (f *fakeTriagePort) DraftReplyPort(_ context.Context, _ domain.OrganizationContext, _, msgID uint) (domain.MailReplyDraft, error) {
	if f.err != nil {
		return domain.MailReplyDraft{}, f.err
	}
	return domain.MailReplyDraft{
		Body: "Thanks for reaching out!",
		Basis: []domain.MailReplyDraftBasis{
			{Kind: "message", ID: msgID, Label: "Original thread"},
		},
	}, nil
}

func newTriageTestRouter(t *testing.T, port domain.MailTriagePort) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	root := router.New(engine)
	root.MiddlewareGroup("auth", func(c *gin.Context) { c.Next() })
	root.MiddlewareGroup("organization_context", func(c *gin.Context) {
		ctx := domain.WithOrganizationContext(c.Request.Context(), domain.OrganizationContext{
			OrganizationID:   10,
			OrganizationName: "Acme",
			OrganizationSlug: "acme",
			MembershipID:     1,
			UserID:           1,
			Role:             domain.OrganizationRoleOwner,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := NewHandler(port)
	h.RegisterErrorMappings(response.DefaultErrorMapper)
	h.RegisterRoutes(root.Prefix("/v1"))
	return engine
}

func TestTriageHandlerFunctionalFlow(t *testing.T) {
	port := &fakeTriagePort{
		setting: domain.MailTriageSettingView{
			Available:        true,
			InstanceDailyCap: 500,
		},
	}
	engine := newTriageTestRouter(t, port)

	// 1. GET /v1/mail-triage-setting
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mail-triage-setting", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET setting: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("missing private cache header")
	}

	// 2. PUT /v1/mail-triage-setting
	body, _ := json.Marshal(map[string]any{"enabled": true, "daily_message_cap": 100, "draft_enabled": true})
	req := httptest.NewRequest(http.MethodPut, "/v1/mail-triage-setting", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT setting: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. PUT /v1/mailboxes/1/messages/9/triage
	body, _ = json.Marshal(map[string]any{"category": "reply"})
	req = httptest.NewRequest(http.MethodPut, "/v1/mailboxes/1/messages/9/triage", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT message triage: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 4. POST /v1/mailboxes/1/messages/9/reply-drafts
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/mailboxes/1/messages/9/reply-drafts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST reply-drafts: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 5. Error mapping: ErrMailTriageUnavailable -> 409
	port.err = domain.ErrMailTriageUnavailable
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/mailboxes/1/messages/9/reply-drafts", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on ErrMailTriageUnavailable, got %d body=%s", rec.Code, rec.Body.String())
	}
}
