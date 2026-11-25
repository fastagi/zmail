package transactional

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

type fakeTransactionalPort struct {
	enabled         bool
	err             error
	lastSendInput   domain.MailTransactionalSendInput
	lastDirectInput domain.MailDirectSendInput
}

func (f *fakeTransactionalPort) TransactionalEnabledPort() bool { return f.enabled }

func (f *fakeTransactionalPort) SendTransactionalPort(_ context.Context, actor domain.MailTransactionalActor, input domain.MailTransactionalSendInput) (*domain.TransactionalMessage, bool, error) {
	f.lastSendInput = input
	if f.err != nil {
		return nil, false, f.err
	}
	return &domain.TransactionalMessage{
		ID:            77,
		Mode:          actor.Mode,
		TemplateKey:   input.Template,
		FromAddress:   "no-reply@example.com",
		ToAddress:     input.To,
		SubjectMasked: "Code ***",
		Status:        domain.TransactionalStatusQueued,
		CreatedAt:     time.Now().UTC(),
	}, true, nil
}

func (f *fakeTransactionalPort) SendDirectTransactionalPort(_ context.Context, actor domain.MailTransactionalActor, input domain.MailDirectSendInput) (*domain.TransactionalMessage, bool, error) {
	f.lastDirectInput = input
	if f.err != nil {
		return nil, false, f.err
	}
	return &domain.TransactionalMessage{
		ID:            79,
		Mode:          actor.Mode,
		FromAddress:   "noreply@kfk.im",
		FromName:      "Keepkite",
		ToAddress:     input.To,
		SubjectMasked: input.Subject,
		Status:        domain.TransactionalStatusQueued,
		CreatedAt:     time.Now().UTC(),
	}, true, nil
}

func (f *fakeTransactionalPort) GetTransactionalPort(_ context.Context, _ uint, mode domain.APIKeyMode, id uint) (*domain.TransactionalMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.TransactionalMessage{ID: id, Mode: mode, ToAddress: "a@example.com", Status: domain.TransactionalStatusSent}, nil
}

func (f *fakeTransactionalPort) ListTransactionalLogPort(_ context.Context, _ uint, _ domain.TransactionalLogFilter) (domain.MailTransactionalLogPage, error) {
	if f.err != nil {
		return domain.MailTransactionalLogPage{}, f.err
	}
	return domain.MailTransactionalLogPage{
		Items:    []*domain.TransactionalMessage{{ID: 77, ToAddress: "a@example.com", Status: domain.TransactionalStatusSent}},
		Total24h: 1,
	}, nil
}

func (f *fakeTransactionalPort) GetTransactionalLogEntryPort(_ context.Context, _, id uint) (*domain.TransactionalMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.TransactionalMessage{ID: id, ToAddress: "a@example.com", Status: domain.TransactionalStatusSent}, nil
}

func (f *fakeTransactionalPort) SendTransactionalTestPort(_ context.Context, _ domain.OrganizationContext, input domain.MailTransactionalTestInput) (*domain.TransactionalMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.TransactionalMessage{ID: 78, Mode: input.Mode, Simulated: input.Mode == domain.APIKeyModeTest, TemplateKey: input.Template}, nil
}

func (f *fakeTransactionalPort) ListSendLanesPort(_ context.Context, _ domain.OrganizationContext) ([]domain.MailSendLaneUsage, time.Time, error) {
	if f.err != nil {
		return nil, time.Time{}, f.err
	}
	return []domain.MailSendLaneUsage{{Lane: domain.SendLaneTransactional, Limit: 1000, Used: 5, Waiting: 0}}, time.Now().UTC(), nil
}

func (f *fakeTransactionalPort) ListSuppressionsPort(_ context.Context, _ domain.OrganizationContext, _ domain.MailSuppressionFilter) ([]domain.MailSuppression, *uint, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return []domain.MailSuppression{{ID: 3, Email: "bounce@example.com", Reason: "hard_bounce", Source: "webhook"}}, nil, nil
}

func (f *fakeTransactionalPort) RemoveSuppressionPort(_ context.Context, _ domain.OrganizationContext, _ uint) error {
	return f.err
}

