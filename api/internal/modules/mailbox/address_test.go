package mailbox

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeLocalPart(t *testing.T) {
	valid := map[string]string{
		"Hello":        "hello",
		" team.sales ": "team.sales",
		"a":            "a",
		"dev_ops-1":    "dev_ops-1",
	}
	for input, want := range valid {
		got, err := normalizeLocalPart(input)
		if err != nil || got != want {
			t.Fatalf("normalizeLocalPart(%q) = %q, %v; want %q", input, got, err, want)
		}
	}

	invalid := []string{"", ".start", "end.", "two..dots", "has space", "a@b", "ünï", strings.Repeat("a", 65), "plus+tag"}
	for _, input := range invalid {
		if _, err := normalizeLocalPart(input); !errors.Is(err, ErrMailboxAddressInvalid) {
			t.Fatalf("normalizeLocalPart(%q) error = %v; want ErrMailboxAddressInvalid", input, err)
		}
	}

	for _, input := range []string{"postmaster", "Abuse", "no-reply"} {
		localPart, err := normalizeLocalPart(input)
		if err != nil {
			t.Fatalf("normalizeLocalPart(%q) error = %v", input, err)
		}
		if err := checkReservedLocalPart(localPart, "mail.example", "mail.example"); !errors.Is(err, ErrMailboxAddressReserved) {
			t.Fatalf("checkReservedLocalPart(%q) on the shared domain = %v; want ErrMailboxAddressReserved", input, err)
		}
		if err := checkReservedLocalPart(localPart, "news.example.com", "mail.example"); err != nil {
			t.Fatalf("checkReservedLocalPart(%q) on an organization domain = %v; want nil", input, err)
		}
	}
}

func TestNormalizeRecipients(t *testing.T) {
	seen := map[string]struct{}{}
	got, ok := normalizeRecipients([]string{"Bob@Example.com", " bob@example.com ", "amy@example.org"}, seen)
	if !ok || len(got) != 2 || got[0] != "bob@example.com" || got[1] != "amy@example.org" {
		t.Fatalf("normalizeRecipients = %v, %v", got, ok)
	}

	for _, bad := range []string{"", "not-an-address", "Bob <bob@example.com>", "bob@localhost", "bob@example.com\r\nBcc: x@y.z",
		strings.Repeat("a", 65) + "@example.com", "a@" + strings.Repeat("b", 64) + ".com",
		"a@" + strings.Repeat(strings.Repeat("b", 60)+".", 5) + "com", "bob@example..com"} {
		if _, ok := normalizeRecipients([]string{bad}, map[string]struct{}{}); ok {
			t.Fatalf("normalizeRecipients accepted %q", bad)
		}
	}
	longest := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 57) + ".com"
	if got, ok := normalizeRecipients([]string{longest}, map[string]struct{}{}); !ok || len(got[0]) != 254 {
		t.Fatalf("normalizeRecipients rejected a 254-character address: %v, %v", got, ok)
	}
}

func TestNormalizeMessageID(t *testing.T) {
	if got, ok := normalizeMessageID(" <abc@mail.example> "); !ok || got != "abc@mail.example" {
		t.Fatalf("normalizeMessageID = %q, %v", got, ok)
	}
	if got, ok := normalizeMessageID(""); !ok || got != "" {
		t.Fatalf("empty message id = %q, %v", got, ok)
	}
	for _, bad := range []string{"no-at-sign", "a b@c", "a@b>\r\nX: y"} {
		if _, ok := normalizeMessageID(bad); ok {
			t.Fatalf("normalizeMessageID accepted %q", bad)
		}
	}
}
