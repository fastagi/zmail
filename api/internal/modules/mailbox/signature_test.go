package mailbox

import (
	"fmt"
	"testing"
	"time"
)

const testInboundSecret = "0123456789abcdef0123456789abcdef"

func signedHeader(at time.Time, recipient string, raw []byte) string {
	return fmt.Sprintf("t=%d,v1=%s", at.Unix(), signInbound(testInboundSecret, at.Unix(), recipient, raw))
}

func TestVerifyInbound(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	raw := []byte("From: a@b.c\r\n\r\nbody")
	header := signedHeader(now, "hello@mail.example", raw)
	tolerance := 5 * time.Minute

	if !verifyInbound(testInboundSecret, header, "hello@mail.example", raw, now, tolerance) {
		t.Fatal("valid signature rejected")
	}
	if !verifyInbound(testInboundSecret, header, "hello@mail.example", raw, now.Add(4*time.Minute), tolerance) {
		t.Fatal("signature inside the tolerance rejected")
	}

	rejected := map[string]bool{
		"empty secret":       verifyInbound("", header, "hello@mail.example", raw, now, tolerance),
		"wrong secret":       verifyInbound("another-secret-another-secret-123", header, "hello@mail.example", raw, now, tolerance),
		"other recipient":    verifyInbound(testInboundSecret, header, "other@mail.example", raw, now, tolerance),
		"tampered body":      verifyInbound(testInboundSecret, header, "hello@mail.example", append([]byte("x"), raw...), now, tolerance),
		"replayed later":     verifyInbound(testInboundSecret, header, "hello@mail.example", raw, now.Add(6*time.Minute), tolerance),
		"from the future":    verifyInbound(testInboundSecret, header, "hello@mail.example", raw, now.Add(-6*time.Minute), tolerance),
		"missing timestamp":  verifyInbound(testInboundSecret, "v1=abcd", "hello@mail.example", raw, now, tolerance),
		"missing signature":  verifyInbound(testInboundSecret, fmt.Sprintf("t=%d", now.Unix()), "hello@mail.example", raw, now, tolerance),
		"malformed header":   verifyInbound(testInboundSecret, "garbage", "hello@mail.example", raw, now, tolerance),
		"non-hex signature":  verifyInbound(testInboundSecret, fmt.Sprintf("t=%d,v1=zz", now.Unix()), "hello@mail.example", raw, now, tolerance),
		"empty header value": verifyInbound(testInboundSecret, "", "hello@mail.example", raw, now, tolerance),
	}
	for name, accepted := range rejected {
		if accepted {
			t.Fatalf("%s: signature accepted", name)
		}
	}
}

// TestSignInboundMatchesTheEdgeVector pins the signature format shared with the Cloudflare
// Email Worker; deploy/cloudflare/email-worker/worker.test.mjs asserts the same vector.
func TestSignInboundMatchesTheEdgeVector(t *testing.T) {
	const want = "e7c3e27b6f2a8c68f085c52ef15055793f8bba92e6845f9201c98e3ca3e4ef1c"
	got := signInbound(testInboundSecret, 1800000000, "Hello@Mail.Example", []byte("From: a@b.c\r\n\r\nbody"))
	if got != want {
		t.Fatalf("signInbound = %s; want %s", got, want)
	}
}
