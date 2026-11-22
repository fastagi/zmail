package user

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
)

func TestPostgresTOTPEnrollmentSignInAndRecovery(t *testing.T) {
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
	svc.verifyPassword = func([]byte, []byte) error { return nil }
	svc.totp = newTOTPGuard(profile.repository, "totp-test-key-0123456789abcdef0123")
	now := time.Unix(1_900_000_000, 0)
	svc.totp.now = func() time.Time { return now }
	login := func(code string) error {
		_, err := svc.Login(ctx, &UserLoginRequest{Username: user.Username, Password: "x", TOTPCode: code})
		return err
	}
	codeAt := func(secret string, at time.Time) string {
		code, err := totpCode(secret, totpStep(at))
		if err != nil {
			t.Fatal(err)
		}
		return code
	}

	if status, err := svc.TOTPStatus(ctx, user.ID); err != nil || !status.Available || status.Enabled {
		t.Fatalf("initial status = %+v, %v", status, err)
	}
	first, err := svc.StartTOTPEnrollment(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.StartTOTPEnrollment(ctx, user.ID)
	if err != nil || second.Secret == first.Secret || !strings.Contains(second.OTPAuthURI, "Zmail") {
		t.Fatalf("restarted enrollment = %+v, %v", second, err)
	}
	if !errors.Is(login(""), domain.ErrServiceUnavailable) {
		t.Fatal("a pending factor must not be asked for at sign-in")
	}
	// A confirmation checked against a secret that was replaced meanwhile must not confirm the new one.
	if staleErr := profile.repository.ConfirmTOTPFactor(ctx, user.ID, "replaced-secret", 1, nil, now); !errors.Is(staleErr, domain.ErrTOTPInvalid) {
		t.Fatalf("ConfirmTOTPFactor(stale secret) error = %v, want ErrTOTPInvalid", staleErr)
	}
	if _, confirmErr := svc.ConfirmTOTP(ctx, user.ID, codeAt(first.Secret, now)); !errors.Is(confirmErr, errTOTPCodeRejected) {
		t.Fatalf("replaced secret confirmed: %v", confirmErr)
	}
	confirmed, err := svc.ConfirmTOTP(ctx, user.ID, codeAt(second.Secret, now))
	if err != nil || len(confirmed.RecoveryCodes) != recoveryCodeCount {
		t.Fatalf("confirm = %+v, %v", confirmed, err)
	}
	if _, err := svc.StartTOTPEnrollment(ctx, user.ID); !errors.Is(err, domain.ErrTOTPAlreadyEnabled) {
		t.Fatalf("enrollment over a confirmed factor: %v", err)
	}

	if err := login(""); !errors.Is(err, domain.ErrTOTPRequired) {
		t.Fatalf("login without a code: %v", err)
	}
	if err := login(codeAt(second.Secret, now)); !errors.Is(err, domain.ErrTOTPInvalid) {
		t.Fatalf("the confirming code was replayed: %v", err)
	}
	now = now.Add(totpPeriod * time.Second)
	if err := login(codeAt(second.Secret, now)); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("login with the next code: %v", err)
	}

	recovery := normalizeSecondFactorCode(confirmed.RecoveryCodes[0])
	var used atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := profile.repository.UseRecoveryCode(ctx, user.ID, crypto.SHA256Hex(recovery), now); err == nil && ok {
				used.Add(1)
			}
		}()
	}
	wg.Wait()
	if used.Load() != 1 {
		t.Fatalf("a recovery code was used %d times", used.Load())
	}
	if err := login(confirmed.RecoveryCodes[0]); !errors.Is(err, domain.ErrTOTPInvalid) {
		t.Fatalf("a used recovery code signed in: %v", err)
	}
	if err := login(" " + strings.ToUpper(confirmed.RecoveryCodes[1]) + " "); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("login with a recovery code: %v", err)
	}
	if status, err := svc.TOTPStatus(ctx, user.ID); err != nil || !status.Enabled || status.RecoveryCodesRemaining != 8 {
		t.Fatalf("status after recovery = %+v, %v", status, err)
	}

	if err := svc.DisableTOTP(ctx, user.ID, "zzzz-zzzz"); !errors.Is(err, errTOTPCodeRejected) {
		t.Fatalf("disable with a wrong code: %v", err)
	}
	if err := svc.DisableTOTP(ctx, user.ID, confirmed.RecoveryCodes[2]); err != nil {
		t.Fatal(err)
	}
	if status, err := svc.TOTPStatus(ctx, user.ID); err != nil || status.Enabled {
		t.Fatalf("status after disable = %+v, %v", status, err)
	}
	if !errors.Is(login(""), domain.ErrServiceUnavailable) {
		t.Fatal("a removed factor is still asked for")
	}
}
