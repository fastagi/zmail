package roster

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/pkg/response"
)

func newTestRouter(t *testing.T, svc Service, orgCtx domain.OrganizationContext) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	root := router.New(engine)
	root.MiddlewareGroup("auth", func(c *gin.Context) { c.Next() })
	root.MiddlewareGroup("organization_context", func(c *gin.Context) {
		ctx := domain.WithOrganizationContext(c.Request.Context(), orgCtx)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := NewHandler(svc)
	h.RegisterErrorMappings(response.DefaultErrorMapper)
	h.RegisterRoutes(root.Prefix("/v1"))
	return engine
}

func doJSON(t *testing.T, engine *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func testOrgContext(orgID, userID uint, role domain.OrganizationRole, features domain.MemberFeatures) domain.OrganizationContext {
	return domain.OrganizationContext{
		OrganizationID:   orgID,
		OrganizationName: "Acme",
		OrganizationSlug: "acme",
		MembershipID:     100 + userID,
		UserID:           userID,
		Role:             role,
		Features:         features,
	}
}

func TestHandlerEndToEndFunctionalFlow(t *testing.T) {
	repo := newMemoryContactRepo()
	svc := NewService(repo)
	ownerCtx := testOrgContext(42, 1, domain.OrganizationRoleOwner, domain.MemberFeatures{})
	engine := newTestRouter(t, svc, ownerCtx)

	// 1. Create list -> 201
	rec := doJSON(t, engine, http.MethodPost, "/v1/mail-contact-lists", map[string]any{"name": "Launch"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/mail-contact-lists: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("missing private cache header: %q", rec.Header().Get("Cache-Control"))
	}
	var listResp struct {
		Data MailContactListResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	listID := listResp.Data.ID
	if listID == 0 || listResp.Data.Name != "Launch" {
		t.Fatalf("unexpected list response: %+v", listResp.Data)
	}

	// 2. Rename list -> 200
	rec = doJSON(t, engine, http.MethodPut, "/v1/mail-contact-lists/1", map[string]any{"name": "Early Access"})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /v1/mail-contact-lists/1: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. Create contact -> 201
	rec = doJSON(t, engine, http.MethodPost, "/v1/mail-contacts", map[string]any{
		"email":      "alice@example.com",
		"first_name": "Alice",
		"last_name":  "Zhang",
		"properties": map[string]string{"tier": "founder"},
		"list_ids":   []uint{listID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/mail-contacts: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var contactResp struct {
		Data MailContactResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &contactResp); err != nil {
		t.Fatalf("unmarshal contact: %v", err)
	}
	contactID := contactResp.Data.ID

	// 4. Duplicate contact -> 409 MAILBOX.CONTACT_EXISTS
	rec = doJSON(t, engine, http.MethodPost, "/v1/mail-contacts", map[string]any{"email": "alice@example.com"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate contact, got %d body=%s", rec.Code, rec.Body.String())
	}

	// 5. Import CSV -> 200
	rec = doJSON(t, engine, http.MethodPost, "/v1/mail-contact-imports", map[string]any{
		"csv":     "email,first_name\nbob@example.com,Bob\nbad-email,X\n",
		"list_id": listID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/mail-contact-imports: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var importResp struct {
		Data MailContactImportResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &importResp); err != nil {
		t.Fatalf("unmarshal import: %v", err)
	}
	if importResp.Data.Created != 1 || importResp.Data.SkippedCount != 1 {
		t.Fatalf("unexpected import response: %+v", importResp.Data)
	}

	// 6. List contacts with filter -> 200
	rec = doJSON(t, engine, http.MethodGet, "/v1/mail-contacts?q=bob&list_id=1&status=subscribed", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/mail-contacts: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 7. Get & Update contact -> 200
	rec = doJSON(t, engine, http.MethodGet, "/v1/mail-contacts/2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/mail-contacts/2: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, engine, http.MethodPut, "/v1/mail-contacts/2", map[string]any{
		"first_name": "Alice Updated",
		"status":     "unsubscribed",
		"list_ids":   []uint{listID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /v1/mail-contacts/2: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 8. List contact lists shows count -> 200
	rec = doJSON(t, engine, http.MethodGet, "/v1/mail-contact-lists", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/mail-contact-lists: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 9. Delete contact -> 204
	_ = contactID
	rec = doJSON(t, engine, http.MethodDelete, "/v1/mail-contacts/2", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/mail-contacts/2: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 10. Delete list -> 204
	rec = doJSON(t, engine, http.MethodDelete, "/v1/mail-contact-lists/1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/mail-contact-lists/1: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerFeatureGateAuthorization(t *testing.T) {
	svc := NewService(newMemoryContactRepo())

	// Plain member with broadcasts grant only: can read contact lists, cannot mutate or read contacts.
	memberBroadcastOnly := testOrgContext(1, 2, domain.OrganizationRoleMember, domain.MemberFeatures{Broadcasts: true})
	engine := newTestRouter(t, svc, memberBroadcastOnly)

	if rec := doJSON(t, engine, http.MethodGet, "/v1/mail-contact-lists", nil); rec.Code != http.StatusOK {
		t.Fatalf("broadcasts member should read contact lists, got %d", rec.Code)
	}
	if rec := doJSON(t, engine, http.MethodPost, "/v1/mail-contact-lists", map[string]any{"name": "X"}); rec.Code != http.StatusForbidden {
		t.Fatalf("broadcasts-only member should be forbidden from creating list, got %d", rec.Code)
	}
	if rec := doJSON(t, engine, http.MethodGet, "/v1/mail-contacts", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("broadcasts-only member should be forbidden from reading contacts, got %d", rec.Code)
	}
}

func TestHandlerConcurrentRequests(t *testing.T) {
	svc := NewService(newMemoryContactRepo())
	engine := newTestRouter(t, svc, testOrgContext(7, 3, domain.OrganizationRoleAdmin, domain.MemberFeatures{}))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := doJSON(t, engine, http.MethodGet, "/v1/mail-contacts", nil)
			if rec.Code != http.StatusOK {
				t.Errorf("concurrent GET /v1/mail-contacts failed: %d", rec.Code)
			}
		}()
	}
	wg.Wait()
}
