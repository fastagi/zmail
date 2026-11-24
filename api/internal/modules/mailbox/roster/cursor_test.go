package roster

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestDirectoryCursorRoundtrip(t *testing.T) {
	t.Parallel()

	for _, id := range []uint{1, 42, 999999, 1<<31 - 1} {
		token := EncodeDirectoryCursor(id)
		got, err := DecodeDirectoryCursor(token)
		if err != nil {
			t.Fatalf("DecodeDirectoryCursor(%q) for id %d failed: %v", token, id, err)
		}
		if got != id {
			t.Fatalf("DecodeDirectoryCursor(%q) = %d, want %d", token, got, id)
		}
	}
}

func TestDecodeDirectoryCursorRejectsMalformed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "exceeds max length", token: strings.Repeat("A", MaxDirectoryCursorLength+1)},
		{name: "invalid base64", token: "!!!not-base64!!!"},
		{name: "zero id", token: base64.RawURLEncoding.EncodeToString([]byte("0"))},
		{name: "negative number", token: base64.RawURLEncoding.EncodeToString([]byte("-5"))},
		{name: "non-numeric", token: base64.RawURLEncoding.EncodeToString([]byte("abc"))},
		{name: "overflow uint64", token: base64.RawURLEncoding.EncodeToString([]byte("999999999999999999999999999"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if id, err := DecodeDirectoryCursor(tc.token); !errors.Is(err, domain.ErrInvalidInput) || id != 0 {
				t.Fatalf("DecodeDirectoryCursor(%q) = (%d, %v), want (0, ErrInvalidInput)", tc.token, id, err)
			}
		})
	}
}
