// Package syncfeed provides multi-client delta sync cursors, wire types
// matching @zmail/mail-core, and an in-process real-time SSE fan-out broker.
package syncfeed

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	// CurrentCursorVersion is the only supported wire version for delta sync cursors.
	CurrentCursorVersion = 1
	// MaxCursorLength bounds an encoded cursor token to prevent oversized inputs.
	MaxCursorLength = 512
)

// ErrInvalidCursor is returned when a cursor cannot be encoded or decoded safely.
var ErrInvalidCursor = errors.New("syncfeed: invalid cursor")

// Cursor represents an opaque delta sync position encoded for clients.
type Cursor struct {
	Version   int       `json:"v"`
	Seq       uint64    `json:"seq"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EncodeCursor validates c and serializes it as an unpadded URL-safe base64 JSON token.
// When c.Version is 0, CurrentCursorVersion is used.
func EncodeCursor(c Cursor) (string, error) {
	if c.Version == 0 {
		c.Version = CurrentCursorVersion
	}
	if c.Version != CurrentCursorVersion {
		return "", fmt.Errorf("%w: unsupported version %d", ErrInvalidCursor, c.Version)
	}
	if c.UpdatedAt.IsZero() {
		return "", fmt.Errorf("%w: updated_at is required", ErrInvalidCursor)
	}
	c.UpdatedAt = c.UpdatedAt.UTC()

	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > MaxCursorLength {
		return "", fmt.Errorf("%w: encoded cursor exceeds max length", ErrInvalidCursor)
	}
	return encoded, nil
}

// DecodeCursor strictly validates and decodes a token produced by EncodeCursor.
func DecodeCursor(token string) (Cursor, error) {
	if token == "" || len(token) > MaxCursorLength {
		return Cursor{}, fmt.Errorf("%w: invalid token length", ErrInvalidCursor)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: malformed base64", ErrInvalidCursor)
	}

	var c Cursor
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Cursor{}, fmt.Errorf("%w: malformed json", ErrInvalidCursor)
	}
	// Reject trailing tokens after the JSON object.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Cursor{}, fmt.Errorf("%w: trailing payload", ErrInvalidCursor)
	}
	if c.Version != CurrentCursorVersion {
		return Cursor{}, fmt.Errorf("%w: unsupported version %d", ErrInvalidCursor, c.Version)
	}
	if c.UpdatedAt.IsZero() {
		return Cursor{}, fmt.Errorf("%w: missing updated_at", ErrInvalidCursor)
	}
	c.UpdatedAt = c.UpdatedAt.UTC()
	return c, nil
}
