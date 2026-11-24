package maildomain

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/pkg/response"
)

type fakeMailDomainPort struct {
	err error
}

func (f *fakeMailDomainPort) ListMailDomainsPort(_ context.Context, _ uint) ([]*domain.MailDomain, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []*domain.MailDomain{{ID: 1, Name: "acme.dev", Status: domain.MailDomainStatusVerified}}, nil
}

func (f *fakeMailDomainPort) GetMailDomainPort(_ context.Context, _, id uint) (*domain.MailDomain, error) {
	return &domain.MailDomain{ID: id, Name: "acme.dev", Status: domain.MailDomainStatusVerified}, f.err
}

func (f *fakeMailDomainPort) ConnectMailDomainPort(_ context.Context, _, _ uint, name string, cap domain.MailDomainCapability) (*domain.MailDomain, error) {
	return &domain.MailDomain{ID: 1, Name: name, Capability: cap, Status: domain.MailDomainStatusPending}, f.err
}

func (f *fakeMailDomainPort) SetUpMailDomainPort(_ context.Context, _, id uint) (*domain.MailDomain, error) {
	return &domain.MailDomain{ID: id, Name: "acme.dev", SettingUp: true}, f.err
}

func (f *fakeMailDomainPort) CheckMailDomainPort(_ context.Context, _, id uint) (*domain.MailDomain, error) {
	return &domain.MailDomain{ID: id, Name: "acme.dev", Status: domain.MailDomainStatusVerified}, f.err
}

func (f *fakeMailDomainPort) EnableMailDomainReceivingPort(_ context.Context, _, id uint) (*domain.MailDomain, error) {
	return &domain.MailDomain{ID: id, Name: "acme.dev", Capability: domain.MailDomainSendReceive}, f.err
}

func (f *fakeMailDomainPort) SetDefaultMailDomainPort(_ context.Context, _, id uint) (*domain.MailDomain, error) {
	return &domain.MailDomain{ID: id, Name: "acme.dev", Default: true}, f.err
}

func (f *fakeMailDomainPort) DeleteMailDomainPort(_ context.Context, _, _ uint) error { return f.err }

func (f *fakeMailDomainPort) PreviewMailDomainPort(_ context.Context, name string) (domain.MailDomainPreviewView, error) {
	return domain.MailDomainPreviewView{Name: name, Apex: name, ExistingMX: []string{}, SuggestedCapability: domain.MailDomainSendReceive}, f.err
}

func (f *fakeMailDomainPort) ListMailZonesPort(_ context.Context, _ uint) ([]*domain.MailZone, error) {
	return []*domain.MailZone{{ID: 5, Name: "acme.dev", Status: domain.MailZoneStatusActive}}, f.err
}

func (f *fakeMailDomainPort) GetMailZonePort(_ context.Context, _, id uint) (*domain.MailZone, error) {
	return &domain.MailZone{ID: id, Name: "acme.dev", Status: domain.MailZoneStatusActive}, f.err
}

func (f *fakeMailDomainPort) DeleteMailZonePort(_ context.Context, _, _ uint) error { return f.err }

func newMailDomainTestRouter(t *testing.T, port domain.MailDomainPort, role domain.OrganizationRole) *gin.Engine {
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
			Role:             role,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := NewHandler(port)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	h.RegisterErrorMappings(response.DefaultErrorMapper)
	h.RegisterRoutes(root.Prefix("/v1"))
	return engine
}

func TestMailDomainHandlerFunctionalFlow(t *testing.T) {
	port := &fakeMailDomainPort{}
	engine := newMailDomainTestRouter(t, port, domain.OrganizationRoleOwner)

	body, _ := json.Marshal(map[string]any{"name": "acme.dev"})
	req := httptest.NewRequest(http.MethodPost, "/v1/mail-domains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST mail-domains: status=%d body=%s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodGet, "/v1/mail-domains", nil},
		{http.MethodGet, "/v1/mail-domains/1", nil},
		{http.MethodPost, "/v1/mail-domains/1/setups", nil},
		{http.MethodPost, "/v1/mail-domains/1/checks", nil},
		{http.MethodPost, "/v1/mail-domains/1/receiving", nil},
		{http.MethodPut, "/v1/mail-domains/1/default", nil},
		{http.MethodPost, "/v1/mail-domain-previews", body},
		{http.MethodGet, "/v1/mail-zones", nil},
		{http.MethodGet, "/v1/mail-zones/5", nil},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body))
		if tc.body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: status=%d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	for _, path := range []string{"/v1/mail-domains/1", "/v1/mail-zones/5"} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("DELETE %s: expected 204, got %d", path, rec.Code)
		}
	}

	// Non-manager member cannot connect domain -> 403
	memberEngine := newMailDomainTestRouter(t, port, domain.OrganizationRoleMember)
	req = httptest.NewRequest(http.MethodPost, "/v1/mail-domains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	memberEngine.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for member connecting domain, got %d", rec.Code)
	}
}
