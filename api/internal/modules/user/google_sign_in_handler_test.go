package user

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/pkg/response"
)

func googleHandlerEngine(t *testing.T, svc *service, limits config.AuthenticationRateLimitConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := NewHandler(svc, svc, svc, nil, nil, newAuthAbuseGuard(limits), svc)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	engine := gin.New()
	routes := router.New(engine).Prefix("/v1")
	handler.RegisterMiddleware(routes)
	handler.RegisterRoutes(routes)
	return engine
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) (map[string]any, string) {
	t.Helper()
	var body struct {
		Data      map[string]any `json:"data"`
		ErrorCode string         `json:"error_code"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), recorder.Body.String())
	return body.Data, body.ErrorCode
}

func TestGoogleSignInRoutesWhenNotConfigured(t *testing.T) {
	engine := googleHandlerEngine(t, NewService(nil, nil, nil, nil, nil, nil), config.AuthenticationRateLimitConfig{})

	providers := httptest.NewRecorder()
	engine.ServeHTTP(providers, httptest.NewRequest(http.MethodGet, "/v1/auth/providers", nil))
	require.Equal(t, http.StatusOK, providers.Code, providers.Body.String())
	data, _ := decodeEnvelope(t, providers)
	assert.Equal(t, map[string]any{"enabled": false}, data["google"], "the button stays hidden")

	for path, body := range map[string]string{
		"/v1/auth/google/authorizations": `{"redirect_uri":"https://app.example/api/auth/google/callback"}`,
		"/v1/auth/google/sessions":       `{"state":"s","code":"c"}`,
	} {
		result := performAuthRequest(engine, path, body, "198.51.100.20:1000")
		require.Equal(t, http.StatusServiceUnavailable, result.Code, path)
		_, code := decodeEnvelope(t, result)
		assert.Equal(t, "AUTH.GOOGLE_NOT_CONFIGURED", code, path)
	}
}

func TestGoogleSignInRoutesWhenConfigured(t *testing.T) {
	f := newGoogleSignInFixture(t)
	engine := googleHandlerEngine(t, f.svc, config.AuthenticationRateLimitConfig{})

	providers := httptest.NewRecorder()
	engine.ServeHTTP(providers, httptest.NewRequest(http.MethodGet, "/v1/auth/providers", nil))
	data, _ := decodeEnvelope(t, providers)
	assert.Equal(t, map[string]any{"enabled": true}, data["google"])

	started := performAuthRequest(engine, "/v1/auth/google/authorizations",
		`{"redirect_uri":"`+googleTestRedirectURI+`","return_url":"/console/admin/domains"}`, "198.51.100.21:1000")
	require.Equal(t, http.StatusCreated, started.Code, started.Body.String())
	data, _ = decodeEnvelope(t, started)
	state, _ := data["state"].(string)
	authorizationURL, _ := data["authorization_url"].(string)
	require.NotEmpty(t, state)
	assert.EqualValues(t, googleSignInStateTTL/time.Second, data["expires_in"])

	code := f.google.approve(t, authorizationURL, googleTestIdentity{Subject: "h1", Email: "handler@example.com", EmailVerified: true})
	completed := performAuthRequest(engine, "/v1/auth/google/sessions",
		`{"state":"`+state+`","code":"`+code+`"}`, "198.51.100.21:1000")
	require.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	data, _ = decodeEnvelope(t, completed)
	assert.Equal(t, "Bearer", data["token_type"])
	assert.Equal(t, "/console/admin/domains", data["return_url"])
	user, _ := data["user"].(map[string]any)
	assert.Equal(t, "handler@example.com", user["email"])
	assert.NotContains(t, user, "email_verified_at", "the API user shape is unchanged")

	replay := performAuthRequest(engine, "/v1/auth/google/sessions",
		`{"state":"`+state+`","code":"`+code+`"}`, "198.51.100.21:1000")
	require.Equal(t, http.StatusBadRequest, replay.Code)
	_, errorCode := decodeEnvelope(t, replay)
	assert.Equal(t, "AUTH.GOOGLE_STATE_INVALID", errorCode)
}

func TestGoogleSignInRoutesShareTheLoginSourceQuota(t *testing.T) {
	engine := googleHandlerEngine(t, NewService(nil, nil, nil, nil, nil, nil), config.AuthenticationRateLimitConfig{
		Enabled: true,
		Login: config.AuthenticationEndpointRateLimitConfig{
			PerIP: config.RateLimitRuleConfig{Max: 1, Window: time.Minute},
		},
	})
	first := performAuthRequest(engine, "/v1/auth/google/authorizations", `{"redirect_uri":"https://a.example/cb"}`, "198.51.100.30:1000")
	assert.Equal(t, http.StatusServiceUnavailable, first.Code)
	second := performAuthRequest(engine, "/v1/auth/google/sessions", `{"state":"s","code":"c"}`, "198.51.100.30:1000")
	assert.Equal(t, http.StatusTooManyRequests, second.Code)
}
