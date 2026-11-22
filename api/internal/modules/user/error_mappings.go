package user

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps this starter's domain errors to public status codes and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrUserNotFound, http.StatusNotFound, domain.CodeUserNotFound)
	mapper.Register(domain.ErrInvalidCredentials, http.StatusUnauthorized, domain.CodeInvalidCredentials)
	mapper.Register(domain.ErrPasswordResetTokenInvalid, http.StatusUnauthorized, domain.CodePasswordResetTokenInvalid)
	mapper.Register(domain.ErrPasswordResetTokenExpired, http.StatusUnauthorized, domain.CodePasswordResetTokenExpired)
	mapper.Register(domain.ErrRegistrationEmailCodeRequired, http.StatusBadRequest, domain.CodeRegistrationEmailCodeRequired)
	mapper.Register(domain.ErrRegistrationEmailCodeInvalid, http.StatusBadRequest, domain.CodeRegistrationEmailCodeInvalid)
	mapper.Register(domain.ErrRegistrationEmailCodeExpired, http.StatusBadRequest, domain.CodeRegistrationEmailCodeExpired)
	mapper.Register(domain.ErrTOTPRequired, http.StatusUnauthorized, domain.CodeTOTPRequired)
	mapper.Register(domain.ErrTOTPInvalid, http.StatusUnauthorized, domain.CodeTOTPInvalid)
	mapper.Register(domain.ErrTOTPAlreadyEnabled, http.StatusConflict, domain.CodeTOTPAlreadyEnabled)
	mapper.Register(domain.ErrTOTPNotEnabled, http.StatusConflict, domain.CodeTOTPNotEnabled)
	mapper.Register(domain.ErrSignInThrottled, http.StatusTooManyRequests, domain.CodeRateLimited)
	mapper.Register(domain.ErrAccountDisabled, http.StatusForbidden, domain.CodeAccountDisabled)
	mapper.Register(domain.ErrRegistrationDisabled, http.StatusForbidden, response.ErrorCodeForbidden)
	mapper.Register(domain.ErrEmailAlreadyExists, http.StatusConflict, domain.CodeEmailAlreadyExists)
	mapper.Register(domain.ErrUsernameAlreadyExists, http.StatusConflict, domain.CodeUsernameAlreadyExists)
}
