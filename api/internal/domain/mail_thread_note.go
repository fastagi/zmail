package domain

import (
	"context"
	"errors"
	"time"
)

const (
	// MailThreadNoteMaxLength bounds a note's body, in characters.
	MailThreadNoteMaxLength = 2000
	// MailThreadNoteLimit bounds how many notes one thread keeps.
	MailThreadNoteLimit = 200
)

// ErrMailThreadNoteLimit reports that a thread already keeps the most notes allowed.
var ErrMailThreadNoteLimit = errors.New("mail thread note limit reached")

// MailThreadNote is an internal remark on a shared mailbox's conversation. Only the mailbox's
// members read it; it is never sent to the correspondent.
type MailThreadNote struct {
	ID        uint
	MailboxID uint
	ThreadID  uint
	// AuthorUserID is nil once the author's account is deleted.
	AuthorUserID *uint
	Body         string
	CreatedAt    time.Time
}

// MailThreadNoteRepository stores thread notes.
type MailThreadNoteRepository interface {
	// ListMailThreadNotes reads a thread's notes, oldest first; a thread keeps at most
	// MailThreadNoteLimit of them.
	ListMailThreadNotes(ctx context.Context, mailboxID, threadID uint) ([]MailThreadNote, error)
	// CreateMailThreadNote stores a note unless the thread already keeps limit notes
	// (ErrMailThreadNoteLimit), counted under a per-thread lock.
	CreateMailThreadNote(ctx context.Context, note MailThreadNote, limit int) (MailThreadNote, error)
	// DeleteMailThreadNote removes an author's own note from a thread; it reports whether one went.
	DeleteMailThreadNote(ctx context.Context, mailboxID, threadID, noteID, authorUserID uint) (bool, error)
}
