package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/capabilities/idgen"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/events"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// AuthService defines the authentication and public account flows.
type AuthService interface {
	Register(ctx context.Context, req *UserRegisterRequest) (*domain.User, error)
	RequestRegistrationEmailCode(ctx context.Context, req *RegistrationEmailCodeRequest) error
	Login(ctx context.Context, req *UserLoginRequest) (*UserLoginResponse, error)
	RequestPasswordReset(ctx context.Context, req *UserPasswordResetRequest) error
	ConfirmPasswordReset(ctx context.Context, req *UserPasswordResetConfirmRequest) error
}

// ProfileService defines authenticated profile management flows.
type ProfileService interface {
	GetProfile(ctx context.Context, userID uint) (*domain.User, error)
	UpdateProfile(ctx context.Context, userID uint, req *UserUpdateRequest) (*domain.User, error)
	ChangePassword(ctx context.Context, userID uint, req *UserChangePasswordRequest) error
	DeleteAccount(ctx context.Context, userID uint) error
	TOTPStatus(ctx context.Context, userID uint) (*TOTPStatusResponse, error)
	StartTOTPEnrollment(ctx context.Context, userID uint) (*TOTPEnrollmentResponse, error)
	ConfirmTOTP(ctx context.Context, userID uint, code string) (*TOTPRecoveryCodesResponse, error)
	DisableTOTP(ctx context.Context, userID uint, code string) error
}

// UserQueryService defines read-only user lookup flows.
type UserQueryService interface {
	GetByID(ctx context.Context, id uint) (*domain.User, error)
	List(ctx context.Context, page, pageSize int) ([]*domain.User, int64, error)
}

// Service is the full user service surface kept for compatibility.
// Narrow seams should prefer AuthService, ProfileService, or UserQueryService.
type Service interface {
	AuthService
	ProfileService
	UserQueryService
}

// UserMailer captures the email seam used by the user starter.
// Email is a true external dependency, so this seam is worth naming explicitly.
type UserMailer interface {
	IsConfigured() bool
	SendPasswordResetEmail(ctx context.Context, to string, resetToken string) error
	SendWelcomeEmail(ctx context.Context, to string, username string) error
	SendRegistrationCodeEmail(ctx context.Context, to string, code string) error
}

type passwordResetStore interface {
	StorePasswordResetToken(ctx context.Context, userID uint, tokenHash string, expiresAt time.Time) error
	ResetPasswordWithToken(ctx context.Context, tokenHash string, passwordHash string, now time.Time) error
}

type authenticationSessionIssuer interface {
	Issue(ctx context.Context, user *domain.User, audience domain.SessionAudience) (*IssuedAuthenticationSession, error)
}

type userRepository interface {
	domain.UserRepository
	FindByLoginIdentifier(ctx context.Context, identifier string) (*domain.User, error)
	DeleteAccount(ctx context.Context, userID uint, check func(context.Context) error) error
	UpdatePasswordAndRevokeSessions(ctx context.Context, userID uint, passwordHash string, now time.Time) error
	RecordLogin(ctx context.Context, userID uint, at time.Time) error
}

const dummyPasswordHash = "$2a$10$BoIQPcmnuQfwI8s38RMnmeXm5V8xwU2lJVIF4EueN3y5x6KYUXelq"

// service implements the user service interfaces.
type service struct {
	repo           userRepository
	passwordResets passwordResetStore
	sessions       authenticationSessionIssuer
	eventBus       *events.EventBus
	mailer         UserMailer
	deletionPolicy *AccountDeletionPolicy
	admin          userAdministrationStore
	verifyPassword func(hashedPassword, password []byte) error
	signIns        signInThrottle
	// registrationOpen is false when AUTH_REGISTRATION_ENABLED=false closes public sign-up.
	registrationOpen bool
	// runPasswordReset starts reset delivery; tests replace it with an inline runner.
	runPasswordReset func(ctx context.Context, task func(context.Context))
	// emailCodes stores registration codes; the repository provides it.
	emailCodes registrationEmailCodeStore
	// emailCodeRequired makes Register demand a mailed code (AUTH_REGISTRATION_EMAIL_CODE_REQUIRED).
	emailCodeRequired bool
	// runRegistrationCode starts code delivery; tests replace it with an inline runner.
	runRegistrationCode func(ctx context.Context, task func(context.Context))
	// totp enforces and manages two-factor sign-in; nil without a factor store.
	totp *totpGuard
}

