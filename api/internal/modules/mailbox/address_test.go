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
		if _, err := normalizeLocalPart(input); !errors.Is(err, ErrMailboxAddressReserved) {
			t.Fatalf("normalizeLocalPart(%q) error = %v; want ErrMailboxAddressReserved", input, err)
		}
	}
}

func TestNormalizeRecipients(t *testing.T) {
	seen := map[string]struct{}{}
	got, ok := normalizeRecipients([]string{"Bob@Example.com", " bob@example.com ", "amy@example.org"}, seen)
	if !ok || len(got) != 2 || got[0] != "bob@example.com" || got[1] != "amy@example.org" {
		t.Fatalf("normalizeRecipients = %v, %v", got, ok)
	}

	for _, bad := range []string{"", "not-an-address", "Bob <bob@example.com>", "bob@localhost", "bob@example.com\r\nBcc: x@y.z"} {
		if _, ok := normalizeRecipients([]string{bad}, map[string]struct{}{}); ok {
			t.Fatalf("normalizeRecipients accepted %q", bad)
		}
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
