package domain

import (
	"context"
	"time"
)

// MailThreadStatus is whether a shared mailbox's conversation still needs work.
type MailThreadStatus string

const (
	// MailThreadOpen still needs work; every thread starts open.
	MailThreadOpen MailThreadStatus = "open"
	// MailThreadClosed is done.
	MailThreadClosed MailThreadStatus = "closed"
)

// MailThreadAssignment is who works one conversation of a shared mailbox, and whether it is done.
// A thread nobody touched is open and unassigned, with no UpdatedAt.
type MailThreadAssignment struct {
	MailboxID      uint
	ThreadID       uint
	AssigneeUserID *uint
	Status         MailThreadStatus
	UpdatedBy      *uint
	UpdatedAt      *time.Time
}

// MailThreadAssignmentRepository stores thread assignments.
type MailThreadAssignmentRepository interface {
	// FindMailThreadAssignment reads a thread's assignment; one never set is open and unassigned.
	FindMailThreadAssignment(ctx context.Context, mailboxID, threadID uint) (MailThreadAssignment, error)
	// SaveMailThreadAssignment replaces a thread's assignment.
	SaveMailThreadAssignment(ctx context.Context, assignment MailThreadAssignment, now time.Time) (MailThreadAssignment, error)
}
