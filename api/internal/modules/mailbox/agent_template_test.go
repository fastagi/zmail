package mailbox

import (
	"net/http"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
)

const agentTemplateBody = `{"name":"Welcome follow-up","subject":"Welcome, {{name}}",` +
	`"text":"Hello {{name}}, how can we help?","sample_variables":{"name":"Alex"}}`

// newAgentTemplateFixture is a member whose key carries templates:create and who holds both the
// templates and the API keys grants.
func newAgentTemplateFixture(t *testing.T) *featureRouteFixture {
	t.Helper()
	f := newFeatureRouteFixture(t)
	f.scopes = []string{apikey.ScopeTemplatesCreate}
	f.viewer.Features = domain.MemberFeatures{Templates: true, APIKeys: true}
	return f
}

func TestAgentMailTemplateCreatesTheTemplateWithoutSending(t *testing.T) {
	f := newAgentTemplateFixture(t)

	recorder, body := f.do(http.MethodPost, "/agent-mail-templates", agentTemplateBody)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	data, _ := body["data"].(map[string]any)
	if data["name"] != "Welcome follow-up" || data["subject"] != "Welcome, {{name}}" {
		t.Fatalf("data = %v", data)
	}
	if len(f.transport.sent) != 0 {
		t.Fatalf("creating a template sent %d messages", len(f.transport.sent))
	}

	recorder, body = f.do(http.MethodPost, "/agent-mail-templates", agentTemplateBody)
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailTemplateNameTaken {
		t.Fatalf("duplicate = %d %v; want 409 %s", recorder.Code, body["error_code"], domain.CodeMailTemplateNameTaken)
	}
	if len(f.transport.sent) != 0 {
		t.Fatalf("a duplicate template sent %d messages", len(f.transport.sent))
	}
}

func TestAgentMailTemplateNeedsTheTemplatesCreateScope(t *testing.T) {
	f := newAgentTemplateFixture(t)
	f.scopes = []string{ScopeMailSend, ScopeMailRead, ScopeMailAgent, ScopeMailTransactional}

	recorder, body := f.do(http.MethodPost, "/agent-mail-templates", agentTemplateBody)
	if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("status = %d %v; want 403 %s", recorder.Code, body["error_code"], domain.CodePermissionDenied)
	}
}

// The key acts as its owner, who needs both the API keys grant and the templates grant, and gets
// the session route's error when either is missing.
func TestAgentMailTemplateFollowsTheOwnersGrants(t *testing.T) {
	for _, features := range []domain.MemberFeatures{
		{Templates: true},
		{APIKeys: true},
		{},
	} {
		f := newAgentTemplateFixture(t)
		f.viewer.Features = features
		recorder, body := f.do(http.MethodPost, "/agent-mail-templates", agentTemplateBody)
		if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodeOrganizationFeatureNotGranted {
			t.Errorf("%+v: status = %d %v; want 403 %s", features, recorder.Code, body["error_code"],
				domain.CodeOrganizationFeatureNotGranted)
		}
	}

	f := newAgentTemplateFixture(t)
	f.viewer.Role = domain.OrganizationRoleAdmin
	f.viewer.Features = domain.MemberFeatures{}
	if recorder, _ := f.do(http.MethodPost, "/agent-mail-templates", agentTemplateBody); recorder.Code != http.StatusCreated {
		t.Fatalf("an admin's key = %d; want 201", recorder.Code)
	}
}
