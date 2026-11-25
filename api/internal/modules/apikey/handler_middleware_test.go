package apikey

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/pkg/response"
)

type apiKeyValidationErrorService struct {
	Service
	err error
}

func (s *apiKeyValidationErrorService) Validate(
	context.Context,
	string,
	...string,
) (*domain.APIKey, error) {
	return nil, s.err
}

func TestAPIKeyMiddlewarePreservesDomainErrorCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		err       error
		errorCode string
	}{
		{name: "invalid", err: domain.ErrAPIKeyInvalid, errorCode: domain.CodeAPIKeyInvalid},
		{name: "expired", err: domain.ErrAPIKeyExpired, errorCode: domain.CodeAPIKeyExpired},
		{name: "revoked", err: domain.ErrAPIKeyRevoked, errorCode: domain.CodeAPIKeyRevoked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response.DefaultErrorMapper.Register(tt.err, http.StatusUnauthorized, tt.errorCode)
			engine := apiKeyMiddlewareTestEngine(&apiKeyValidationErrorService{err: tt.err})
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("X-API-Key", "fastbuild_test.secret")
			result := httptest.NewRecorder()

			engine.ServeHTTP(result, request)

			assert.Equal(t, http.StatusUnauthorized, result.Code)
			var body struct {
				ErrorCode string `json:"error_code"`
			}
			require.NoError(t, json.Unmarshal(result.Body.Bytes(), &body))
			assert.Equal(t, tt.errorCode, body.ErrorCode)
		})
	}
}

func TestAPIKeyMiddlewareUsesAuthUnauthorizedWhenHeaderIsMissing(t *testing.T) {
	engine := apiKeyMiddlewareTestEngine(&apiKeyValidationErrorService{})
	result := httptest.NewRecorder()

	engine.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/protected", nil))

	assert.Equal(t, http.StatusUnauthorized, result.Code)
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &body))
	assert.Equal(t, response.ErrorCodeUnauthorized, body.ErrorCode)
}

func apiKeyMiddlewareTestEngine(service Service) *gin.Engine {
	engine := gin.New()
	routes := router.New(engine)
	handler := NewHandler(service)
	handler.RegisterMiddleware(routes)
	routes.WithMiddleware("api_key").GET("/protected", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	return engine
}

type apiKeyStaticService struct {
	Service
	key *domain.APIKey
}

func (s *apiKeyStaticService) Validate(context.Context, string, ...string) (*domain.APIKey, error) {
	return s.key, nil
}

func TestAPIKeyMiddlewareSetsModeInContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, mode := range []domain.APIKeyMode{domain.APIKeyModeLive, domain.APIKeyModeTest} {
		engine := gin.New()
		routes := router.New(engine)
		NewHandler(&apiKeyStaticService{key: &domain.APIKey{
			ID: 3, UserID: 9, Scopes: []string{"mail:transactional"}, Mode: mode,
		}}).RegisterMiddleware(routes)
		var seen domain.APIKeyMode
		routes.WithMiddleware("api_key").GET("/protected", func(c *gin.Context) {
			seen = APIKeyModeFromContext(c)
			c.Status(http.StatusNoContent)
		})
		request := httptest.NewRequest(http.MethodGet, "/protected", nil)
		request.Header.Set("X-API-Key", "fastbuild_ab12cd34.secret")
		result := httptest.NewRecorder()

		engine.ServeHTTP(result, request)

		assert.Equal(t, http.StatusNoContent, result.Code)
		assert.Equal(t, mode, seen)
	}
}

func TestAPIKeyMiddlewareAcceptsBearerAndXAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		xAPIKey    string
		authHeader string
		wantStatus int
		wantKey    string
	}{
		{
			name:       "X-API-Key header",
			xAPIKey:    "zmail_live_abc.secret1",
			wantStatus: http.StatusNoContent,
			wantKey:    "zmail_live_abc.secret1",
		},
		{
			name:       "Authorization Bearer header",
			authHeader: "Bearer zmail_live_abc.secret2",
			wantStatus: http.StatusNoContent,
			wantKey:    "zmail_live_abc.secret2",
		},
		{
			name:       "Authorization bearer lowercase",
			authHeader: "bearer zmail_live_abc.secret3",
			wantStatus: http.StatusNoContent,
			wantKey:    "zmail_live_abc.secret3",
		},
		{
			name:       "X-API-Key takes precedence over Authorization Bearer",
			xAPIKey:    "zmail_live_abc.fromheader",
			authHeader: "Bearer zmail_live_abc.frombearer",
			wantStatus: http.StatusNoContent,
			wantKey:    "zmail_live_abc.fromheader",
		},
		{
			name:       "Non-Bearer Authorization rejected",
			authHeader: "Basic dXNlcjpwYXNz",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "Empty Bearer token rejected",
			authHeader: "Bearer   ",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string
			svc := &apiKeyCaptureService{
				key: &domain.APIKey{ID: 7, UserID: 11, Scopes: []string{"mail:transactional"}, Mode: domain.APIKeyModeLive},
				onValidate: func(k string) {
					captured = k
				},
			}
			engine := apiKeyMiddlewareTestEngine(svc)
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tt.xAPIKey != "" {
				req.Header.Set("X-API-Key", tt.xAPIKey)
			}
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantKey, captured)
		})
	}
}

type apiKeyCaptureService struct {
	Service
	key        *domain.APIKey
	onValidate func(string)
}

func (s *apiKeyCaptureService) Validate(_ context.Context, key string, _ ...string) (*domain.APIKey, error) {
	if s.onValidate != nil {
		s.onValidate(key)
	}
	return s.key, nil
}
