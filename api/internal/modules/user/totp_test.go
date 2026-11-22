package user

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 secret "12345678901234567890", last six digits.
func TestTOTPCodeMatchesRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for _, tc := range []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		got, err := totpCode(secret, totpStep(time.Unix(tc.unix, 0)))
		if err != nil || got != tc.code {
			t.Fatalf("totpCode(%d) = %q, %v; want %q", tc.unix, got, err, tc.code)
		}
	}
}

func TestMatchTOTPAcceptsSkewAndRejectsReplay(t *testing.T) {
	secret, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	previous, _ := totpCode(secret, totpStep(now)-1)
	step, ok := matchTOTP(secret, previous, now, 0)
	if !ok || step != totpStep(now)-1 {
		t.Fatalf("previous step code: step %d ok %v", step, ok)
	}
	if _, ok := matchTOTP(secret, previous, now, step); ok {
		t.Fatal("a used step was accepted again")
	}
	stale, _ := totpCode(secret, totpStep(now)-2)
	if _, ok := matchTOTP(secret, stale, now, 0); ok {
		t.Fatal("a code two steps old was accepted")
	}
	if _, ok := matchTOTP(secret, "12345", now, 0); ok {
		t.Fatal("a short code was accepted")
	}
	if uri := totpURI("Zmail", "a@b.c", secret); !strings.HasPrefix(uri, "otpauth://totp/Zmail:a@b.c?") || !strings.Contains(uri, "secret="+secret) {
		t.Fatalf("uri = %s", uri)
	}
}
