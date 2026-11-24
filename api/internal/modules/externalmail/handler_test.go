package externalmail

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

type fakeExternalMailPort struct {
	err error
}

func (f *fakeExternalMailPort) ListExternalMailConnectorsPort(_ context.Context) []domain.ExternalMailConnectorView {
	return []domain.ExternalMailConnectorView{{ID: "gmail", AuthKind: "oauth", Read: true, Send: true, Tenant: true}}
}

func (f *fakeExternalMailPort) StartExternalMailAuthorizationPort(_ context.Context, _ domain.OrganizationContext, _ domain.ExternalMailAuthorizationInput) (domain.ExternalMailAuthorizationStart, error) {
	return domain.ExternalMailAuthorizationStart{
		AuthorizationURL: "https://accounts.example.com/auth",
		ExpiresAt:        time.Now().Add(10 * time.Minute).UTC(),
	}, f.err
}

func (f *fakeExternalMailPort) CompleteExternalMailAuthorizationPort(_ context.Context, _ domain.OrganizationContext, _, _ string) (*domain.ExternalMailConnection, bool, error) {
	return &domain.ExternalMailConnection{
		ID:             2,
		Provider:       domain.ExternalMailProviderGmail,
		AccountAddress: "alice@gmail.com",
		MailboxID:      5,
		Status:         domain.ExternalMailStatusActive,
		CreatedAt:      time.Now().UTC(),
	}, true, f.err
}

func (f *fakeExternalMailPort) ListExternalMailConnectionsPort(_ context.Context, _ domain.OrganizationContext) ([]*domain.ExternalMailConnection, error) {
	return []*domain.ExternalMailConnection{{
		ID:             2,
		Provider:       domain.ExternalMailProviderGmail,
		AccountAddress: "alice@gmail.com",
		Status:         domain.ExternalMailStatusActive,
		CreatedAt:      time.Now().UTC(),
	}}, f.err
}

func (f *fakeExternalMailPort) RevokeExternalMailConnectionPort(_ context.Context, _ domain.OrganizationContext, _ uint) error {
	return f.err
}

func newExternalMailTestRouter(t *testing.T, port domain.ExternalMailPort) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	root := router.New(engine)
	root.MiddlewareGroup("auth", func(c *gin.Context) { c.Next() })
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

func TestExternalMailHandlerFunctionalFlow(t *testing.T) {
	port := &fakeExternalMailPort{}
	engine := newExternalMailTestRouter(t, port)

	// 1. List connectors -> 200
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/external-mail-connectors", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET connectors: status=%d", rec.Code)
	}

	// 2. Start authorization (missing provider & connection_id -> 422)
	req := httptest.NewRequest(http.MethodPost, "/v1/external-mail-authorizations", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on empty auth start, got %d", rec.Code)
	}

	// 3. Start authorization -> 201
	body, _ := json.Marshal(map[string]any{"provider": "gmail"})
	req = httptest.NewRequest(http.MethodPost, "/v1/external-mail-authorizations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST authorizations: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 4. Complete authorization -> 201
	body, _ = json.Marshal(map[string]any{"state": "st-1", "code": "code-1"})
	req = httptest.NewRequest(http.MethodPost, "/v1/external-mail-authorizations/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST completions: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 5. List connections -> 200
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/external-mail-connections", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET connections: status=%d", rec.Code)
	}

	// 6. Revoke connection -> 204
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/external-mail-connections/2", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE connections/2: status=%d", rec.Code)
	}
}
