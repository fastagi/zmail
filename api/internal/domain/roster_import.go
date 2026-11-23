package domain

import "context"

// OrganizationRosterInviter is the organization starter's seam for a roster import run by another
// starter: it finds and creates invitations with the grants the new membership starts with. The
// caller has already checked that the actor manages the organization.
type OrganizationRosterInviter interface {
	// FindPendingInvitationByEmail returns the organization's pending, unexpired invitation for the
	// lowercased email, or nil when there is none.
	FindPendingInvitationByEmail(ctx context.Context, organizationID uint, email string) (*OrganizationInvitation, error)
	// InviteRosterMember creates an invitation for the lowercased email with the role and grants,
	// emails it, and audits it like any invitation. It returns the invitation and the email
	// send status (accepted_by_provider, failed, or not_configured). An email that already
	// belongs to a member is ErrOrganizationMemberAlreadyExists; one with a pending invitation is
	// ErrOrganizationInvitationAlreadyPending.
	InviteRosterMember(
		ctx context.Context,
		actorUserID, organizationID uint,
		email string,
		role OrganizationRole,
		features MemberFeatures,
	) (*OrganizationInvitation, string, error)
}

// MailboxRosterRepository stores the mailbox side of a roster import: personal mailboxes reserved
// for an invited email until that person joins.
type MailboxRosterRepository interface {
	// ClaimReservedMailboxes makes the user the owner of the organization's live personal mailboxes
	// reserved for the lowercased email and clears the reservation, returning them.
	ClaimReservedMailboxes(ctx context.Context, organizationID, userID uint, email string) ([]*Mailbox, error)
	// OwnsPersonalMailbox reports whether the user owns a live Zmail personal mailbox in the
	// organization.
	OwnsPersonalMailbox(ctx context.Context, organizationID, userID uint) (bool, error)
}
