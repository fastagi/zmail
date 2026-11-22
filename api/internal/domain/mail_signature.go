package domain

import (
	"context"
	"time"
)

// MailSignatureMaxLength caps a signature in characters.
const MailSignatureMaxLength = 2000

// MailSignature is the plain-text signature a member appends to what they write from a mailbox.
// Each member keeps their own per mailbox; an empty Body means none is set.
type MailSignature struct {
	MailboxID uint
	UserID    uint
	Body      string
	// UpdatedAt is nil until a signature is saved.
	UpdatedAt *time.Time
}

// MailSignatureRepository stores members' signatures.
type MailSignatureRepository interface {
	// FindMailSignature reads a member's signature; a missing row is an empty, unsaved one.
	FindMailSignature(ctx context.Context, mailboxID, userID uint) (MailSignature, error)
	// SaveMailSignature replaces a member's signature; an empty Body removes it.
	SaveMailSignature(ctx context.Context, signature MailSignature, now time.Time) (MailSignature, error)
}
