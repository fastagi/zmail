package apikey

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestRequireScopes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		scopes    []string
		setScopes bool
		wantCode  int
		wantError string
	}{
		{
			name:      "all required scopes",
			scopes:    []string{"models:read", "models:invoke"},
			setScopes: true,
			wantCode:  http.StatusNoContent,
		},
		{
			name:      "wildcard",
			scopes:    []string{"*"},
			setScopes: true,
			wantCode:  http.StatusNoContent,
		},
		{
			name:      "scope denied",
			scopes:    []string{"models:read"},
			setScopes: true,
			wantCode:  http.StatusForbidden,
			wantError: domain.CodePermissionDenied,
		},
		{
			name:      "authentication missing",
			wantCode:  http.StatusUnauthorized,
			wantError: "AUTH.UNAUTHORIZED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := gin.New()
			engine.GET("/resource", func(c *gin.Context) {
				if tt.setScopes {
					c.Set(apiKeyScopesContextKey, tt.scopes)
				}
				c.Next()
			}, RequireScopes("models:read", "models:invoke"), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			responseRecorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/resource", nil)
			engine.ServeHTTP(responseRecorder, request)

			assert.Equal(t, tt.wantCode, responseRecorder.Code)
			if tt.wantError != "" {
				var body struct {
					ErrorCode string `json:"error_code"`
				}
				require.NoError(t, json.Unmarshal(responseRecorder.Body.Bytes(), &body))
				assert.Equal(t, tt.wantError, body.ErrorCode)
			}
		})
	}
}

func TestRequireScopesRejectsInvalidConfiguration(t *testing.T) {
	assert.Panics(t, func() { RequireScopes() })
	assert.Panics(t, func() { RequireScopes("models") })
}

func serveScopedRoute(t *testing.T, middleware gin.HandlerFunc, mode domain.APIKeyMode, scopes []string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/resource", func(c *gin.Context) {
		c.Set(apiKeyScopesContextKey, scopes)
		if mode != "" {
			c.Set(apiKeyModeContextKey, mode)
		}
		c.Next()
	}, middleware, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource", nil))
	return recorder
}

func TestRequireScopesRefusesTestKey(t *testing.T) {
	for _, scopes := range [][]string{{"mail:send"}, {"*"}} {
		recorder := serveScopedRoute(t, RequireScopes("mail:send"), domain.APIKeyModeTest, scopes)

		assert.Equal(t, http.StatusForbidden, recorder.Code)
		var body struct {
			ErrorCode string `json:"error_code"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		assert.Equal(t, domain.CodePermissionDenied, body.ErrorCode)
	}

	// A live key, or a context without a mode, still passes on scopes alone.
	assert.Equal(t, http.StatusNoContent, serveScopedRoute(t, RequireScopes("mail:send"), domain.APIKeyModeLive, []string{"mail:send"}).Code)
	assert.Equal(t, http.StatusNoContent, serveScopedRoute(t, RequireScopes("mail:send"), "", []string{"mail:send"}).Code)
}

func TestRequireScopesAllowingTestAcceptsBothModes(t *testing.T) {
	for _, mode := range []domain.APIKeyMode{domain.APIKeyModeLive, domain.APIKeyModeTest} {
		recorder := serveScopedRoute(t, RequireScopesAllowingTest("mail:transactional"), mode, []string{"mail:transactional"})
		assert.Equal(t, http.StatusNoContent, recorder.Code, "mode %s", mode)

		recorder = serveScopedRoute(t, RequireScopesAllowingTest("mail:transactional"), mode, []string{"mail:send"})
		assert.Equal(t, http.StatusForbidden, recorder.Code, "mode %s still needs its scope", mode)
	}
	assert.Panics(t, func() { RequireScopesAllowingTest() })
}

func TestAPIKeyModeFromContextDefaultsToLive(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Equal(t, domain.APIKeyModeLive, APIKeyModeFromContext(c))
	c.Set(apiKeyModeContextKey, domain.APIKeyModeTest)
	assert.Equal(t, domain.APIKeyModeTest, APIKeyModeFromContext(c))
	c.Set(apiKeyModeContextKey, "bogus")
	assert.Equal(t, domain.APIKeyModeLive, APIKeyModeFromContext(c))
}
