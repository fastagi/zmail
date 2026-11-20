package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/internal/modules/mailbox"
)

type emptyDirectory struct{}

func (emptyDirectory) PlatformMemberTotals(context.Context) (domain.PlatformMemberTotals, error) {
	return domain.PlatformMemberTotals{}, nil
}

func (emptyDirectory) PlatformOrganizations(context.Context, string) ([]domain.PlatformOrganizationRow, error) {
	return nil, nil
}

func (emptyDirectory) PlatformOrganizationNames(context.Context, []uint) (map[uint]string, error) {
	return map[uint]string{}, nil
}

// newCapabilityFixture mounts the real operator route tree with the mailbox and organization
// starters selected. The mail metrics store has no database, so a request the capability check lets
// through fails in the store (a 5xx), never with 401, 403, or 404.
func newCapabilityFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	cfg := &config.Config{Operator: config.OperatorConfig{AllowedOrigins: []string{testOrigin}}}
	cfg.App.Env = "production"
	cfg.Starters.Optional = []string{config.StarterOperator, config.StarterOrganization, config.StarterMailbox}
	handler := NewHandler(f.handler.service, nil, cfg, Surfaces{
		Organizations: nil,
		MailMetrics:   mailbox.NewOperatorMetricsHandler(nil, emptyDirectory{}),
	})
	// Mail metrics mount only beside the organization surface; give it one without routes of its own.
	handler.mailMetrics = mailbox.NewOperatorMetricsHandler(nil, emptyDirectory{})
	users := f.handler.service.users.(*fakeUsers)
	users.byID[3] = &domain.User{ID: 3, Username: "viewer", Status: 1}
	users.byID[4] = &domain.User{ID: 4, Username: "owner", Status: 1}
	f.engine = gin.New()
	handler.RegisterRoutes(router.New(f.engine).Prefix("/v1"))
	f.handler = handler
	return f
}

func TestMailMetricsNeedOnlyTheMetricsCapability(t *testing.T) {
	// passed marks a request the authorization let through to the database-less store.
	const passed = -1
	const (
		fullOperator  = uint(1)
		member        = uint(2)
		metricsViewer = uint(3)
		orgOwner      = uint(4)
	)
	metricsRoutes := []string{
		"/v1/operator/mail-metrics",
		"/v1/operator/mail-metrics/organizations",
		"/v1/operator/outbound-messages",
	}
	manageRoutes := []string{"/v1/operator/users", "/v1/operator/audit-logs"}

	tests := []struct {
		name        string
		userID      uint
		scope       domain.OperatorScope
		granted     bool
		metricsCode int
		metricsErr  string
		manageCode  int
		manageErr   string
	}{
		{name: "normal member without a grant", userID: member,
			metricsCode: http.StatusForbidden, metricsErr: domain.CodeOperatorForbidden,
			manageCode: http.StatusForbidden, manageErr: domain.CodeOperatorForbidden},
		{name: "organization owner gets nothing from the role", userID: orgOwner,
			metricsCode: http.StatusForbidden, metricsErr: domain.CodeOperatorForbidden,
			manageCode: http.StatusForbidden, manageErr: domain.CodeOperatorForbidden},
		{name: "metrics-only grant reads metrics but cannot manage", userID: metricsViewer,
			scope: domain.OperatorScopeMailMetrics, granted: true,
			metricsCode: passed,
			manageCode:  http.StatusForbidden, manageErr: domain.CodeOperatorCapability},
		{name: "full operator reads metrics and manages", userID: fullOperator,
			scope: domain.OperatorScopeFull, granted: true,
			metricsCode: passed,
			manageCode:  http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newCapabilityFixture(t)
			f.grants.operators = map[uint]bool{}
			if test.granted {
				f.grants.operators[test.userID] = true
				f.grants.scopes = map[uint]domain.OperatorScope{test.userID: test.scope}
			}
			f.auth.identity = &domain.AuthenticationIdentity{UserID: test.userID, Audience: domain.SessionAudienceOperator}
			for _, path := range metricsRoutes {
				recorder := f.do(t, call{method: http.MethodGet, path: path, cookie: testCredential})
				if test.metricsCode == passed {
					assert.GreaterOrEqual(t, recorder.Code, http.StatusInternalServerError, path)
					continue
				}
				assert.Equal(t, test.metricsCode, recorder.Code, path)
				if test.metricsErr != "" {
					assert.Equal(t, test.metricsErr, errorCode(t, recorder), path)
				}
			}
			for _, path := range manageRoutes {
				recorder := f.do(t, call{method: http.MethodGet, path: path, cookie: testCredential})
				assert.Equal(t, test.manageCode, recorder.Code, path)
				if test.manageErr != "" {
					assert.Equal(t, test.manageErr, errorCode(t, recorder), path)
				}
			}
		})
	}
}

