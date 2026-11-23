package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/pkg/response"
)

// featureRouteFixture serves the real route table, as a member of organization 7 whose feature
// grants each test chooses.
type featureRouteFixture struct {
	engine  *gin.Engine
	service *service
	viewer  domain.OrganizationContext
}

func newFeatureRouteFixture(t *testing.T) *featureRouteFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	inbox := newInboxRecorder()
	repo := inbox.fakeRepository
	svc := NewService(inbox, &fakeMemberships{}, repo, newFakeContacts(), &stubBroadcasts{}, nil, nil, nil, nil,
		&fakeTransport{}, &fakeNotifier{}, Settings{Domain: "mail.example", InboundTolerance: 5 * time.Minute, DailySendLimit: 5},
		nil, nil, nil, nil)
	handler := NewHandler(svc)
	response.DefaultErrorMapper.Register(domain.ErrInvalidInput, http.StatusBadRequest, domain.CodeInvalidInput)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)

	// Recovery turns a nil test dependency behind an allowed route into a 500: these tests only
	// ask whether the feature gate let the request through.
	engine := gin.New()
	engine.Use(gin.RecoveryWithWriter(io.Discard))
	f := &featureRouteFixture{engine: engine, service: svc, viewer: viewerOf(7, handlerUser, domain.OrganizationRoleMember)}
	f.viewer.Features = domain.MemberFeatures{}
	routes := router.New(f.engine)
	routes.MiddlewareGroup("auth", func(c *gin.Context) { c.Set("userID", uint(handlerUser)); c.Next() })
	routes.MiddlewareGroup("api_key", func(c *gin.Context) {
		c.Set("apiKeyID", uint(41))
		c.Set("apiKeyScopes", []string{ScopeMailSend, ScopeMailRead, ScopeMailAgent, ScopeMailTransactional})
		c.Set("apiKeyMode", domain.APIKeyModeLive)
		c.Set("userID", uint(handlerUser))
		c.Next()
	})
	routes.MiddlewareGroup("organization_context", func(c *gin.Context) {
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), f.viewer))
		c.Next()
	})
	handler.RegisterRoutes(routes)
	return f
}

func (f *featureRouteFixture) do(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	f.engine.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder, decoded
}

type featureRoute struct {
	method, path, body string
}

// featureGatedRoutes lists, per feature, every route a member needs that feature for.
var featureGatedRoutes = map[domain.MemberFeature][]featureRoute{
	domain.MemberFeatureBroadcasts: {
		{http.MethodGet, "/mail-broadcasts", ""},
		{http.MethodPost, "/mail-broadcasts", `{}`},
		{http.MethodGet, "/mail-broadcasts/1", ""},
		{http.MethodPut, "/mail-broadcasts/1", `{}`},
		{http.MethodDelete, "/mail-broadcasts/1", ""},
		{http.MethodPost, "/mail-broadcasts/1/sends", `{}`},
		{http.MethodPost, "/mail-broadcasts/1/cancellations", ""},
		{http.MethodPost, "/mail-broadcasts/1/pauses", ""},
		{http.MethodPost, "/mail-broadcasts/1/resumptions", ""},
		{http.MethodGet, "/mail-broadcasts/1/recipients", ""},
	},
	domain.MemberFeatureContacts: {
		{http.MethodGet, "/mail-contacts", ""},
		{http.MethodPost, "/mail-contacts", `{}`},
		{http.MethodGet, "/mail-contacts/1", ""},
		{http.MethodPut, "/mail-contacts/1", `{}`},
		{http.MethodDelete, "/mail-contacts/1", ""},
		{http.MethodPost, "/mail-contact-imports", ""},
		{http.MethodPost, "/mail-contact-lists", `{}`},
		{http.MethodPut, "/mail-contact-lists/1", `{}`},
		{http.MethodDelete, "/mail-contact-lists/1", ""},
	},
	domain.MemberFeatureTemplates: {
		{http.MethodPost, "/mail-templates", `{}`},
		{http.MethodPut, "/mail-templates/1", `{}`},
		{http.MethodDelete, "/mail-templates/1", ""},
		{http.MethodPost, "/mail-templates/1/tests", `{}`},
	},
	domain.MemberFeatureAPIKeys: {
		{http.MethodPost, "/outbound-mail-messages", `{}`},
		{http.MethodGet, "/outbound-mail-messages/1", ""},
		{http.MethodGet, "/agent-mail-events", ""},
		{http.MethodGet, "/agent-mail-messages/1", ""},
		{http.MethodPost, "/agent-mail-drafts", `{}`},
		{http.MethodPost, "/transactional-messages", `{}`},
		{http.MethodGet, "/transactional-messages/1", ""},
	},
}

func featuresWith(feature domain.MemberFeature) domain.MemberFeatures {
	var features domain.MemberFeatures
	switch feature {
	case domain.MemberFeatureBroadcasts:
		features.Broadcasts = true
	case domain.MemberFeatureContacts:
		features.Contacts = true
	case domain.MemberFeatureTemplates:
		features.Templates = true
	case domain.MemberFeatureAPIKeys:
		features.APIKeys = true
	}
	return features
}

