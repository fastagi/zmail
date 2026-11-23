package user

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/router"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
	"github.com/fastagi/zmail/api/pkg/response"
)

func registrationCodeEngine(t *testing.T, svc *service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := NewHandler(svc, nil, nil, nil, nil, newAuthAbuseGuard(config.AuthenticationRateLimitConfig{}), nil)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	engine := gin.New()
	routes := router.New(engine).Prefix("/v1")
	handler.RegisterMiddleware(routes)
	handler.RegisterRoutes(routes)
	return engine
}

func TestRegistrationEmailCodeRouteAnswersTheSameForEveryAddress(t *testing.T) {
	mailer := testplatform.NewFakeMailer()
	repo := &fakeRepo{findByEmailFn: func(_ context.Context, email string) (*domain.User, error) {
		if email == "taken@example.test" {
			return &domain.User{ID: 1, Email: email}, nil
		}
		return nil, gorm.ErrRecordNotFound
	}}
	store := &fakeEmailCodeStore{}
	engine := registrationCodeEngine(t, newEmailCodeTestService(repo, store, mailer))

	newAddress := performAuthRequest(engine, "/v1/register/email-codes", `{"email":"new@example.test"}`, "198.51.100.1:1")
	existing := performAuthRequest(engine, "/v1/register/email-codes", `{"email":"taken@example.test"}`, "198.51.100.1:1")
	store.issueResult = registrationEmailCodeCoolingDown
	coolingDown := performAuthRequest(engine, "/v1/register/email-codes", `{"email":"new@example.test"}`, "198.51.100.1:1")

	require.Equal(t, http.StatusAccepted, newAddress.Code, newAddress.Body.String())
	for _, other := range []string{existing.Body.String(), coolingDown.Body.String()} {
		assert.JSONEq(t, newAddress.Body.String(), other, "the body never depends on the address")
	}
	assert.Equal(t, http.StatusAccepted, existing.Code)
	assert.Equal(t, http.StatusAccepted, coolingDown.Code)
	assert.Len(t, mailer.Sent(), 1, "only the new address was mailed")
}

func TestRegistrationEmailCodeRouteRejectsBadInputAndClosedRegistration(t *testing.T) {
	svc := newEmailCodeTestService(&fakeRepo{}, &fakeEmailCodeStore{}, testplatform.NewFakeMailer())
	engine := registrationCodeEngine(t, svc)

	invalid := performAuthRequest(engine, "/v1/register/email-codes", `{"email":"not-an-address"}`, "198.51.100.2:1")
	assert.Equal(t, http.StatusUnprocessableEntity, invalid.Code, invalid.Body.String())

	svc.registrationOpen = false
	closed := performAuthRequest(engine, "/v1/register/email-codes", `{"email":"new@example.test"}`, "198.51.100.2:1")
	require.Equal(t, http.StatusForbidden, closed.Code, closed.Body.String())
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	require.NoError(t, json.Unmarshal(closed.Body.Bytes(), &body))
	assert.Equal(t, response.ErrorCodeForbidden, body.ErrorCode)
}

func TestRegistrationEmailCodeRouteHasItsOwnIPAndAddressQuotas(t *testing.T) {
	limits := config.AuthenticationRateLimitConfig{
		Enabled: true,
		RegisterEmailCode: config.AuthenticationEndpointRateLimitConfig{
			PerIP:      config.RateLimitRuleConfig{Max: 2, Window: time.Hour},
			PerSubject: config.RateLimitRuleConfig{Max: 1, Window: time.Hour},
		},
	}
	service := &authGuardFakeService{}
	engine := authProtectionTestEngine(t, limits, service)

	first := performAuthRequest(engine, "/v1/register/email-codes", `{"email":"a@example.test"}`, "198.51.100.3:1")
	sameAddress := performAuthRequest(engine, "/v1/register/email-codes", `{"email":"A@Example.test"}`, "198.51.100.4:1")
	assert.Equal(t, http.StatusAccepted, first.Code, first.Body.String())
	assert.Equal(t, http.StatusTooManyRequests, sameAddress.Code, "the address quota ignores case")

	second := performAuthRequest(engine, "/v1/register/email-codes", `{"email":"b@example.test"}`, "198.51.100.3:1")
	third := performAuthRequest(engine, "/v1/register/email-codes", `{"email":"c@example.test"}`, "198.51.100.3:1")
	assert.Equal(t, http.StatusAccepted, second.Code)
	assert.Equal(t, http.StatusTooManyRequests, third.Code, "the source quota counts every address")
	assert.Equal(t, 2, service.registrationCodeCalls)
}

func TestRegisterRouteMapsEmailCodeErrors(t *testing.T) {
	for _, test := range []struct {
		storeErr error
		code     string
		body     string
	}{
		{nil, domain.CodeRegistrationEmailCodeRequired, `{"username":"alice","email":"alice@example.test","password":"password123"}`},
		{domain.ErrRegistrationEmailCodeInvalid, domain.CodeRegistrationEmailCodeInvalid, `{"username":"alice","email":"alice@example.test","password":"password123","email_code":"123456"}`},
		{domain.ErrRegistrationEmailCodeExpired, domain.CodeRegistrationEmailCodeExpired, `{"username":"alice","email":"alice@example.test","password":"password123","email_code":"123456"}`},
	} {
		svc := newEmailCodeTestService(&fakeRepo{}, &fakeEmailCodeStore{registerErr: test.storeErr}, testplatform.NewFakeMailer())
		engine := registrationCodeEngine(t, svc)

		result := performAuthRequest(engine, "/v1/register", test.body, "198.51.100.5:1")

		require.Equal(t, http.StatusBadRequest, result.Code, result.Body.String())
		var body struct {
			ErrorCode string `json:"error_code"`
		}
		require.NoError(t, json.Unmarshal(result.Body.Bytes(), &body))
		assert.Equal(t, test.code, body.ErrorCode)
	}

	svc := newEmailCodeTestService(&fakeRepo{}, &fakeEmailCodeStore{}, testplatform.NewFakeMailer())
	engine := registrationCodeEngine(t, svc)
	malformed := performAuthRequest(engine, "/v1/register",
		`{"username":"alice","email":"alice@example.test","password":"password123","email_code":"12ab56"}`, "198.51.100.6:1")
	assert.Equal(t, http.StatusUnprocessableEntity, malformed.Code, "a code is six digits")
}