var (
	_ Service          = (*service)(nil)
	_ AuthService      = (*service)(nil)
	_ ProfileService   = (*service)(nil)
	_ UserQueryService = (*service)(nil)

	_ domain.CredentialSignIn = (*service)(nil)
)

// NewService creates a new service instance
func NewService(
	repo userRepository,
	passwordResets passwordResetStore,
	sessions authenticationSessionIssuer,
	eventBus *events.EventBus,
	mailer UserMailer,
	deletionPolicy *AccountDeletionPolicy,
) *service {
	svc := &service{
		repo:             repo,
		passwordResets:   passwordResets,
		sessions:         sessions,
		eventBus:         eventBus,
		mailer:           mailer,
		deletionPolicy:   deletionPolicy,
		verifyPassword:   bcrypt.CompareHashAndPassword,
		registrationOpen: true,
		runPasswordReset: newBackgroundRunner(
			"user.password_reset", passwordResetConcurrency, passwordResetTimeout,
		).run,
		runRegistrationCode: newBackgroundRunner(
			"user.registration_email_code", registrationEmailCodeConcurrency, registrationEmailCodeTimeout,
		).run,
	}
	if store, ok := repo.(userAdministrationStore); ok {
		svc.admin = store
	}
	if store, ok := repo.(registrationEmailCodeStore); ok {
		svc.emailCodes = store
	}
	return svc
}

// ProvideService builds the service with the configured per-account sign-in failure budget.
func ProvideService(
	repo userRepository,
	passwordResets passwordResetStore,
	sessions authenticationSessionIssuer,
	eventBus *events.EventBus,
	mailer UserMailer,
	deletionPolicy *AccountDeletionPolicy,
	guard *AuthAbuseGuard,
	cfg *config.Config,
) *service {
	svc := NewService(repo, passwordResets, sessions, eventBus, mailer, deletionPolicy)
	svc.signIns = guard.loginThrottle()
	if cfg != nil {
		svc.registrationOpen = !cfg.Authentication.RegistrationDisabled
		svc.emailCodeRequired = cfg.Authentication.RegistrationEmailCodeRequired
	}
	if store, ok := repo.(totpStore); ok {
		key := ""
		if cfg != nil {
			key = cfg.Authentication.TOTPEncryptionKey
		}
		svc.totp = newTOTPGuard(store, key)
	}
	if svc.registrationOpen && svc.emailCodeRequired && (mailer == nil || !mailer.IsConfigured()) {
		slog.Warn("user.registration_email_code_mailer_missing",
			"detail", "registration requires an email code but no system mailer is configured, so nobody can register")
	}
	return svc
}

// ============================================================================
// Authentication
// ============================================================================

// Register handles user registration
func (s *service) Register(ctx context.Context, req *UserRegisterRequest) (*domain.User, error) {
	if !s.registrationOpen {
		return nil, domain.ErrRegistrationDisabled
	}

	var (
		user *domain.User
		err  error
	)
	if s.emailCodeRequired {
		user, err = s.registerWithEmailCode(ctx, req)
	} else {
		var hashedPassword string
		if hashedPassword, err = hashRegistrationPassword(req.Password); err == nil {
			user, err = s.createAccount(ctx, req, hashedPassword)
		}
	}
	if err != nil {
		return nil, err
	}

	// Publish UserCreated event (fully decoupled side effects)
	// Welcome delivery is fire-and-forget after the account transaction. Keep
	// request values for correlation, but let the email adapter own its deadline.
	s.eventBus.PublishAsync(context.WithoutCancel(ctx), domain.NewUserCreatedEvent(user))

	return user, nil
}

