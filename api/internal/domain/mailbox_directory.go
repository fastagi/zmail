package domain

import (
	"context"
	"time"
)

// MailboxDirectoryFilter selects one keyset page of the administration directory.
type MailboxDirectoryFilter struct {
	// Type narrows the page to one mailbox type; empty lists both.
	Type MailboxType
	// Query is a case-insensitive substring of the address or display name, matched literally.
	Query string
	// BeforeID lists mailboxes with a smaller id; 0 starts at the newest.
	BeforeID uint
	// Limit is the number of rows to read.
	Limit int
}

// MailboxDirectoryEntry is one mailbox of the directory with its counts. It never carries mail.
type MailboxDirectoryEntry struct {
	Mailbox     *Mailbox
	MemberCount int
	// SentLast24h counts the mailbox's personal-lane send attempts since the requested instant.
	SentLast24h int64
}

// MailboxDirectoryRepository reads the administration directory of an organization's mailboxes.
type MailboxDirectoryRepository interface {
	// ListMailboxDirectory lists live mailboxes matching filter, newest first, with member counts
	// and personal-lane send attempts since sentSince. Counts are read for the page's ids only.
	ListMailboxDirectory(ctx context.Context, organizationID uint, filter MailboxDirectoryFilter, sentSince time.Time) ([]*MailboxDirectoryEntry, error)
	// CountMailboxes counts the organization's live mailboxes.
	CountMailboxes(ctx context.Context, organizationID uint) (int64, error)
	// MailboxAddressUsed reports whether any mailbox, deleted ones included, holds the address.
	MailboxAddressUsed(ctx context.Context, address string) (bool, error)
	// ListMembersOfMailboxes lists the members of the given mailboxes.
	ListMembersOfMailboxes(ctx context.Context, mailboxIDs []uint) ([]*MailboxMember, error)
}
