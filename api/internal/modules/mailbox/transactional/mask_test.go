package transactional

import (
	"bytes"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestMaskSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "https url masks path and query", value: "https://App.Example.com/reset?token=abc123", want: "app.example.com" + MaskedURLPath},
		{name: "http url with whitespace", value: "  http://login.example.org:8080/magic/xyz  ", want: "login.example.org" + MaskedURLPath},
		{name: "otp code", value: "847291", want: MaskedSecret},
		{name: "non-http scheme", value: "ftp://files.example.com/secret", want: MaskedSecret},
		{name: "scheme without host", value: "https:///only-path", want: MaskedSecret},
		{name: "invalid url", value: "://bad-url", want: MaskedSecret},
		{name: "empty value", value: "", want: MaskedSecret},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MaskSecret(tc.value); got != tc.want {
				t.Fatalf("MaskSecret(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestMaskRecipient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
		want    string
	}{
		{name: "standard address keeps first two runes", address: "alice@example.com", want: "al" + MaskedLocalPart + "@example.com"},
		{name: "two-char local part keeps one", address: "ab@example.com", want: "a" + MaskedLocalPart + "@example.com"},
		{name: "single-char local part keeps one", address: "x@example.com", want: "x" + MaskedLocalPart + "@example.com"},
		{name: "empty local part", address: "@example.com", want: MaskedLocalPart + "@example.com"},
		{name: "missing at sign", address: "not-an-email", want: MaskedLocalPart},
		{name: "multibyte local part", address: "\u00e9lise@example.com", want: "\u00e9l" + MaskedLocalPart + "@example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MaskRecipient(tc.address); got != tc.want {
				t.Fatalf("MaskRecipient(%q) = %q, want %q", tc.address, got, tc.want)
			}
		})
	}
}

func TestFingerprintDeterminism(t *testing.T) {
	t.Parallel()

	keyA := FingerprintKey("secret-key-a")
	keyA2 := FingerprintKey("secret-key-a")
	keyB := FingerprintKey("secret-key-b")

	if !bytes.Equal(keyA, keyA2) {
		t.Fatalf("FingerprintKey should be deterministic for the same secret")
	}
	if bytes.Equal(keyA, keyB) {
		t.Fatalf("FingerprintKey should differ for different secrets")
	}

	fp1 := Fingerprint(keyA, "welcome", "alice@example.com", map[string]string{"name": "Alice", "code": "123"})
	fp2 := Fingerprint(keyA, "welcome", "alice@example.com", map[string]string{"code": "123", "name": "Alice"})
	if !bytes.Equal(fp1, fp2) {
		t.Fatalf("Fingerprint should be deterministic regardless of map iteration order")
	}

	fpNil := Fingerprint(keyA, "welcome", "alice@example.com", nil)
	fpEmpty := Fingerprint(keyA, "welcome", "alice@example.com", map[string]string{})
	if !bytes.Equal(fpNil, fpEmpty) {
		t.Fatalf("Fingerprint should treat nil and empty variable maps identically")
	}

	fpDiffRecipient := Fingerprint(keyA, "welcome", "bob@example.com", map[string]string{"name": "Alice", "code": "123"})
	if bytes.Equal(fp1, fpDiffRecipient) {
		t.Fatalf("Fingerprint should change when recipient changes")
	}
	fpDiffKey := Fingerprint(keyB, "welcome", "alice@example.com", map[string]string{"name": "Alice", "code": "123"})
	if bytes.Equal(fp1, fpDiffKey) {
		t.Fatalf("Fingerprint should change when HMAC key changes")
	}
}

func TestSimulatorOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		to        string
		simDomain string
		wantOut   string
		wantSim   bool
		wantOK    bool
	}{
		{name: "delivered outcome", to: "delivered@sim.zmail.test", simDomain: "sim.zmail.test", wantOut: "delivered", wantSim: true, wantOK: true},
		{name: "plus tag stripped and case-insensitive", to: "  Soft-Bounced+tag1@SIM.ZMAIL.TEST ", simDomain: "sim.zmail.test", wantOut: "soft-bounced", wantSim: true, wantOK: true},
		{name: "unknown outcome on simulator domain", to: "unknown@sim.zmail.test", simDomain: "sim.zmail.test", wantOut: "", wantSim: true, wantOK: false},
		{name: "regular domain is not simulated", to: "delivered@example.com", simDomain: "sim.zmail.test", wantOut: "", wantSim: false, wantOK: true},
		{name: "empty simulator domain disables simulation", to: "delivered@sim.zmail.test", simDomain: "", wantOut: "", wantSim: false, wantOK: true},
		{name: "malformed address without at", to: "delivered", simDomain: "sim.zmail.test", wantOut: "", wantSim: false, wantOK: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, sim, ok := SimulatorOutcome(tc.to, tc.simDomain)
			if out != tc.wantOut || sim != tc.wantSim || ok != tc.wantOK {
				t.Fatalf("SimulatorOutcome(%q, %q) = (%q, %v, %v), want (%q, %v, %v)",
					tc.to, tc.simDomain, out, sim, ok, tc.wantOut, tc.wantSim, tc.wantOK)
			}
		})
	}
}

func TestPGTimeNormalizeEmailAndMessagePORoundtrip(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("UTC+3", 3*3600)
	raw := time.Date(2026, 6, 15, 10, 20, 30, 123456789, loc)
	got := PGTime(raw)
	want := time.Date(2026, 6, 15, 7, 20, 30, 123456000, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("PGTime = %v, want %v", got, want)
	}
	if PGTimePointer(nil) != nil {
		t.Fatalf("PGTimePointer(nil) should return nil")
	}
	if ptr := PGTimePointer(&raw); ptr == nil || !ptr.Equal(want) {
		t.Fatalf("PGTimePointer(&raw) = %v, want %v", ptr, want)
	}

	if norm := NormalizeEmail("  Alice.Smith@Example.COM \t"); norm != "alice.smith@example.com" {
		t.Fatalf("NormalizeEmail = %q", norm)
	}

	reason := domain.TransactionalQueuedReason("rate_limit")
	msg := &domain.TransactionalMessage{
		ID:              10,
		OrganizationID:  2,
		Mode:            domain.APIKeyModeLive,
		TemplateKey:     "otp",
		FromAddress:     "no-reply@example.com",
		ToAddress:       "  USER@Example.com ",
		SubjectMasked:   "Your code",
		VariablesMasked: map[string]string{"code": MaskedSecret},
		Status:          domain.TransactionalStatusQueued,
		QueuedReason:    &reason,
		NextAttemptAt:   &raw,
		LastEvent:       domain.MailEventDelivered,
	}
	po, err := toMessagePO(msg)
	if err != nil {
		t.Fatalf("toMessagePO failed: %v", err)
	}
	if po.TableName() != "transactional_messages" || po.ToAddress != "user@example.com" {
		t.Fatalf("unexpected PO: table=%q to=%q", po.TableName(), po.ToAddress)
	}
	roundtripped := po.toDomain()
	if roundtripped.VariablesMasked["code"] != MaskedSecret || roundtripped.QueuedReason == nil ||
		*roundtripped.QueuedReason != reason || roundtripped.LastEvent != domain.MailEventDelivered {
		t.Fatalf("roundtrip mismatch: %+v", roundtripped)
	}
}