func TestMailMetricsRejectSessionsFromPublicLogin(t *testing.T) {
	f := newCapabilityFixture(t)
	f.auth.identity = &domain.AuthenticationIdentity{UserID: 1, Audience: domain.SessionAudienceUser}
	recorder := f.do(t, call{method: http.MethodGet, path: "/v1/operator/mail-metrics", cookie: testCredential})
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	recorder = f.do(t, call{method: http.MethodGet, path: "/v1/operator/mail-metrics"})
	assert.Equal(t, http.StatusUnauthorized, recorder.Code, "no cookie")
}

func TestSessionReportsCapabilities(t *testing.T) {
	f := newFixture(t)
	f.grants.scopes = map[uint]domain.OperatorScope{1: domain.OperatorScopeMailMetrics}
	recorder := f.do(t, call{method: http.MethodPost, path: "/v1/operator/session", origin: testOrigin,
		body: `{"identifier":"ops","password":"secret"}`})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, []string{"mail_metrics:read"}, sessionCapabilities(t, recorder.Body.Bytes()),
		"a metrics-only grant may sign in")

	f.grants.scopes[1] = domain.OperatorScopeFull
	recorder = f.do(t, call{method: http.MethodGet, path: "/v1/operator/session", cookie: testCredential})
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, []string{"operator:manage", "mail_metrics:read"}, sessionCapabilities(t, recorder.Body.Bytes()),
		"a scope change applies on the next request")
}

func TestUnknownStoredScopeAllowsNothing(t *testing.T) {
	f := newFixture(t)
	f.grants.scopes = map[uint]domain.OperatorScope{1: "superuser"}
	recorder := f.do(t, call{method: http.MethodGet, path: "/v1/operator/session", cookie: testCredential})
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, domain.CodeOperatorForbidden, errorCode(t, recorder))
}

func TestGrantOperatorScopes(t *testing.T) {
	f := newFixture(t)
	users := &fakeUsers{byID: map[uint]*domain.User{}}
	viewer := &domain.User{ID: 7, Email: "viewer@example.test"}
	users.byID[7] = viewer
	svc := f.handler.service
	svc.users = emailUsers{fakeUsers: users, byEmail: map[string]*domain.User{"viewer@example.test": viewer}}

	metrics := domain.OperatorAccess{Scope: domain.OperatorScopeMailMetrics}
	grant, change, err := svc.GrantOperator(context.Background(), "viewer@example.test", metrics)
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantCreated, change)
	assert.Equal(t, domain.OperatorScopeMailMetrics, grant.Scope)
	_, change, err = svc.GrantOperator(context.Background(), "viewer@example.test", metrics)
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantUnchanged, change)
	_, change, err = svc.GrantOperator(context.Background(), "viewer@example.test", domain.OperatorAccess{Scope: domain.OperatorScopeFull})
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantScopeChanged, change)
	grant, change, err = svc.GrantOperator(context.Background(), "viewer@example.test",
		domain.OperatorAccess{Scope: domain.OperatorScopeFull, MailContent: true})
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantScopeChanged, change)
	assert.True(t, grant.MailContent)
	_, _, err = svc.GrantOperator(context.Background(), "viewer@example.test", domain.OperatorAccess{Scope: "superuser", MailContent: true})
	require.ErrorIs(t, err, domain.ErrInvalidInput)
}

type emailUsers struct {
	*fakeUsers
	byEmail map[string]*domain.User
}

func (u emailUsers) FindByEmail(_ context.Context, email string) (*domain.User, error) {
	if user, ok := u.byEmail[email]; ok {
		return user, nil
	}
	return nil, domain.ErrUserNotFound
}

