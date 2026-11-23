package domain

import (
	"context"
	"errors"
	"time"
)

// MailClientProtocol is a protocol a mail app signs in with using a mail client password.
type MailClientProtocol string

const (
	// MailClientProtocolIMAP reads mail.
	MailClientProtocolIMAP MailClientProtocol = "imap"
	// MailClientProtocolSMTP submits mail.
	MailClientProtocolSMTP MailClientProtocol = "smtp"
)

// IsValid reports whether the protocol is known.
func (p MailClientProtocol) IsValid() bool {
	return p == MailClientProtocolIMAP || p == MailClientProtocolSMTP
}

// MailAppPasswordRevokeReason records who revoked a mail client password.
type MailAppPasswordRevokeReason string

const (
	// MailAppPasswordRevokedByUser is the password's own member.
	MailAppPasswordRevokedByUser MailAppPasswordRevokeReason = "user"
	// MailAppPasswordRevokedByAdmin is an organization owner or admin.
	MailAppPasswordRevokedByAdmin MailAppPasswordRevokeReason = "admin"
)

// MailAppPassword is one device's mail client password. Only a keyed hash of the secret is
// stored; the secret itself exists once, in the response that created it.
type MailAppPassword struct {
	ID             uint
	OrganizationID uint
	MembershipID   uint
	UserID         uint
	Name           string
	ScopeIMAP      bool
	ScopeSMTP      bool
	// Hint is the secret's last four letters, to tell devices apart.
	Hint             string
	LastUsedAt       *time.Time
	LastUsedIP       string
	LastUsedProtocol MailClientProtocol
	RevokedAt        *time.Time
	RevokedBy        *uint
	RevokeReason     MailAppPasswordRevokeReason
	// UserEmail and UserName identify the owner; only the organization directory fills them.
	UserEmail string
	UserName  string
	CreatedAt time.Time
}

// Scopes lists the protocols the password may sign in with, IMAP first.
func (p *MailAppPassword) Scopes() []MailClientProtocol {
	scopes := make([]MailClientProtocol, 0, 2)
	if p.ScopeIMAP {
		scopes = append(scopes, MailClientProtocolIMAP)
	}
	if p.ScopeSMTP {
		scopes = append(scopes, MailClientProtocolSMTP)
	}
	return scopes
}

// MailAppPasswordPruner deletes mail client passwords revoked longer than their retention; the mail
// worker runs it every pass.
type MailAppPasswordPruner interface {
	PurgeRevokedAppPasswords(ctx context.Context, now time.Time, limit int) (int, error)
}

// CredentialReverifier confirms, for a signed-in user, the account password and, when two-factor
// sign-in is on, an authenticator or recovery code, before a change that must not rest on the
// session alone. It returns ErrInvalidCredentials for a wrong password or an inactive account,
// ErrTOTPRequired when a second factor is needed and missing, and ErrTOTPInvalid for a wrong one.
type CredentialReverifier interface {
	ReverifyCredentials(ctx context.Context, userID uint, password, secondFactor string) error
}

// Errors of the mail client store. They never reach HTTP: the IMAP gateway turns them into
// protocol responses.
var (
	// ErrMailClientAuthFailed is every failed mail app sign-in, whatever step refused it, so a
	// response never tells which.
	ErrMailClientAuthFailed = errors.New("mail client authentication failed")
	// ErrMailClientFolderNotFound is a folder the signed-in member cannot see or that does not exist.
	ErrMailClientFolderNotFound = errors.New("mail client folder not found")
	// ErrMailClientSearchUnsupported is a search criterion the store cannot evaluate.
	ErrMailClientSearchUnsupported = errors.New("mail client search criterion is not supported")
	// ErrMailClientMessageNotFound is a UID that no longer names a readable message.
	ErrMailClientMessageNotFound = errors.New("mail client message not found")
)

// MailClientFlag is a message flag a mail app may change.
type MailClientFlag string

const (
	// MailClientFlagSeen is the member's own read state.
	MailClientFlagSeen MailClientFlag = "seen"
	// MailClientFlagFlagged is the member's own star.
	MailClientFlagFlagged MailClientFlag = "flagged"
)

// Folder keys of a mailbox's standard folders; a user folder is "folder:<id>".
const (
	MailClientFolderInbox   = "inbox"
	MailClientFolderSent    = "sent"
	MailClientFolderArchive = "archive"
	MailClientFolderTrash   = "trash"
)

// MailClientPrincipal is a signed-in mail app: the password it used, the membership it acts as, and
// the mailbox shown at its root (zero when the member has none).
type MailClientPrincipal struct {
	PasswordID    uint
	Viewer        OrganizationContext
	RootMailboxID uint
}

// MailClientFolder is one folder a mail app sees. Name is the folder's own name for a user folder
// and empty for a standard one; MailboxAddress names the mailbox of a shared folder.
type MailClientFolder struct {
	MailboxID      uint
	Key            string
	Name           string
	Shared         bool
	MailboxAddress string
}

// MailClientFolderState is a folder's UID state and the UIDs of its messages, ascending.
type MailClientFolderState struct {
	UIDValidity uint32
	UIDNext     uint32
	UIDs        []uint32
	Unseen      int
}

// MailClientMessage is one message of a folder with the member's flags on it.
type MailClientMessage struct {
	UID          uint32
	MessageID    uint
	Seen         bool
	Flagged      bool
	Answered     bool
	InternalDate time.Time
}

// MailClientUIDRange is an inclusive range of UIDs.
type MailClientUIDRange struct {
	Start, Stop uint32
}

// MailClientSearch is a search of one folder. Every set field must match; UIDSets each hold the
// ranges of one set, of which a message must fall in one. Not and Or nest further searches.
type MailClientSearch struct {
	UIDSets       [][]MailClientUIDRange
	Seen, Flagged *bool
	Answered      *bool
	Since, Before *time.Time
	From, To, Cc  []string
	Subject       []string
	MessageID     []string
	Body, Text    []string
	MatchNothing  bool
	Not           []MailClientSearch
	Or            [][2]MailClientSearch
}

// MailClientStore serves mail apps: it signs them in and reads and flags the mail of the mailboxes
// the member may view. Every read and change checks the member's access again.
type MailClientStore interface {
	// AuthenticateClient signs a mail app in; every refusal is ErrMailClientAuthFailed.
	AuthenticateClient(ctx context.Context, username, password string, protocol MailClientProtocol, ip string) (*MailClientPrincipal, error)
	// RevalidateClients reports, by password id, whether each principal may still sign in.
	RevalidateClients(ctx context.Context, principals []*MailClientPrincipal) (map[uint]bool, error)
	// ClientMailboxVisible reports whether the principal may still view the mailbox.
	ClientMailboxVisible(ctx context.Context, p *MailClientPrincipal, mailboxID uint) (bool, error)
	ClientFolders(ctx context.Context, p *MailClientPrincipal) ([]MailClientFolder, error)
	ClientFolderState(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string) (*MailClientFolderState, error)
	// ClientMessages lists the folder's messages among uids, or all of them when uids is nil.
	ClientMessages(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string, uids []uint32) ([]MailClientMessage, error)
	ClientMessageSource(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string, uid uint32) ([]byte, error)
	SetClientFlag(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string, uids []uint32, flag MailClientFlag, value bool) error
	SearchClient(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string, criteria MailClientSearch) ([]uint32, error)
}
