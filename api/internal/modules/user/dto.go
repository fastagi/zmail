package user

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ============================================================================
// Request DTOs (Input)
// ============================================================================

// UserRegisterRequest represents the registration request
type UserRegisterRequest struct {
	// A username never contains "@", so it cannot be mistaken for, or shadow, an email at sign-in.
	Username string `json:"username" binding:"required,min=3,max=50,excludesrune=@"`
	Password string `json:"password" binding:"required,min=6,max=50"`
	Email    string `json:"email" binding:"required,email"`
	Nickname string `json:"nickname" binding:"max=50"`
	Phone    string `json:"phone" binding:"max=20"`
	// EmailCode is the 6-digit code mailed by POST /v1/register/email-codes. It is required when
	// AUTH_REGISTRATION_EMAIL_CODE_REQUIRED is true and ignored otherwise.
	EmailCode string `json:"email_code" binding:"omitempty,len=6,numeric"`
}

// RegistrationEmailCodeRequest asks for a registration code for one address.
type RegistrationEmailCodeRequest struct {
	Email string `json:"email" binding:"required,email,max=100"`
}

// UserLoginRequest represents the login request
type UserLoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	// TOTPCode is the authenticator or recovery code of an account with two-factor sign-in.
	TOTPCode string `json:"totp_code" binding:"omitempty,max=32"`
}

// UserUpdateRequest represents the profile update request
type UserUpdateRequest struct {
	Nickname string `json:"nickname" binding:"max=50"`
	Avatar   string `json:"avatar" binding:"max=255"`
	Phone    string `json:"phone" binding:"max=20"`
	Bio      string `json:"bio" binding:"max=500"`
}

// UserChangePasswordRequest represents the password change request
type UserChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=50"`
}

// UserPasswordResetRequest represents the password reset request
type UserPasswordResetRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// UserPasswordResetConfirmRequest represents the reset confirmation request.
type UserPasswordResetConfirmRequest struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=50"`
}

// ============================================================================
// Response DTOs (Output)
// ============================================================================

// UserLoginResponse represents the login response
type UserLoginResponse struct {
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresIn   int64        `json:"expires_in"`
	User        *domain.User `json:"user"`
}

// ============================================================================
// Model Mappers (Moved to model.go)
// ============================================================================

// TOTPStatusResponse reports the signed-in user's two-factor state. Available is false when the
// instance cannot enroll new authenticators.
type TOTPStatusResponse struct {
	Available              bool       `json:"available"`
	Enabled                bool       `json:"enabled"`
	ConfirmedAt            *time.Time `json:"confirmed_at,omitempty"`
	RecoveryCodesRemaining int64      `json:"recovery_codes_remaining"`
}

// TOTPEnrollmentResponse carries a new pending authenticator secret, shown once.
type TOTPEnrollmentResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURI string `json:"otpauth_uri"`
}

// TOTPCodeRequest carries an authenticator code, or for disabling also a recovery code.
type TOTPCodeRequest struct {
	Code string `json:"code" binding:"required,max=32"`
}

// TOTPRecoveryCodesResponse carries the one-time recovery codes, shown once.
type TOTPRecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}