func newTransactionalTestRouter(t *testing.T, port domain.MailTransactionalPort) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	root := router.New(engine)
	root.MiddlewareGroup("auth", func(c *gin.Context) { c.Next() })
	root.MiddlewareGroup("api_key", func(c *gin.Context) {
		c.Set("apiKeyID", uint(55))
		c.Set("apiKeyScopes", []string{ScopeMailTransactional})
		c.Set("apiKeyMode", domain.APIKeyModeLive)
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

func TestTransactionalHandlerFunctionalFlow(t *testing.T) {
	port := &fakeTransactionalPort{enabled: true}
	engine := newTransactionalTestRouter(t, port)

	// 1. POST /v1/transactional-messages -> 202 + Location
	body, _ := json.Marshal(map[string]any{"template": "signup-code", "to": "alice@example.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/transactional-messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-1")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || rec.Header().Get("Location") != "/v1/transactional-messages/77" {
		t.Fatalf("POST transactional-messages: status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	// 2. GET /v1/transactional-messages/77 -> 200
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/transactional-messages/77", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET transactional-messages/77: status=%d", rec.Code)
	}

	// 3. GET /v1/transactional-mail-log & detail -> 200
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/transactional-mail-log", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET transactional-mail-log: status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/transactional-mail-log/77", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET transactional-mail-log/77: status=%d", rec.Code)
	}

	// 4. POST /v1/transactional-test-sends -> 202
	body, _ = json.Marshal(map[string]any{"template": "signup-code", "mode": "test"})
	req = httptest.NewRequest(http.MethodPost, "/v1/transactional-test-sends", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST transactional-test-sends: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 5. GET /v1/mail-send-lanes -> 200
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mail-send-lanes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET mail-send-lanes: status=%d", rec.Code)
	}

	// 6. GET /v1/mail-suppressions & DELETE -> 200 / 204
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mail-suppressions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET mail-suppressions: status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/mail-suppressions/3", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE mail-suppressions/3: status=%d", rec.Code)
	}

	// 7. Disabled -> 503 before body validation
	port.enabled = false
	req = httptest.NewRequest(http.MethodPost, "/v1/transactional-messages", bytes.NewReader([]byte("not json")))
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when disabled, got %d", rec.Code)
	}
}

func TestEmailsHandlerDirectAndTemplateModes(t *testing.T) {
	port := &fakeTransactionalPort{enabled: true}
	engine := newTransactionalTestRouter(t, port)

	// 1. Direct send with string "to" and no Idempotency-Key -> 202 + Location /v1/emails/79
	body, _ := json.Marshal(map[string]any{
		"from":    "Keepkite <noreply@kfk.im>",
		"to":      "alice@example.com",
		"subject": "Welcome",
		"html":    "<p>Hello!</p>",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/emails", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || rec.Header().Get("Location") != "/v1/emails/79" {
		t.Fatalf("POST /v1/emails direct (string to): status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if port.lastDirectInput.To != "alice@example.com" || port.lastDirectInput.From != "Keepkite <noreply@kfk.im>" {
		t.Fatalf("unexpected direct input: %+v", port.lastDirectInput)
	}

	// 2. Direct send with array "to" and explicit Idempotency-Key -> 202
	body, _ = json.Marshal(map[string]any{
		"from":    "noreply@kfk.im",
		"to":      []string{"bob@example.com"},
		"subject": "Hello",
		"text":    "Hi Bob",
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/emails", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "custom-key-1")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/emails direct (array to): status=%d body=%s", rec.Code, rec.Body.String())
	}
	if port.lastDirectInput.To != "bob@example.com" || port.lastDirectInput.IdempotencyKey != "custom-key-1" {
		t.Fatalf("unexpected direct input: %+v", port.lastDirectInput)
	}

	// 3. Template mode without Idempotency-Key -> auto-generates key & delegates to SendTransactionalPort
	body, _ = json.Marshal(map[string]any{
		"template":  "signup-code",
		"to":        []string{"carol@example.com"},
		"variables": map[string]string{"code": "123456"},
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/emails", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || rec.Header().Get("Location") != "/v1/emails/77" {
		t.Fatalf("POST /v1/emails template: status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if port.lastSendInput.Template != "signup-code" || port.lastSendInput.To != "carol@example.com" || port.lastSendInput.IdempotencyKey == "" {
		t.Fatalf("unexpected template input: %+v", port.lastSendInput)
	}

	// 4. GET /v1/emails/79 -> 200
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/emails/79", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/emails/79: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 5. Missing "to" -> 422
	body, _ = json.Marshal(map[string]any{"from": "noreply@kfk.im", "subject": "Hi", "text": "Hello"})
	req = httptest.NewRequest(http.MethodPost, "/v1/emails", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /v1/emails missing to: expected 422, got %d", rec.Code)
	}

	// 6. Multiple recipients -> 422
	body, _ = json.Marshal(map[string]any{"from": "noreply@kfk.im", "to": []string{"a@example.com", "b@example.com"}, "subject": "Hi", "text": "Hello"})
	req = httptest.NewRequest(http.MethodPost, "/v1/emails", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /v1/emails multiple to: expected 422, got %d", rec.Code)
	}
}