func hashRegistrationPassword(password string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(hashedPassword), nil
}

// createAccount checks the username and email are free and inserts the account. It runs inside the
// registration-code transaction when codes are required.
func (s *service) createAccount(ctx context.Context, req *UserRegisterRequest, hashedPassword string) (*domain.User, error) {
	existingByUsername, err := s.repo.FindByUsername(ctx, req.Username)
	if err == nil && existingByUsername != nil {
		return nil, domain.ErrUsernameAlreadyExists
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to check existing username: %w", err)
	}

	// Check if email already exists
	existingByEmail, err := s.repo.FindByEmail(ctx, req.Email)
	if err == nil && existingByEmail != nil {
		return nil, domain.ErrEmailAlreadyExists
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to check existing email: %w", err)
	}

	user := &domain.User{
		Username: req.Username,
		Email:    req.Email,
		Password: hashedPassword,
		Nickname: req.Nickname,
		Phone:    req.Phone,
		Status:   1,
	}

	if err := s.repo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}
	return user, nil
}

// Login handles user login
func (s *service) Login(ctx context.Context, req *UserLoginRequest) (*UserLoginResponse, error) {
	user, issued, err := s.signIn(ctx, req.Username, req.Password, req.TOTPCode, domain.SessionAudienceUser, nil)
	if err != nil {
		return nil, err
	}

	return &UserLoginResponse{
		AccessToken: issued.AccessToken,
		TokenType:   issued.TokenType,
		ExpiresIn:   issued.ExpiresIn,
		User:        user,
	}, nil
}

// SignIn verifies credentials, lets the caller authorize the verified user, then issues a session.
func (s *service) SignIn(
	ctx context.Context,
	identifier string,
	password string,
	secondFactor string,
	audience domain.SessionAudience,
	authorize func(context.Context, *domain.User) error,
) (*domain.IssuedSession, error) {
	user, issued, err := s.signIn(ctx, identifier, password, secondFactor, audience, authorize)
	if err != nil {
		return nil, err
	}
	return &domain.IssuedSession{
		Credential: issued.AccessToken,
		ExpiresAt:  time.Now().Add(time.Duration(issued.ExpiresIn) * time.Second),
		User:       user,
	}, nil
}

// signIn is the single credential path for public login and operator sign-in. Unknown, wrong, and
// disabled accounts share one failure and comparable hashing work so responses do not reveal
// account state. authorize runs after verification and before any session exists.
func (s *service) signIn(
	ctx context.Context,
	identifier string,
	password string,
	totpCode string,
	audience domain.SessionAudience,
	authorize func(context.Context, *domain.User) error,
) (*domain.User, *IssuedAuthenticationSession, error) {
	user, err := s.repo.FindByLoginIdentifier(ctx, identifier)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, fmt.Errorf("failed to lookup login identifier: %w", err)
	}
	if err != nil {
		user = nil
	}
	throttleKey := signInThrottleKey(user, identifier)
	if s.signIns != nil {
		if allowed, _, _ := s.signIns.Take(ctx, throttleKey); !allowed {
			return nil, nil, domain.ErrSignInThrottled
		}
	}
	if user == nil {
		if compareErr := s.verifyPassword([]byte(dummyPasswordHash), []byte(password)); compareErr != nil &&
			!errors.Is(compareErr, bcrypt.ErrMismatchedHashAndPassword) {
			slog.ErrorContext(ctx, "user.login_dummy_hash_invalid", "err", compareErr)
		}
		return nil, nil, domain.ErrInvalidCredentials
	}

	passwordErr := s.verifyPassword([]byte(user.Password), []byte(password))
	if passwordErr != nil || !user.IsActive() {
		return nil, nil, domain.ErrInvalidCredentials
	}

	// A missing or wrong second factor keeps the attempt counted against the account.
	if factorErr := s.totp.checkSignIn(ctx, user.ID, totpCode); factorErr != nil {
		return nil, nil, factorErr
	}

	// Both factors were right: clear the account's failures before any authorization decision.
	if s.signIns != nil {
		s.signIns.Reset(ctx, throttleKey)
	}

	if authorize != nil {
		if authErr := authorize(ctx, user); authErr != nil {
			return nil, nil, authErr
		}
	}

	if s.sessions == nil {
		return nil, nil, domain.ErrServiceUnavailable
	}
	issued, err := s.sessions.Issue(ctx, user, audience)
	if err != nil {
		return nil, nil, err
	}

	// Update last login. Auth has already succeeded; a write failure
	// here shouldn't fail the login, but it shouldn't be invisible either.
	now := time.Now()
	user.LastLogin = &now
	if err := s.repo.RecordLogin(ctx, user.ID, now); err != nil {
		slog.WarnContext(ctx, "user.last_login_update_failed",
			"user_id", user.ID,
			"err", err,
		)
	}
	return user, issued, nil
}

