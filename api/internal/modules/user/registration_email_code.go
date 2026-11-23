package user

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// registrationEmailCodeDigits is the length of a mailed registration code.
	registrationEmailCodeDigits = 6
	// registrationEmailCodeMaxAttempts wrong guesses spend a code.
	registrationEmailCodeMaxAttempts = 5
	registrationEmailCodeConcurrency = 32
	registrationEmailCodeTimeout     = 30 * time.Second
)

// defaultRegistrationEmailCodePolicy bounds how often one address can be mailed a code.
var defaultRegistrationEmailCodePolicy = registrationEmailCodePolicy{
	TTL:          10 * time.Minute,
	Cooldown:     60 * time.Second,
	Window:       time.Hour,
	MaxPerWindow: 5,
}

// registrationEmailCodePolicy is the lifetime and the per-address issue limits of registration codes.
type registrationEmailCodePolicy struct {
	TTL          time.Duration
	Cooldown     time.Duration
	Window       time.Duration
	MaxPerWindow int
}

// registrationEmailCodeIssue says whether the store issued a code, and why not when it did not.
type registrationEmailCodeIssue string

const (
	registrationEmailCodeIssued      registrationEmailCodeIssue = "issued"
	registrationEmailCodeCoolingDown registrationEmailCodeIssue = "cooling_down"
	registrationEmailCodeCapped      registrationEmailCodeIssue = "capped"
)

// registrationEmailCodeStore persists registration codes by hash. Issuing enforces the per-address
// cooldown and window cap atomically; registering checks the newest code, runs create, and consumes
// the code in one transaction.
type registrationEmailCodeStore interface {
	IssueRegistrationEmailCode(
		ctx context.Context,
		email string,
		codeHash string,
		now time.Time,
		policy registrationEmailCodePolicy,
	) (registrationEmailCodeIssue, error)
	RegisterWithEmailCode(
		ctx context.Context,
		email string,
		codeHash string,
		now time.Time,
		maxAttempts int,
		create func(context.Context) error,
	) error
}

// normalizedRegistrationEmail is the key registration codes are stored and checked under.
func normalizedRegistrationEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// registrationEmailCodeHash binds a code to the address it was mailed to, so a row only proves
// ownership of that one address.
func registrationEmailCodeHash(normalizedEmail, code string) string {
	return crypto.SHA256Hex(normalizedEmail + "\x00" + code)
}

func generateRegistrationEmailCode() (string, error) {
	limit := big.NewInt(1_000_000)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", registrationEmailCodeDigits, value.Int64()), nil
}

// RequestRegistrationEmailCode mails a registration code to an address that has no account. The
// answer is the same whether the address has an account, is cooling down, or was mailed: the
// lookup, the issue, and the delivery run after the response on a bounded background task.
func (s *service) RequestRegistrationEmailCode(ctx context.Context, req *RegistrationEmailCodeRequest) error {
	if !s.registrationOpen {
		return domain.ErrRegistrationDisabled
	}
	email := normalizedRegistrationEmail(req.Email)
	s.runRegistrationCode(ctx, func(ctx context.Context) {
		s.deliverRegistrationEmailCode(ctx, email)
	})
	return nil
}

// deliverRegistrationEmailCode logs every outcome without the address or the code.
func (s *service) deliverRegistrationEmailCode(ctx context.Context, email string) {
	if s.emailCodes == nil {
		slog.ErrorContext(ctx, "user.registration_email_code_store_missing")
		return
	}
	if _, err := s.repo.FindByEmail(ctx, email); err == nil {
		// An existing account gets neither a code nor a notice; see contracts/AUTHENTICATION.md.
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		slog.ErrorContext(ctx, "user.registration_email_code_lookup_failed", "error_type", fmt.Sprintf("%T", err))
		return
	}

	code, err := generateRegistrationEmailCode()
	if err != nil {
		slog.ErrorContext(ctx, "user.registration_email_code_generation_failed", "err", err)
		return
	}
	issue, err := s.emailCodes.IssueRegistrationEmailCode(
		ctx, email, registrationEmailCodeHash(email, code), time.Now(), defaultRegistrationEmailCodePolicy,
	)
	if err != nil {
		slog.ErrorContext(ctx, "user.registration_email_code_store_failed", "err", err)
		return
	}
	if issue != registrationEmailCodeIssued {
		slog.InfoContext(ctx, "user.registration_email_code_refused", "reason", string(issue))
		return
	}

	if s.mailer == nil || !s.mailer.IsConfigured() {
		slog.ErrorContext(ctx, "user.registration_email_code_mailer_missing")
		return
	}
	if err := s.mailer.SendRegistrationCodeEmail(ctx, email, code); err != nil {
		slog.ErrorContext(ctx, "user.registration_email_code_delivery_failed", "err", err)
	}
}

// registerWithEmailCode checks the code and creates the account in one transaction, so the code is
// consumed only when the account exists and a conflict never spends it.
func (s *service) registerWithEmailCode(ctx context.Context, req *UserRegisterRequest) (*domain.User, error) {
	code := strings.TrimSpace(req.EmailCode)
	if code == "" {
		return nil, domain.ErrRegistrationEmailCodeRequired
	}
	if s.emailCodes == nil {
		return nil, domain.ErrServiceUnavailable
	}
	hashedPassword, err := hashRegistrationPassword(req.Password)
	if err != nil {
		return nil, err
	}

	email := normalizedRegistrationEmail(req.Email)
	var user *domain.User
	err = s.emailCodes.RegisterWithEmailCode(
		ctx, email, registrationEmailCodeHash(email, code), time.Now(), registrationEmailCodeMaxAttempts,
		func(transactionContext context.Context) error {
			// The code proved the address.
			verifiedAt := time.Now()
			created, createErr := s.createAccount(transactionContext, req, hashedPassword, &verifiedAt)
			user = created
			return createErr
		},
	)
	if err != nil {
		return nil, err
	}
	return user, nil
}
