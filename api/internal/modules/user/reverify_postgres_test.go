package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/fastagi/zmail/api/internal/domain"
)

// TestReverifyCredentials asks a signed-in user for the account password and, once two-factor
// sign-in is on, a fresh second factor that cannot be replayed.
func TestReverifyCredentials(t *testing.T) {
	profile := newPostgresUserProfile(t)
	for _, statement := range []string{
		`CREATE TABLE user_totp_factors (
		  user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
		  secret text NOT NULL, confirmed_at timestamptz,
		  last_used_step bigint NOT NULL DEFAULT 0, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL)`,
		`CREATE TABLE user_recovery_codes (
		  id bigserial PRIMARY KEY, user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
		  code_hash char(64) NOT NULL, used_at timestamptz, created_at timestamptz NOT NULL, UNIQUE (user_id, code_hash))`,
	} {
		if err := profile.db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	user := profile.nextUser()
	if err := profile.repository.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	svc := NewService(profile.repository, profile.repository, nil, nil, nil, nil)
	svc.verifyPassword = func(_ []byte, password []byte) error {
		if string(password) == "right" {
			return nil
		}
		return bcrypt.ErrMismatchedHashAndPassword
	}
	svc.totp = newTOTPGuard(profile.repository, "totp-test-key-0123456789abcdef0123")
	now := time.Unix(1_900_000_000, 0)
	svc.totp.now = func() time.Time { return now }

	if err := svc.ReverifyCredentials(ctx, user.ID, "wrong", ""); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v", err)
	}
	if err := svc.ReverifyCredentials(ctx, user.ID, "right", ""); err != nil {
		t.Fatalf("right password without two-factor sign-in = %v", err)
	}
	if err := svc.ReverifyCredentials(ctx, 999999, "right", ""); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("unknown user = %v", err)
	}

	enrollment, err := svc.StartTOTPEnrollment(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	code := func() string {
		value, codeErr := totpCode(enrollment.Secret, totpStep(now))
		if codeErr != nil {
			t.Fatal(codeErr)
		}
		return value
	}
	if _, err := svc.ConfirmTOTP(ctx, user.ID, code()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(totpPeriod * time.Second)
	if err := svc.ReverifyCredentials(ctx, user.ID, "right", ""); !errors.Is(err, domain.ErrTOTPRequired) {
		t.Fatalf("missing code = %v", err)
	}
	if err := svc.ReverifyCredentials(ctx, user.ID, "right", "000000"); !errors.Is(err, domain.ErrTOTPInvalid) {
		t.Fatalf("wrong code = %v", err)
	}
	if err := svc.ReverifyCredentials(ctx, user.ID, "wrong", code()); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("a wrong password with a valid code = %v", err)
	}
	if err := svc.ReverifyCredentials(ctx, user.ID, "right", code()); err != nil {
		t.Fatalf("right password and code = %v", err)
	}
	if err := svc.ReverifyCredentials(ctx, user.ID, "right", code()); !errors.Is(err, domain.ErrTOTPInvalid) {
		t.Fatalf("a replayed code = %v", err)
	}
}
