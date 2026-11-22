package user

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	totpIssuer        = "Zmail"
	recoveryCodeCount = 10
)

// errTOTPCodeRejected is a wrong code on a signed-in settings request; unlike ErrTOTPInvalid at
// sign-in it must not read as an ended session.
var errTOTPCodeRejected = errors.New("the authenticator or recovery code is wrong")

type totpStore interface {
	FindTOTPFactor(ctx context.Context, userID uint) (*totpFactor, error)
	SavePendingTOTPFactor(ctx context.Context, userID uint, secret string, now time.Time) error
	ConfirmTOTPFactor(ctx context.Context, userID uint, secret string, step int64, codeHashes []string, now time.Time) error
	UseTOTPStep(ctx context.Context, userID uint, step int64, now time.Time) (bool, error)
	UseRecoveryCode(ctx context.Context, userID uint, codeHash string, now time.Time) (bool, error)
	CountRecoveryCodes(ctx context.Context, userID uint) (int64, error)
	DeleteTOTPFactor(ctx context.Context, userID uint) error
}

// totpGuard owns two-factor state. cipher is nil without AUTH_TOTP_ENCRYPTION_KEY: enrollment is
// then off, but existing factors are still enforced and recovery codes still work.
type totpGuard struct {
	store  totpStore
	cipher *crypto.AESEncryptor
	now    func() time.Time
}

func newTOTPGuard(store totpStore, key string) *totpGuard {
	guard := &totpGuard{store: store, now: time.Now}
	if key != "" {
		guard.cipher = crypto.NewAESEncryptorFromString(key)
	}
	return guard
}

func normalizeSecondFactorCode(code string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code)))
}

func newRecoveryCodes() (codes, hashes []string, err error) {
	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	for range recoveryCodeCount {
		raw := make([]byte, 5)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, fmt.Errorf("generate recovery code: %w", err)
		}
		code := strings.ToLower(encoding.EncodeToString(raw))
		codes = append(codes, code[:4]+"-"+code[4:])
		hashes = append(hashes, crypto.SHA256Hex(code))
	}
	return codes, hashes, nil
}

// verify consumes a six-digit code or a recovery code of a confirmed factor.
func (g *totpGuard) verify(ctx context.Context, factor *totpFactor, code string) (bool, error) {
	code = normalizeSecondFactorCode(code)
	now := g.now()
	if len(code) == totpDigits && strings.Trim(code, "0123456789") == "" {
		if g.cipher == nil {
			return false, nil
		}
		secret, err := g.cipher.DecryptString(factor.Secret)
		if err != nil {
			return false, fmt.Errorf("decrypt totp secret: %w", err)
		}
		step, ok := matchTOTP(secret, code, now, factor.LastUsedStep)
		if !ok {
			return false, nil
		}
		return g.store.UseTOTPStep(ctx, factor.UserID, step, now)
	}
	if code == "" {
		return false, nil
	}
	return g.store.UseRecoveryCode(ctx, factor.UserID, crypto.SHA256Hex(code), now)
}

// checkSignIn asks a user with a confirmed factor for a valid second factor.
func (g *totpGuard) checkSignIn(ctx context.Context, userID uint, code string) error {
	if g == nil {
		return nil
	}
	factor, err := g.store.FindTOTPFactor(ctx, userID)
	if err != nil {
		return fmt.Errorf("find totp factor: %w", err)
	}
	if factor == nil || factor.ConfirmedAt == nil {
		return nil
	}
	if strings.TrimSpace(code) == "" {
		return domain.ErrTOTPRequired
	}
	ok, err := g.verify(ctx, factor, code)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrTOTPInvalid
	}
	return nil
}

func (s *service) TOTPStatus(ctx context.Context, userID uint) (*TOTPStatusResponse, error) {
	if s.totp == nil {
		return &TOTPStatusResponse{}, nil
	}
	status := &TOTPStatusResponse{Available: s.totp.cipher != nil}
	factor, err := s.totp.store.FindTOTPFactor(ctx, userID)
	if err != nil || factor == nil || factor.ConfirmedAt == nil {
		return status, err
	}
	status.Enabled = true
	status.ConfirmedAt = factor.ConfirmedAt
	status.RecoveryCodesRemaining, err = s.totp.store.CountRecoveryCodes(ctx, userID)
	return status, err
}