// signInThrottleKey keys known accounts by ID, so every identifier for one account shares a
// budget, and unknown identifiers by their normalized hash, so they are throttled the same way.
func signInThrottleKey(user *domain.User, identifier string) string {
	if user != nil {
		return "auth:login:user:" + strconv.FormatUint(uint64(user.ID), 10)
	}
	return "auth:login:identifier:" + crypto.SHA256Hex(strings.ToLower(strings.TrimSpace(identifier)))
}

// ============================================================================
// Profile (Authenticated User)
// ============================================================================

// GetProfile retrieves user profile
func (s *service) GetProfile(ctx context.Context, userID uint) (*domain.User, error) {
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return nil, currentUserLookupError(err)
	}
	return user, nil
}

// currentUserLookupError keeps "not found" distinct from database failures, so a query error is
// never reported to the client as a missing account.
func currentUserLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrUserNotFound
	}
	if errors.Is(err, domain.ErrServiceUnavailable) {
		return err
	}
	return fmt.Errorf("load current user: %w", err)
}

// UpdateProfile updates user profile
func (s *service) UpdateProfile(ctx context.Context, userID uint, req *UserUpdateRequest) (*domain.User, error) {
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return nil, currentUserLookupError(err)
	}

	changes := make(map[string]domain.AuditValueChange)

	// Only update non-empty fields
	if req.Nickname != "" && req.Nickname != user.Nickname {
		changes["nickname"] = domain.AuditValueChange{Before: user.Nickname, After: req.Nickname}
		user.Nickname = req.Nickname
	}
	if req.Avatar != "" && req.Avatar != user.Avatar {
		changes["avatar"] = domain.AuditValueChange{Before: user.Avatar, After: req.Avatar}
		user.Avatar = req.Avatar
	}
	if req.Phone != "" && req.Phone != user.Phone {
		changes["phone"] = domain.AuditValueChange{Before: user.Phone, After: req.Phone}
		user.Phone = req.Phone
	}
	if req.Bio != "" && req.Bio != user.Bio {
		changes["bio"] = domain.AuditValueChange{Before: user.Bio, After: req.Bio}
		user.Bio = req.Bio
	}

	if err := s.repo.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}
	if len(changes) > 0 {
		auditstarter.RecordChange(ctx, auditstarter.Change{
			TargetType: "user",
			TargetID:   strconv.FormatUint(uint64(userID), 10),
			Result:     domain.AuditResultSuccess,
			Changes:    changes,
		})
	}

	return user, nil
}

