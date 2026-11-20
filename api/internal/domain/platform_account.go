package domain

import (
	"context"
	"time"
)

const (
	// MaxPlatformNameLength bounds a trimmed platform name, in characters.
	MaxPlatformNameLength = 64
	// MaxPlatformHandleLength bounds a trimmed account handle, in characters.
	MaxPlatformHandleLength = 200
	// MaxPlatformProfileURLLength bounds a profile URL, in bytes.
	MaxPlatformProfileURLLength = 2048
	// MaxPlatformAccountNotesLength bounds trimmed notes, in characters.
	MaxPlatformAccountNotesLength = 2000
	// MaxPlatformAccountQueryLength bounds the ledger search text, in characters.
	MaxPlatformAccountQueryLength = 100
)

// PlatformAccountStatus is where an account on an outside platform stands.
type PlatformAccountStatus string

const (
	PlatformAccountStatusInvited              PlatformAccountStatus = "invited"
	PlatformAccountStatusAwaitingConfirmation PlatformAccountStatus = "awaiting_confirmation"
	PlatformAccountStatusActive               PlatformAccountStatus = "active"
	PlatformAccountStatusTimedOut             PlatformAccountStatus = "timed_out"
	PlatformAccountStatusNeedsReview          PlatformAccountStatus = "needs_review"
	PlatformAccountStatusRetired              PlatformAccountStatus = "retired"
)

// Valid reports whether the status is one the ledger knows.
func (s PlatformAccountStatus) Valid() bool {
	switch s {
	case PlatformAccountStatusInvited, PlatformAccountStatusAwaitingConfirmation, PlatformAccountStatusActive,
		PlatformAccountStatusTimedOut, PlatformAccountStatusNeedsReview, PlatformAccountStatusRetired:
		return true
	}
	return false
}

// PlatformAccountEventKind names one entry of an account's history.
type PlatformAccountEventKind string

const (
	PlatformAccountEventCreated        PlatformAccountEventKind = "created"
	PlatformAccountEventStatusChanged  PlatformAccountEventKind = "status_changed"
	PlatformAccountEventOwnerChanged   PlatformAccountEventKind = "owner_changed"
	PlatformAccountEventMailboxChanged PlatformAccountEventKind = "mailbox_changed"
	// PlatformAccountEventDeleted is the last event of an account; it outlives the account.
	PlatformAccountEventDeleted PlatformAccountEventKind = "deleted"
)

// PlatformAccount is one account an organization holds on an outside platform (Medium, Behance,
// and so on): the mailbox it was registered with, the mail role it belongs to, and the member
// responsible for it. The ledger grants no mailbox access. Platform and handle are unique in the
// organization ignoring case.
type PlatformAccount struct {
	ID             uint
	OrganizationID uint
	Platform       string
	Handle         string
	// ProfileURL is an http(s) URL; empty when unknown.
	ProfileURL string
	// RoleID, MailboxID, and OwnerUserID are nil when unset or when the referenced row is gone.
	RoleID      *uint
	MailboxID   *uint
	OwnerUserID *uint
	// MailboxAddress is the registration mailbox's address; empty when there is none, the mailbox
	// was deleted, or the viewer may not see it.
	MailboxAddress string
	Status         PlatformAccountStatus
	Notes          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// PlatformAccountEvent is one entry of an account's handover and status history. From and To are
// short text: a status, or a user or mailbox id; empty for none.
type PlatformAccountEvent struct {
	ID             uint
	OrganizationID uint
	AccountID      uint
	Kind           PlatformAccountEventKind
	// ActorUserID is who made the change; nil when that user's account was deleted.
	ActorUserID *uint
	From        string
	To          string
	CreatedAt   time.Time
}

// PlatformAccountFilter narrows a ledger listing. Zero values do not filter.
type PlatformAccountFilter struct {
	// Query matches platform, handle, or the registration address, ignoring case.
	Query       string
	RoleID      uint
	MailboxID   uint
	OwnerUserID uint
	Status      PlatformAccountStatus
	// Visible, when not nil, limits a member's listing to accounts registered with one of these
	// mailboxes or owned by VisibleOwner. Nil lists every account (owners and admins).
	Visible      []uint
	VisibleOwner uint
	// AddressMailboxIDs, when not nil, limits Query's address match to these mailboxes, so a
	// member cannot probe the address of a mailbox they may not see.
	AddressMailboxIDs []uint
	Page              int
	PerPage           int
}

// PlatformAccountHandover is the result of moving every account of one owner to another.
type PlatformAccountHandover struct {
	FromUserID uint
	ToUserID   uint
	// AccountIDs are the accounts moved, ascending.
	AccountIDs []uint
}

// PlatformAccountRepository stores the platform account ledger and its history. Every call is
// scoped to one organization: an account of another organization does not exist. Writes record
// their events in the same transaction.
type PlatformAccountRepository interface {
	ListPlatformAccounts(ctx context.Context, organizationID uint, filter PlatformAccountFilter) ([]*PlatformAccount, int64, error)
	FindPlatformAccount(ctx context.Context, organizationID, id uint) (*PlatformAccount, error)
	// CreatePlatformAccount stores the account and its created event.
	CreatePlatformAccount(ctx context.Context, account *PlatformAccount, actorUserID uint) error
	// UpdatePlatformAccount replaces every field and records an event per changed status, owner,
	// and mailbox.
	UpdatePlatformAccount(ctx context.Context, account *PlatformAccount, actorUserID uint) error
	// DeletePlatformAccount records the deleted event and removes the account.
	DeletePlatformAccount(ctx context.Context, organizationID, id, actorUserID uint) error
	// ListPlatformAccountEvents lists an account's events, newest first.
	ListPlatformAccountEvents(ctx context.Context, organizationID, accountID uint, page, perPage int) ([]*PlatformAccountEvent, int64, error)
	// HandOverPlatformAccounts moves every account owned by fromUserID to toUserID with one
	// owner_changed event each, and returns the ids moved.
	HandOverPlatformAccounts(ctx context.Context, organizationID, fromUserID, toUserID, actorUserID uint) ([]uint, error)
}
