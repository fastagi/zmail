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
	// ErrMailClientMessageTooLarge is a message whose source is larger than
	// MailClientMaxSourceBytes; it is not loaded.
	ErrMailClientMessageTooLarge = errors.New("mail client message source is too large")
)

// MailClientMaxSourceBytes is the largest message source served to a mail app: the 5 MiB that
// inbound mail is capped at when it is stored.
const MailClientMaxSourceBytes = 5 << 20

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

// MailClientSubmission is one message a mail app submits over SMTP: the envelope it gave and the
// message as it sent it.
type MailClientSubmission struct {
	// EnvelopeFrom is the MAIL FROM address, lowercase.
	EnvelopeFrom string
	// Recipients are the RCPT TO addresses, lowercase, without repeats, at most MaxMailRecipients.
	Recipients []string
	// Raw is the RFC 5322 message, at most MailClientMaxSubmissionBytes.
	Raw []byte
}

// MailClientMaxSubmissionBytes is the SMTP SIZE a mail app may submit.
const MailClientMaxSubmissionBytes = 8 << 20

// MailClientSubmissionStatus is what became of an accepted submission.
type MailClientSubmissionStatus string

const (
	// MailClientSubmissionSent was handed to the provider.
	MailClientSubmissionSent MailClientSubmissionStatus = "sent"
	// MailClientSubmissionQueued waits for room in the organization's send limit; the mail worker
	// sends it.
	MailClientSubmissionQueued MailClientSubmissionStatus = "queued"
)

// MailClientSubmissionResult is an accepted submission and the message it stored.
type MailClientSubmissionResult struct {
	Status    MailClientSubmissionStatus
	MessageID uint
}

// MailClientSubmitErrorKind classifies a refused submission; the gateway turns each into one SMTP
// reply.
type MailClientSubmitErrorKind string

const (
	// MailClientSubmitSenderNotAllowed: the From or MAIL FROM is no mailbox the member may send from.
	MailClientSubmitSenderNotAllowed MailClientSubmitErrorKind = "sender_not_allowed"
	// MailClientSubmitTooLarge: the message, a body, the subject, or the attachments exceed a limit.
	MailClientSubmitTooLarge MailClientSubmitErrorKind = "too_large"
	// MailClientSubmitInvalid: the message cannot be sent as it is.
	MailClientSubmitInvalid MailClientSubmitErrorKind = "invalid"
	// MailClientSubmitConflict: another message was already sent with the same Message-ID.
	MailClientSubmitConflict MailClientSubmitErrorKind = "conflict"
	// MailClientSubmitLimitReached: the send limit and its waiting queue are both full.
	MailClientSubmitLimitReached MailClientSubmitErrorKind = "limit_reached"
	// MailClientSubmitPaused: the organization's sending is paused by its bounce or complaint rate.
	MailClientSubmitPaused MailClientSubmitErrorKind = "paused"
	// MailClientSubmitDeliveryFailed: the provider refused or could not be reached; a retry of the
	// same message sends it again.
	MailClientSubmitDeliveryFailed MailClientSubmitErrorKind = "delivery_failed"
	// MailClientSubmitInProgress: an earlier attempt of the same message is still being sent.
	MailClientSubmitInProgress MailClientSubmitErrorKind = "in_progress"
	// MailClientSubmitNotConfigured: this instance has no outbound provider.
	MailClientSubmitNotConfigured MailClientSubmitErrorKind = "not_configured"
)

// MailClientSubmitError is a refused submission. Reason is a short English sentence for the mail
// app; it never names an address, a subject, or anything else of the message.
type MailClientSubmitError struct {
	Kind   MailClientSubmitErrorKind
	Reason string
}

func (e *MailClientSubmitError) Error() string {
	if e.Reason == "" {
		return "mail submission refused: " + string(e.Kind)
	}
	return "mail submission refused: " + string(e.Kind) + ": " + e.Reason
}

// MailSubmissionStore serves mail apps that send: it signs them in, checks them again before each
// message, and sends what they submit through the same path as the console.
type MailSubmissionStore interface {
	// AuthenticateClient signs a mail app in; every refusal is ErrMailClientAuthFailed.
	AuthenticateClient(ctx context.Context, username, password string, protocol MailClientProtocol, ip string) (*MailClientPrincipal, error)
	// RevalidateClients reports, by password id, whether each principal may still sign in.
	RevalidateClients(ctx context.Context, principals []*MailClientPrincipal) (map[uint]bool, error)
	// ClientSenderAllowed reports whether the principal may send from the address.
	ClientSenderAllowed(ctx context.Context, p *MailClientPrincipal, address string) (bool, error)
	// SubmitClientMessage sends a submitted message; a refusal is a *MailClientSubmitError.
	SubmitClientMessage(ctx context.Context, p *MailClientPrincipal, submission MailClientSubmission) (*MailClientSubmissionResult, error)
}
