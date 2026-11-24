package agentgrant

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

type fakeAgentGrantPort struct {
	err error
}

func (f *fakeAgentGrantPort) IssueAgentGrantPort(_ context.Context, viewer domain.OrganizationContext, mailboxID uint, input domain.IssueMailAgentGrantInput) (*domain.MailAgentGrant, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.MailAgentGrant{
		ID:            11,
		MailboxID:     mailboxID,
		APIKeyID:      input.APIKeyID,
		APIKeyName:    "Bot",
		APIKeyPrefix:  "zm_live_",
		KeyUserID:     viewer.UserID,
		GrantedBy:     viewer.UserID,
		CanDraft:      true,
		RevealSecrets: input.RevealSecrets,
		SenderFilters: input.Senders,
		LabelIDs:      input.LabelIDs,
		ExpiresAt:     input.ExpiresAt,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func (f *fakeAgentGrantPort) ListAgentGrantsPort(_ context.Context, _ domain.OrganizationContext, mailboxID uint, _, _ int) ([]*domain.MailAgentGrant, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return []*domain.MailAgentGrant{{ID: 11, MailboxID: mailboxID, ExpiresAt: time.Now().Add(time.Hour)}}, 1, nil
}

func (f *fakeAgentGrantPort) RevokeAgentGrantPort(_ context.Context, _ domain.OrganizationContext, _, _ uint) error {
	return f.err
}

func (f *fakeAgentGrantPort) AgentGrantActivityPort(_ context.Context, _ domain.OrganizationContext, _, grantID uint) ([]*domain.MailAgentAuditEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []*domain.MailAgentAuditEntry{{ID: 1, GrantID: grantID, APIKeyID: 99, MessageID: 5, Action: "read"}}, nil
}

func (f *fakeAgentGrantPort) ListAgentDraftsPort(_ context.Context, _ domain.OrganizationContext, mailboxID uint, _, _ int) ([]*domain.MailAgentDraft, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return []*domain.MailAgentDraft{{ID: 3, MailboxID: mailboxID, GrantID: 11, MessageID: 5, TextBody: "Hi", Status: "pending"}}, 1, nil
}

func (f *fakeAgentGrantPort) AgentMailEventsPort(_ context.Context, _ domain.OrganizationContext, _, _ uint, _ int) (domain.MailAgentFeedPage, error) {
	if f.err != nil {
		return domain.MailAgentFeedPage{}, f.err
	}
	return domain.MailAgentFeedPage{
		Events:     []*domain.MailAgentEvent{{ID: 7, Type: "message.received", GrantID: 11, MailboxID: 1, MessageID: 5}},
		NextCursor: 7,
		HasMore:    false,
	}, nil
}

func (f *fakeAgentGrantPort) SaveAgentDraftPort(_ context.Context, _ domain.OrganizationContext, apiKeyID uint, input domain.MailAgentDraftInput) (*domain.MailAgentDraft, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	return &domain.MailAgentDraft{
		ID: 4, MailboxID: 1, GrantID: 11, MessageID: input.MessageID, APIKeyID: apiKeyID, TextBody: input.Text, Status: "pending",
	}, true, nil
}

func newAgentGrantTestRouter(t *testing.T, port domain.MailAgentGrantPort) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	root := router.New(engine)
	root.MiddlewareGroup("auth", func(c *gin.Context) { c.Next() })
	root.MiddlewareGroup("api_key", func(c *gin.Context) {
		c.Set("apiKeyID", uint(99))
		c.Set("apiKeyScopes", []string{ScopeMailAgent})
		c.Next()
	})
	root.MiddlewareGroup("organization_context", func(c *gin.Context) {
		ctx := domain.WithOrganizationContext(c.Request.Context(), domain.OrganizationContext{
			OrganizationID:   1,
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

func TestAgentGrantHandlerFunctionalFlow(t *testing.T) {
	port := &fakeAgentGrantPort{}
	engine := newAgentGrantTestRouter(t, port)

	// 1. Issue grant -> 201
	body, _ := json.Marshal(map[string]any{
		"api_key_id":   99,
		"capabilities": []string{"read", "draft"},
		"expires_at":   time.Now().Add(24 * time.Hour).UTC(),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/mailboxes/1/agent-grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST agent-grants: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 2. List grants -> 200
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mailboxes/1/agent-grants", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET agent-grants: status=%d", rec.Code)
	}

	// 3. Grant activity -> 200
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mailboxes/1/agent-grants/11/activity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET activity: status=%d", rec.Code)
	}

	// 4. List agent drafts -> 200
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mailboxes/1/agent-drafts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET agent-drafts: status=%d", rec.Code)
	}

	// 5. Agent mail events feed -> 200
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/agent-mail-events?limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET agent-mail-events: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 6. Create agent draft -> 201
	body, _ = json.Marshal(map[string]any{"message_id": 5, "text": "Draft reply"})
	req = httptest.NewRequest(http.MethodPost, "/v1/agent-mail-drafts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST agent-mail-drafts: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 7. Revoke grant -> 204
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/mailboxes/1/agent-grants/11", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE agent-grants/11: status=%d", rec.Code)
	}

	// 8. Error mapping: ErrMailAgentGrantNotFound -> 404
	port.err = domain.ErrMailAgentGrantNotFound
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/mailboxes/1/agent-grants/999", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on ErrMailAgentGrantNotFound, got %d", rec.Code)
	}
}
