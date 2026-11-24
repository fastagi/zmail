package broadcast

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

type fakeBroadcastPort struct {
	err error
}

func (f *fakeBroadcastPort) ListBroadcastsPort(_ context.Context, _ domain.OrganizationContext, _, _ int) ([]*domain.MailBroadcast, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return []*domain.MailBroadcast{{ID: 1, Name: "Launch", Status: domain.MailBroadcastDraft}}, 1, nil
}

func (f *fakeBroadcastPort) GetBroadcastPort(_ context.Context, _ domain.OrganizationContext, id uint) (*domain.MailBroadcast, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.MailBroadcast{ID: id, Name: "Launch", Status: domain.MailBroadcastSending}, nil
}

func (f *fakeBroadcastPort) BroadcastProgressPort(_ context.Context, _ *domain.MailBroadcast) (*domain.MailBroadcastProgress, error) {
	rate := 12.345
	return &domain.MailBroadcastProgress{RatePerMinute: &rate, RateWindow: 5 * time.Minute, ComputedAt: time.Now().UTC()}, nil
}

func (f *fakeBroadcastPort) CreateBroadcastPort(_ context.Context, _ domain.OrganizationContext, input domain.MailBroadcastInput) (*domain.MailBroadcast, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.MailBroadcast{ID: 1, Name: input.Name, MailboxID: input.MailboxID, TemplateID: input.TemplateID, ListID: input.ListID, Status: domain.MailBroadcastDraft}, nil
}

func (f *fakeBroadcastPort) UpdateBroadcastPort(_ context.Context, _ domain.OrganizationContext, id uint, input domain.MailBroadcastInput) (*domain.MailBroadcast, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.MailBroadcast{ID: id, Name: input.Name, Status: domain.MailBroadcastDraft}, nil
}

func (f *fakeBroadcastPort) DeleteBroadcastPort(_ context.Context, _ domain.OrganizationContext, _ uint) error {
	return f.err
}

func (f *fakeBroadcastPort) SendBroadcastPort(_ context.Context, _ domain.OrganizationContext, id uint, _ *time.Time) (*domain.MailBroadcast, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.MailBroadcast{ID: id, Status: domain.MailBroadcastSending}, nil
}

func (f *fakeBroadcastPort) CancelBroadcastPort(_ context.Context, _ domain.OrganizationContext, id uint) (*domain.MailBroadcast, bool, error) {
	return &domain.MailBroadcast{ID: id, Status: domain.MailBroadcastCanceled}, true, f.err
}

func (f *fakeBroadcastPort) PauseBroadcastPort(_ context.Context, _ domain.OrganizationContext, id uint) (*domain.MailBroadcast, bool, error) {
	return &domain.MailBroadcast{ID: id, Status: domain.MailBroadcastPaused}, false, f.err
}

func (f *fakeBroadcastPort) ResumeBroadcastPort(_ context.Context, _ domain.OrganizationContext, id uint) (*domain.MailBroadcast, error) {
	return &domain.MailBroadcast{ID: id, Status: domain.MailBroadcastSending}, f.err
}

func (f *fakeBroadcastPort) ListBroadcastRecipientsPort(_ context.Context, _ domain.OrganizationContext, _ uint, _ domain.MailRecipientStatus, _, _ int) ([]*domain.MailBroadcastRecipient, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return []*domain.MailBroadcastRecipient{{ID: 9, Email: "a@example.com", Status: domain.MailRecipientSent}}, 1, nil
}

func (f *fakeBroadcastPort) UnsubscribePort(_ context.Context, _ string) error {
	return f.err
}

func newBroadcastTestRouter(t *testing.T, port domain.MailBroadcastPort, role domain.OrganizationRole, features domain.MemberFeatures) *gin.Engine {
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
			Features:         features,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := NewHandler(port)
	h.RegisterErrorMappings(response.DefaultErrorMapper)
	h.RegisterRoutes(root.Prefix("/v1"))
	return engine
}

func TestBroadcastHandlerFunctionalFlow(t *testing.T) {
	port := &fakeBroadcastPort{}
	engine := newBroadcastTestRouter(t, port, domain.OrganizationRoleOwner, domain.MemberFeatures{})

	// 1. Create broadcast -> 201
	body, _ := json.Marshal(map[string]any{"name": "Launch", "mailbox_id": 1, "template_id": 2, "list_id": 3})
	req := httptest.NewRequest(http.MethodPost, "/v1/mail-broadcasts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST mail-broadcasts: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 2. List & Get & Update -> 200
	for _, tc := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodGet, "/v1/mail-broadcasts", nil},
		{http.MethodGet, "/v1/mail-broadcasts/1", nil},
		{http.MethodPut, "/v1/mail-broadcasts/1", body},
		{http.MethodPost, "/v1/mail-broadcasts/1/sends", []byte(`{}`)},
		{http.MethodPost, "/v1/mail-broadcasts/1/cancellations", nil},
		{http.MethodPost, "/v1/mail-broadcasts/1/resumptions", nil},
		{http.MethodGet, "/v1/mail-broadcasts/1/recipients", nil},
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

	// 3. Pause (hidden mailbox -> 204), Delete -> 204, Unsubscribe -> 204
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/mail-broadcasts/1/pauses"},
		{http.MethodDelete, "/v1/mail-broadcasts/1"},
		{http.MethodPost, "/v1/mail-unsubscribes/tok-123"},
	} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s %s: expected 204, got %d", tc.method, tc.path, rec.Code)
		}
	}

	// 4. Feature gate: member without broadcasts grant -> 403
	restricted := newBroadcastTestRouter(t, port, domain.OrganizationRoleMember, domain.MemberFeatures{})
	rec = httptest.NewRecorder()
	restricted.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mail-broadcasts", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without broadcasts grant, got %d", rec.Code)
	}
}
