package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/pkg/response"
)

const fakeDeliveryRoute = "/v1/external-mail-connections/:connection_id/fake-deliveries"

// fakeDeliveryRouter serves the starter's real routes to viewer's session.
func (f *externalFixture) fakeDeliveryRouter(viewer domain.OrganizationContext) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(f.service)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	engine := gin.New()
	routes := router.New(engine).Prefix("/v1")
	routes.MiddlewareGroup("api_key", func(c *gin.Context) { c.Next() })
	routes.MiddlewareGroup("auth", func(c *gin.Context) {
		c.Set("userID", viewer.UserID)
		c.Next()
	})
	routes.MiddlewareGroup("organization_context", func(c *gin.Context) {
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), viewer))
		c.Next()
	})
	handler.RegisterRoutes(routes)
	return engine
}

func hasRoute(engine *gin.Engine, method, path string) bool {
	for _, route := range engine.Routes() {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

func (f *externalFixture) deliverFake(t *testing.T, viewer domain.OrganizationContext, connectionID uint, body string) (int, map[string]any) {
	t.Helper()
	engine := f.fakeDeliveryRouter(viewer)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/external-mail-connections/%d/fake-deliveries", connectionID), bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

// TestFakeDeliveryRouteExistsOnlyWithTheFakeSource: the development delivery route is registered
// while `fake` is the active source, and never with real connectors or with external mail off.
func TestFakeDeliveryRouteExistsOnlyWithTheFakeSource(t *testing.T) {
	for name, providers := range map[string][]string{
		"fake": {config.ExternalMailSourceFake},
		"real": {config.ExternalMailProviderGmail, config.ExternalMailProviderOutlook},
		"imap": {config.ExternalMailProviderIMAP},
		"off":  nil,
	} {
		f := newExternalFixture(t, providers...)
		engine := f.fakeDeliveryRouter(f.b)
		if got, want := hasRoute(engine, http.MethodPost, fakeDeliveryRoute), name == "fake"; got != want {
			t.Fatalf("%s: route registered = %v; want %v", name, got, want)
		}
		if name == "fake" {
			continue
		}
		status, _ := f.deliverFake(t, f.b, f.connection.ID, `{"from":"x@sender.test","subject":"Hi","text":"Hi"}`)
		if status != http.StatusNotFound {
			t.Fatalf("%s: delivery = %d; want 404 for a route that does not exist", name, status)
		}
		if stored := f.storedExternal(t); len(stored) != 0 {
			t.Fatalf("%s: stored %d messages", name, len(stored))
		}
	}
}

// TestFakeDeliveryIsSyncedOnce: a message delivered to the owner's connection is stored once, with
// the connection's provider as its source; delivering the same message id again and a worker pass
// store no second copy. Others and invalid bodies are refused.
func TestFakeDeliveryIsSyncedOnce(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	body := `{"from":"carol@sender.test","subject":"Invoice","text":"Body","html":"<p>Body</p>","message_id":"<inv-1@sender.test>"}`

	status, response := f.deliverFake(t, f.b, f.connection.ID, body)
	data, _ := response["data"].(map[string]any)
	if status != http.StatusCreated || data["message_id"] != "inv-1@sender.test" {
		t.Fatalf("delivery = %d %v", status, response)
	}
	stored := f.storedExternal(t)
	if len(stored) != 1 {
		t.Fatalf("stored %d messages, want 1", len(stored))
	}
	message := stored[0]
	if message.Source != domain.MailSourceGmail || message.Direction != domain.MailDirectionInbound ||
		message.Subject != "Invoice" || message.FromAddress != "carol@sender.test" {
		t.Fatalf("stored message = %+v", message)
	}

	if status, response = f.deliverFake(t, f.b, f.connection.ID, body); status != http.StatusCreated {
		t.Fatalf("repeated delivery = %d %v", status, response)
	}
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if again := f.storedExternal(t); len(again) != 1 {
		t.Fatalf("after a repeated delivery and a worker pass the mailbox holds %d messages, want 1", len(again))
	}

	// Without a message id one is generated, and the message is another one.
	status, response = f.deliverFake(t, f.b, f.connection.ID, `{"from":"dan@sender.test","subject":"Lunch","text":"Noon"}`)
	data, _ = response["data"].(map[string]any)
	if generated, _ := data["message_id"].(string); status != http.StatusCreated || generated == "" {
		t.Fatalf("delivery without an id = %d %v", status, response)
	}
	if after := f.storedExternal(t); len(after) != 2 {
		t.Fatalf("stored %d messages, want 2", len(after))
	}

	// Only the owner may deliver; a missing text is refused.
	if status, response = f.deliverFake(t, f.c, f.connection.ID, body); status != http.StatusNotFound ||
		response["error_code"] != domain.CodeExternalMailConnectionNotFound {
		t.Fatalf("colleague delivery = %d %v", status, response)
	}
	if status, _ = f.deliverFake(t, f.b, f.connection.ID, `{"from":"carol@sender.test","subject":"No text"}`); status != http.StatusUnprocessableEntity {
		t.Fatalf("delivery without text = %d; want 422", status)
	}
	// A revoked connection takes no more mail.
	if err := f.service.RevokeExternalMailConnection(context.Background(), f.b, f.connection.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if status, _ = f.deliverFake(t, f.b, f.connection.ID, body); status != http.StatusNotFound {
		t.Fatalf("delivery to a revoked connection = %d; want 404", status)
	}
}
