package apikey

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

const (
	apiKeyScopesContextKey = "apiKeyScopes"
	apiKeyModeContextKey   = "apiKeyMode"
)

// APIKeyModeFromContext returns the authenticated key's mode. A context without a known mode is
// live, so a route that reads it before the key is authenticated never mistakes it for test.
func APIKeyModeFromContext(c *gin.Context) domain.APIKeyMode {
	value, exists := c.Get(apiKeyModeContextKey)
	if !exists {
		return domain.APIKeyModeLive
	}
	mode, ok := value.(domain.APIKeyMode)
	if !ok || !mode.Valid() {
		return domain.APIKeyModeLive
	}
	return mode
}

// RequireScopes rejects authenticated API keys that do not grant every scope, and refuses every
// test key: a route accepts test keys only by declaring it with RequireScopesAllowingTest.
// Apply it after the starter-owned "api_key" middleware.
func RequireScopes(requiredScopes ...string) gin.HandlerFunc {
	return requireScopes(false, requiredScopes)
}

// RequireScopesAllowingTest is RequireScopes for a route that declares test support: it accepts
// live and test keys that grant every scope. The route must branch on APIKeyModeFromContext.
func RequireScopesAllowingTest(requiredScopes ...string) gin.HandlerFunc {
	return requireScopes(true, requiredScopes)
}

func requireScopes(allowTest bool, requiredScopes []string) gin.HandlerFunc {
	normalized, err := normalizeAPIKeyScopes(requiredScopes)
	if err != nil || len(normalized) == 0 {
		panic("apikey: RequireScopes needs at least one valid scope")
	}

	return func(c *gin.Context) {
		value, exists := c.Get(apiKeyScopesContextKey)
		scopes, ok := value.([]string)
		if !exists || !ok {
			response.AbortUnauthorized(c, "API key authentication required")
			return
		}

		if !allowTest && APIKeyModeFromContext(c) == domain.APIKeyModeTest {
			response.AbortWithCode(
				c,
				http.StatusForbidden,
				domain.CodePermissionDenied,
				"Test API keys are not accepted here",
			)
			return
		}

		key := &domain.APIKey{Scopes: scopes}
		for _, required := range normalized {
			if !key.HasScope(required) {
				response.AbortWithCode(
					c,
					http.StatusForbidden,
					domain.CodePermissionDenied,
					"API key scope denied",
				)
				return
			}
		}

		c.Next()
	}
}
