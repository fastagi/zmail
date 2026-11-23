package domain

import (
	"context"
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
