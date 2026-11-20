package mailbox

import (
	"bytes"
	"testing"
)

// Review Focus 2: a secret URL keeps only its host; anything else is six dots.
func TestMaskSecret(t *testing.T) {
	for value, want := range map[string]string{
		"482913":                                 "••••••",
		"":                                       "••••••",
		"https://u:p@reset.acme.com/t?code=1":    "reset.acme.com/••••",
		"http://reset.acme.com:8443/t#frag":      "reset.acme.com/••••",
		"HTTPS://Reset.Acme.com/t":               "reset.acme.com/••••",
		"reset.acme.com/t":                       "••••••",
		"ftp://files.acme.com/x":                 "••••••",
		"https:///no-host":                       "••••••",
		"a long secret value that is not a URL!": "••••••",
	} {
		if got := maskSecret(value); got != want {
			t.Errorf("maskSecret(%q) = %q; want %q", value, got, want)
		}
	}
}

func TestMaskRecipient(t *testing.T) {
	for address, want := range map[string]string{
		"lilei@example.com": "li••@example.com",
		"ab@example.com":    "a••@example.com",
		"a@example.com":     "a••@example.com",
		"abc@example.com":   "ab••@example.com",
	} {
		if got := maskRecipient(address); got != want {
			t.Errorf("maskRecipient(%q) = %q; want %q", address, got, want)
		}
	}
}

func TestTransactionalFingerprint(t *testing.T) {
	key := transactionalFingerprintKey("an-encryption-key-of-at-least-32-chars")
	base := transactionalFingerprint(key, "signup-code", "lilei@example.com", map[string]string{"code": "1", "app": "Acme"})
	same := transactionalFingerprint(key, "signup-code", "lilei@example.com", map[string]string{"app": "Acme", "code": "1"})
	if !bytes.Equal(base, same) || len(base) != 32 {
		t.Fatalf("fingerprint depends on variable order or has length %d", len(base))
	}
	for name, other := range map[string][]byte{
		"template":  transactionalFingerprint(key, "reset", "lilei@example.com", map[string]string{"code": "1", "app": "Acme"}),
		"recipient": transactionalFingerprint(key, "signup-code", "hanmei@example.com", map[string]string{"code": "1", "app": "Acme"}),
		"value":     transactionalFingerprint(key, "signup-code", "lilei@example.com", map[string]string{"code": "2", "app": "Acme"}),
		"key":       transactionalFingerprint(transactionalFingerprintKey("another-encryption-key-of-32-chars!!"), "signup-code", "lilei@example.com", map[string]string{"code": "1", "app": "Acme"}),
	} {
		if bytes.Equal(base, other) {
			t.Errorf("a different %s gave the same fingerprint", name)
		}
	}
}

func TestSimulatorOutcome(t *testing.T) {
	tests := []struct {
		to                 string
		outcome            string
		simulated, allowed bool
	}{
		{"lilei@example.com", "", false, true},
		{"delivered@test.zmail.im", "delivered", true, true},
		{"bounced+u1@test.zmail.im", "bounced", true, true},
		{"soft-bounced@test.zmail.im", "soft-bounced", true, true},
		{"complained+x@test.zmail.im", "complained", true, true},
		{"suppressed@test.zmail.im", "suppressed", true, true},
		{"nobody@test.zmail.im", "", true, false},
		{"delivered@sub.test.zmail.im", "", false, true},
	}
	for _, test := range tests {
		outcome, simulated, ok := simulatorOutcome(test.to, "test.zmail.im")
		if outcome != test.outcome || simulated != test.simulated || ok != test.allowed {
			t.Errorf("simulatorOutcome(%q) = %q, %v, %v; want %q, %v, %v",
				test.to, outcome, simulated, ok, test.outcome, test.simulated, test.allowed)
		}
	}
}
