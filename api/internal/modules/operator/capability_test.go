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

	grant, change, err := svc.GrantOperator(context.Background(), "viewer@example.test", domain.OperatorScopeMailMetrics)
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantCreated, change)
	assert.Equal(t, domain.OperatorScopeMailMetrics, grant.Scope)
	_, change, err = svc.GrantOperator(context.Background(), "viewer@example.test", domain.OperatorScopeMailMetrics)
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantUnchanged, change)
	_, change, err = svc.GrantOperator(context.Background(), "viewer@example.test", domain.OperatorScopeFull)
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantScopeChanged, change)
	_, _, err = svc.GrantOperator(context.Background(), "viewer@example.test", "superuser")
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
