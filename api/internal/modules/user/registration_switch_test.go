package user

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/events"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/pkg/response"
)

func registrationSwitchService(t *testing.T, cfg *config.Config, created *int) *service {
	t.Helper()
	repo := &fakeRepo{
		findByEmailFn: func(context.Context, string) (*domain.User, error) {
			return nil, gorm.ErrRecordNotFound
		},
		createFn: func(context.Context, *domain.User) error {
			*created++
			return nil
		},
	}
	return ProvideService(
		repo, repo, &fakeSessionIssuer{}, events.NewEventBus(), &fakeUserMailer{},
		NewAccountDeletionPolicy(), newAuthAbuseGuard(config.AuthenticationRateLimitConfig{}), cfg,
	)
}

func closedRegistrationConfig() *config.Config {
	return &config.Config{Authentication: config.AuthenticationConfig{RegistrationDisabled: true}}
}

func TestProvideServiceClosesRegistrationFromConfig(t *testing.T) {
	request := &UserRegisterRequest{Username: "alice", Email: "alice@example.com", Password: "password123"}

	var created int
	closed := registrationSwitchService(t, closedRegistrationConfig(), &created)
	_, err := closed.Register(context.Background(), request)
	require.ErrorIs(t, err, domain.ErrRegistrationDisabled)
	assert.Zero(t, created, "a closed registration must not reach persistence")

	open := registrationSwitchService(t, &config.Config{}, &created)
	_, err = open.Register(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, 1, created)

	// Callers that build the service without configuration keep the open default.
	unconfigured := registrationSwitchService(t, nil, &created)
	_, err = unconfigured.Register(context.Background(), request)
	require.NoError(t, err)
}

func TestRegisterRouteRefusesClosedRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var created int
	svc := registrationSwitchService(t, closedRegistrationConfig(), &created)

	handler := NewHandler(svc, nil, nil, nil, nil, newAuthAbuseGuard(config.AuthenticationRateLimitConfig{}))
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	engine := gin.New()
	routes := router.New(engine).Prefix("/v1")
	handler.RegisterMiddleware(routes)
	handler.RegisterRoutes(routes)

	result := performAuthRequest(engine, "/v1/register",
		`{"username":"alice","email":"alice@example.com","password":"password123"}`, "198.51.100.10:1001")

	require.Equal(t, http.StatusForbidden, result.Code, result.Body.String())
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &body))
	assert.Equal(t, response.ErrorCodeForbidden, body.ErrorCode)
	assert.Zero(t, created)
}