// ChangePassword changes user password
func (s *service) ChangePassword(ctx context.Context, userID uint, req *UserChangePasswordRequest) error {
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return currentUserLookupError(err)
	}

	if cmpErr := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.OldPassword)); cmpErr != nil {
		return domain.ErrInvalidCredentials
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	if err := s.repo.UpdatePasswordAndRevokeSessions(
		ctx,
		user.ID,
		string(hashedPassword),
		time.Now().UTC(),
	); err != nil {
		return err
	}

	auditstarter.RecordChange(ctx, auditstarter.Change{
		TargetType: "user",
		TargetID:   strconv.FormatUint(uint64(userID), 10),
		Result:     domain.AuditResultSuccess,
		Changes: map[string]domain.AuditValueChange{
			"password": {Before: "[redacted]", After: "[redacted]"},
		},
		Metadata: map[string]any{
			"credential": "password",
		},
	})
	return nil
}

// DeleteAccount deletes user account
func (s *service) DeleteAccount(ctx context.Context, userID uint) error {
	if err := s.repo.DeleteAccount(ctx, userID, func(transactionContext context.Context) error {
		return s.deletionPolicy.Prepare(transactionContext, userID)
	}); err != nil {
		return err
	}

	auditstarter.RecordChange(ctx, auditstarter.Change{
		TargetType: "user",
		TargetID:   strconv.FormatUint(uint64(userID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"operation": "delete_account",
		},
	})
	return nil
}

// ============================================================================
// Public
// ============================================================================

// RequestPasswordReset creates a one-time reset token and emails it to the user.
func (s *service) RequestPasswordReset(ctx context.Context, req *UserPasswordResetRequest) error {
	email := req.Email
	s.runPasswordReset(ctx, func(ctx context.Context) {
		s.deliverPasswordReset(ctx, email)
	})
	return nil
}

// deliverPasswordReset runs off the request path, so neither the account lookup nor mail delivery
// changes the response or its timing. Every failure is logged and never reported to the caller.
func (s *service) deliverPasswordReset(ctx context.Context, email string) {
	user, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			slog.ErrorContext(ctx, "user.password_reset_lookup_failed", "error_type", fmt.Sprintf("%T", err))
		}
		return
	}

	secret, err := crypto.GenerateKeyHex(24)
	if err != nil {
		slog.ErrorContext(ctx, "user.password_reset_token_generation_failed", "user_id", user.ID, "err", err)
		return
	}
	resetToken := "zrp_" + strings.ToLower(idgen.ShortID()) + "." + secret
	expiresAt := time.Now().Add(30 * time.Minute)

	if err := s.passwordResets.StorePasswordResetToken(ctx, user.ID, crypto.SHA256Hex(resetToken), expiresAt); err != nil {
		slog.ErrorContext(ctx, "user.password_reset_token_store_failed", "user_id", user.ID, "err", err)
		return
	}

	if s.mailer == nil || !s.mailer.IsConfigured() {
		slog.ErrorContext(ctx, "user.password_reset_mailer_missing", "user_id", user.ID)
		return
	}
	if err := s.mailer.SendPasswordResetEmail(ctx, user.Email, resetToken); err != nil {
		slog.ErrorContext(ctx, "user.password_reset_delivery_failed", "user_id", user.ID, "err", err)
	}
}

// ConfirmPasswordReset consumes a one-time token and writes the new password hash.
func (s *service) ConfirmPasswordReset(ctx context.Context, req *UserPasswordResetConfirmRequest) error {
	token := strings.TrimSpace(req.Token)
	if token == "" {
		return domain.ErrInvalidInput
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	if err := s.passwordResets.ResetPasswordWithToken(ctx, crypto.SHA256Hex(token), string(hashedPassword), time.Now()); err != nil {
		return err
	}
	return nil
}

// ============================================================================
// Admin/Query
// ============================================================================

// GetByID retrieves a user by ID
func (s *service) GetByID(ctx context.Context, id uint) (*domain.User, error) {
	return s.repo.FindByID(ctx, id)
}

// List retrieves a paginated list of users
func (s *service) List(ctx context.Context, page, pageSize int) ([]*domain.User, int64, error) {
	return s.repo.FindAll(ctx, page, pageSize)
}