func (s *service) StartTOTPEnrollment(ctx context.Context, userID uint) (*TOTPEnrollmentResponse, error) {
	if s.totp == nil || s.totp.cipher == nil {
		return nil, domain.ErrServiceUnavailable
	}
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return nil, currentUserLookupError(err)
	}
	secret, err := newTOTPSecret()
	if err != nil {
		return nil, err
	}
	sealed, err := s.totp.cipher.EncryptString(secret)
	if err != nil {
		return nil, fmt.Errorf("encrypt totp secret: %w", err)
	}
	if err := s.totp.store.SavePendingTOTPFactor(ctx, userID, sealed, s.totp.now()); err != nil {
		return nil, err
	}
	return &TOTPEnrollmentResponse{Secret: secret, OTPAuthURI: totpURI(totpIssuer, user.Email, secret)}, nil
}

func (s *service) ConfirmTOTP(ctx context.Context, userID uint, code string) (*TOTPRecoveryCodesResponse, error) {
	if s.totp == nil || s.totp.cipher == nil {
		return nil, domain.ErrServiceUnavailable
	}
	factor, err := s.totp.store.FindTOTPFactor(ctx, userID)
	if err != nil {
		return nil, err
	}
	if factor == nil {
		return nil, domain.ErrTOTPNotEnabled
	}
	if factor.ConfirmedAt != nil {
		return nil, domain.ErrTOTPAlreadyEnabled
	}
	secret, err := s.totp.cipher.DecryptString(factor.Secret)
	if err != nil {
		return nil, fmt.Errorf("decrypt totp secret: %w", err)
	}
	throttleKey, err := s.takeTOTPAttempt(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := s.totp.now()
	step, ok := matchTOTP(secret, normalizeSecondFactorCode(code), now, factor.LastUsedStep)
	if !ok {
		return nil, errTOTPCodeRejected
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.totp.store.ConfirmTOTPFactor(ctx, userID, factor.Secret, step, hashes, now); err != nil {
		if errors.Is(err, domain.ErrTOTPInvalid) {
			return nil, errTOTPCodeRejected
		}
		return nil, err
	}
	s.resetTOTPAttempts(ctx, throttleKey)
	return &TOTPRecoveryCodesResponse{RecoveryCodes: codes}, nil
}

// takeTOTPAttempt spends one of the account's sign-in attempts, so code guesses from a signed-in
// session share the budget that guards sign-in.
func (s *service) takeTOTPAttempt(ctx context.Context, userID uint) (string, error) {
	key := signInThrottleKey(&domain.User{ID: userID}, "")
	if s.signIns != nil {
		if allowed, _, _ := s.signIns.Take(ctx, key); !allowed {
			return key, domain.ErrSignInThrottled
		}
	}
	return key, nil
}

func (s *service) resetTOTPAttempts(ctx context.Context, key string) {
	if s.signIns != nil {
		s.signIns.Reset(ctx, key)
	}
}

func (s *service) DisableTOTP(ctx context.Context, userID uint, code string) error {
	if s.totp == nil {
		return domain.ErrTOTPNotEnabled
	}
	factor, err := s.totp.store.FindTOTPFactor(ctx, userID)
	if err != nil {
		return err
	}
	if factor == nil || factor.ConfirmedAt == nil {
		return domain.ErrTOTPNotEnabled
	}
	throttleKey, err := s.takeTOTPAttempt(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := s.totp.verify(ctx, factor, code)
	if err != nil {
		return err
	}
	if !ok {
		return errTOTPCodeRejected
	}
	if err := s.totp.store.DeleteTOTPFactor(ctx, userID); err != nil {
		return err
	}
	s.resetTOTPAttempts(ctx, throttleKey)
	return nil
}