func TestMembersWithoutAGrantGetFeatureNotGranted(t *testing.T) {
	f := newFeatureRouteFixture(t)
	for feature, routes := range featureGatedRoutes {
		for _, route := range routes {
			recorder, body := f.do(route.method, route.path, route.body)
			if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodeOrganizationFeatureNotGranted {
				t.Errorf("%s %s without %s = %d %v; want 403 %s", route.method, route.path, feature,
					recorder.Code, body["error_code"], domain.CodeOrganizationFeatureNotGranted)
			}
		}
	}
}

func TestAGrantOpensOnlyItsOwnRoutes(t *testing.T) {
	f := newFeatureRouteFixture(t)
	for granted := range featureGatedRoutes {
		f.viewer.Features = featuresWith(granted)
		for feature, routes := range featureGatedRoutes {
			for _, route := range routes {
				_, body := f.do(route.method, route.path, route.body)
				denied := body["error_code"] == domain.CodeOrganizationFeatureNotGranted
				if feature == granted && denied {
					t.Errorf("%s %s with %s was refused", route.method, route.path, granted)
				}
				if feature != granted && !denied {
					t.Errorf("%s %s with only %s was not refused as %s", route.method, route.path, granted, feature)
				}
			}
		}
	}
}

func TestManagersNeedNoGrant(t *testing.T) {
	f := newFeatureRouteFixture(t)
	for _, role := range []domain.OrganizationRole{domain.OrganizationRoleOwner, domain.OrganizationRoleAdmin} {
		f.viewer.Role = role
		for _, routes := range featureGatedRoutes {
			for _, route := range routes {
				_, body := f.do(route.method, route.path, route.body)
				if body["error_code"] == domain.CodeOrganizationFeatureNotGranted {
					t.Errorf("%s: %s %s was refused", role, route.method, route.path)
				}
			}
		}
	}
}

// Composing a broadcast needs the organization's templates and list names, so a broadcast grant
// reads them; editing either still needs its own grant.
func TestBroadcastGrantReadsTemplatesAndListNames(t *testing.T) {
	f := newFeatureRouteFixture(t)
	reads := []featureRoute{
		{http.MethodGet, "/mail-templates", ""},
		{http.MethodGet, "/mail-templates/1", ""},
		{http.MethodPost, "/mail-template-previews", `{}`},
		{http.MethodGet, "/mail-contact-lists", ""},
	}
	for _, route := range reads {
		recorder, body := f.do(route.method, route.path, route.body)
		if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodeOrganizationFeatureNotGranted {
			t.Errorf("%s %s with no grant = %d %v", route.method, route.path, recorder.Code, body["error_code"])
		}
	}
	for _, features := range []domain.MemberFeatures{{Broadcasts: true}, {Templates: true, Contacts: true}} {
		f.viewer.Features = features
		for _, route := range reads {
			if _, body := f.do(route.method, route.path, route.body); body["error_code"] == domain.CodeOrganizationFeatureNotGranted {
				t.Errorf("%s %s with %+v was refused", route.method, route.path, features)
			}
		}
	}
}

// A member with the broadcast grant may stop only broadcasts they could send; managers stop any.
func TestGrantedMembersStopOnlyBroadcastsTheyCanSend(t *testing.T) {
	f := newFeatureRouteFixture(t)
	f.viewer.Features = domain.MemberFeatures{Broadcasts: true}
	broadcasts := &recordingBroadcasts{broadcast: &domain.MailBroadcast{
		ID: 9, OrganizationID: 7, MailboxID: 404, Status: domain.MailBroadcastSending,
	}}
	f.service.broadcasts = broadcasts

	_, _, err := f.service.CancelBroadcast(context.Background(), f.viewer, 9)
	if err == nil {
		t.Fatal("a member without send on the sender mailbox canceled the broadcast")
	}
	if broadcasts.canceled {
		t.Fatal("the broadcast was canceled before access was checked")
	}
	_, _, err = f.service.PauseBroadcast(context.Background(), f.viewer, 9)
	if err == nil || broadcasts.paused {
		t.Fatalf("a member without send on the sender mailbox paused the broadcast: %v", err)
	}
}

// recordingBroadcasts finds one broadcast and records whether it was stopped.
type recordingBroadcasts struct {
	stubBroadcasts
	broadcast        *domain.MailBroadcast
	canceled, paused bool
}

func (r *recordingBroadcasts) FindBroadcast(context.Context, uint, uint) (*domain.MailBroadcast, error) {
	copied := *r.broadcast
	return &copied, nil
}

func (r *recordingBroadcasts) CancelBroadcast(context.Context, uint, uint) error {
	r.canceled = true
	return nil
}

func (r *recordingBroadcasts) PauseBroadcast(context.Context, uint, uint, string) error {
	r.paused = true
	return nil
}