func sessionCapabilities(t *testing.T, body []byte) []string {
	t.Helper()
	var envelope struct {
		Data struct {
			Capabilities []string `json:"capabilities"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	return envelope.Data.Capabilities
}

// Spec acceptance row 4: a normal member, an organization owner, a metrics-only grant, and a full
// grant without the content add-on cannot read a broadcast body; only a grant with content can,
// and removing content or revoking the grant makes the next read fail.
func TestBroadcastContentNeedsTheExplicitContentCapability(t *testing.T) {
	// passed marks a request the authorization let through to the database-less store.
	const passed = -1
	const auditsPath = "/v1/operator/content-access-audits"
	tests := []struct {
		name       string
		userID     uint
		granted    bool
		scope      domain.OperatorScope
		content    bool
		readCode   int
		readErr    string
		auditsCode int
		auditsErr  string
	}{
		{name: "normal member without a grant", userID: 2,
			readCode: http.StatusForbidden, readErr: domain.CodeOperatorForbidden,
			auditsCode: http.StatusForbidden, auditsErr: domain.CodeOperatorForbidden},
		{name: "organization owner gets nothing from the role", userID: 4,
			readCode: http.StatusForbidden, readErr: domain.CodeOperatorForbidden,
			auditsCode: http.StatusForbidden, auditsErr: domain.CodeOperatorForbidden},
		{name: "metrics-only grant", userID: 3, granted: true, scope: domain.OperatorScopeMailMetrics,
			readCode: http.StatusForbidden, readErr: domain.CodeOperatorCapability,
			auditsCode: http.StatusForbidden, auditsErr: domain.CodeOperatorCapability},
		{name: "full grant without content reads the audit but no body", userID: 1, granted: true,
			scope:    domain.OperatorScopeFull,
			readCode: http.StatusForbidden, readErr: domain.CodeOperatorCapability,
			auditsCode: passed},
		{name: "metrics grant with content", userID: 3, granted: true, scope: domain.OperatorScopeMailMetrics,
			content: true, readCode: passed, auditsCode: passed},
		{name: "full grant with content", userID: 1, granted: true, scope: domain.OperatorScopeFull,
			content: true, readCode: passed, auditsCode: passed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newCapabilityFixture(t)
			f.grants.operators = map[uint]bool{}
			if test.granted {
				f.grants.operators[test.userID] = true
				f.grants.scopes = map[uint]domain.OperatorScope{test.userID: test.scope}
				f.grants.content = map[uint]bool{test.userID: test.content}
			}
			f.auth.identity = &domain.AuthenticationIdentity{UserID: test.userID, Audience: domain.SessionAudienceOperator}

			read := f.do(t, contentRead())
			if test.readCode == passed {
				assert.GreaterOrEqual(t, read.Code, http.StatusInternalServerError)
			} else {
				assert.Equal(t, test.readCode, read.Code)
				assert.Equal(t, test.readErr, errorCode(t, read))
			}
			audits := f.do(t, call{method: http.MethodGet, path: auditsPath, cookie: testCredential})
			if test.auditsCode == passed {
				assert.GreaterOrEqual(t, audits.Code, http.StatusInternalServerError)
			} else {
				assert.Equal(t, test.auditsCode, audits.Code)
				assert.Equal(t, test.auditsErr, errorCode(t, audits))
			}
		})
	}
}

func TestRemovingContentFailsTheNextRead(t *testing.T) {
	f := newCapabilityFixture(t)
	f.grants.operators = map[uint]bool{3: true}
	f.grants.scopes = map[uint]domain.OperatorScope{3: domain.OperatorScopeMailMetrics}
	f.grants.content = map[uint]bool{3: true}
	f.auth.identity = &domain.AuthenticationIdentity{UserID: 3, Audience: domain.SessionAudienceOperator}

	recorder := f.do(t, call{method: http.MethodGet, path: "/v1/operator/session", cookie: testCredential})
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, []string{"mail_metrics:read", "mail_content:read"}, sessionCapabilities(t, recorder.Body.Bytes()))
	assert.GreaterOrEqual(t, f.do(t, contentRead()).Code, http.StatusInternalServerError, "the content grant passes")

	f.grants.content[3] = false
	recorder = f.do(t, contentRead())
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, domain.CodeOperatorCapability, errorCode(t, recorder), "re-granting without content applies on the next request")

	f.grants.content[3] = true
	delete(f.grants.operators, 3)
	recorder = f.do(t, contentRead())
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, domain.CodeOperatorForbidden, errorCode(t, recorder), "a revoked grant reads nothing")
}

func TestBroadcastContentIsAnUnsafeRequest(t *testing.T) {
	f := newCapabilityFixture(t)
	f.grants.operators = map[uint]bool{1: true}
	f.grants.content = map[uint]bool{1: true}
	read := contentRead()
	read.csrf = ""
	recorder := f.do(t, read)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, domain.CodeOperatorCSRFRejected, errorCode(t, recorder))
}

// contentPath is a broadcast body read; the database-less store never resolves it.
const contentPath = "/v1/operator/outbound-messages/1/2/content"

func contentRead() call {
	return call{method: http.MethodPost, path: contentPath, body: `{"reason":"Complaint ticket 42"}`,
		origin: testOrigin, cookie: testCredential, csrf: csrfToken(testCredential)}
}
