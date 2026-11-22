package domain

import (
	"context"
	"time"
)

// OrganizationRole is scoped to one organization and is not a global RBAC role.
type OrganizationRole string

const (
	OrganizationRoleOwner  OrganizationRole = "owner"
	OrganizationRoleAdmin  OrganizationRole = "admin"
	OrganizationRoleMember OrganizationRole = "member"
)

// IsValid reports whether the role belongs to the organization vocabulary.
func (r OrganizationRole) IsValid() bool {
	switch r {
	case OrganizationRoleOwner, OrganizationRoleAdmin, OrganizationRoleMember:
		return true
	default:
		return false
	}
}

// CanManageOrganization reports whether the role may change organization settings.
func (r OrganizationRole) CanManageOrganization() bool {
	return r == OrganizationRoleOwner || r == OrganizationRoleAdmin
}

// CanChangeMemberRoles reports whether the role may grant or revoke admin.
func (r OrganizationRole) CanChangeMemberRoles() bool {
	return r == OrganizationRoleOwner
}

// CanRemoveMember reports whether the role may remove another membership.
func (r OrganizationRole) CanRemoveMember(target OrganizationRole) bool {
	switch r {
	case OrganizationRoleOwner:
		return target == OrganizationRoleAdmin || target == OrganizationRoleMember
	case OrganizationRoleAdmin:
		return target == OrganizationRoleMember
	default:
		return false
	}
}

// CanBeInvited reports whether the role may be granted by an invitation.
func (r OrganizationRole) CanBeInvited() bool {
	return r == OrganizationRoleAdmin || r == OrganizationRoleMember
}

// Organization is the tenant/account ownership boundary.
type Organization struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedBy uint      `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OrganizationMembership links one user to one organization-scoped role.
type OrganizationMembership struct {
	ID             uint             `json:"id"`
	OrganizationID uint             `json:"organization_id"`
	UserID         uint             `json:"user_id"`
	Role           OrganizationRole `json:"role"`
	Organization   *Organization    `json:"-"`
	User           *User            `json:"-"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// OrganizationMembershipRoleChange captures the persisted role transition.
type OrganizationMembershipRoleChange struct {
	Membership *OrganizationMembership
	BeforeRole OrganizationRole
}

// OrganizationOwnershipTransfer captures both post-transfer memberships.
type OrganizationOwnershipTransfer struct {
	PreviousOwner      *OrganizationMembership
	NewOwner           *OrganizationMembership
	NewOwnerBeforeRole OrganizationRole
}

// OrganizationInvitationStatus is the externally visible invitation lifecycle state.
type OrganizationInvitationStatus string

const (
	OrganizationInvitationStatusPending  OrganizationInvitationStatus = "pending"
	OrganizationInvitationStatusAccepted OrganizationInvitationStatus = "accepted"
	OrganizationInvitationStatusRevoked  OrganizationInvitationStatus = "revoked"
	OrganizationInvitationStatusExpired  OrganizationInvitationStatus = "expired"
)

// OrganizationInvitation is a one-time organization membership offer.
type OrganizationInvitation struct {
	ID             uint             `json:"id"`
	OrganizationID uint             `json:"organization_id"`
	InvitedBy      uint             `json:"invited_by"`
	Email          string           `json:"email"`
	Role           OrganizationRole `json:"role"`
	TokenHash      string           `json:"-"`
	ExpiresAt      time.Time        `json:"expires_at"`
	AcceptedAt     *time.Time       `json:"accepted_at,omitempty"`
	AcceptedBy     *uint            `json:"accepted_by,omitempty"`
	RevokedAt      *time.Time       `json:"revoked_at,omitempty"`
	RevokedBy      *uint            `json:"revoked_by,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// Status derives expiry without mutating invitation history on read.
func (i *OrganizationInvitation) Status(now time.Time) OrganizationInvitationStatus {
	if i != nil && i.AcceptedAt != nil {
		return OrganizationInvitationStatusAccepted
	}
	if i != nil && i.RevokedAt != nil {
		return OrganizationInvitationStatusRevoked
	}
	if i == nil || !now.Before(i.ExpiresAt) {
		return OrganizationInvitationStatusExpired
	}
	return OrganizationInvitationStatusPending
}

// OrganizationRepository is the persistence seam for membership-scoped organization access.
type OrganizationRepository interface {
	CreateWithOwner(ctx context.Context, organization *Organization, owner *OrganizationMembership) error
	FindForUser(ctx context.Context, organizationID, userID uint) (*OrganizationMembership, error)
	ResolveContext(ctx context.Context, organizationID, userID uint) (*OrganizationContext, error)
	ListForUser(ctx context.Context, userID uint, page, pageSize int) ([]*OrganizationMembership, int64, error)
	ListMembers(ctx context.Context, organizationID, userID uint, page, pageSize int) ([]*OrganizationMembership, int64, error)
	ChangeMemberRole(ctx context.Context, organizationID, userID, memberID uint, role OrganizationRole, now time.Time) (*OrganizationMembershipRoleChange, error)
	RemoveMember(ctx context.Context, organizationID, userID, memberID uint) (*OrganizationMembership, error)
	TransferOwnership(ctx context.Context, organizationID, userID, newOwnerMemberID uint, now time.Time) (*OrganizationOwnershipTransfer, error)
	Update(ctx context.Context, organization *Organization) error
	CountMembershipsForUser(ctx context.Context, userID uint) (total, owned int64, err error)
	// FindMembershipsByEmails returns the organization's memberships whose user's account email
	// equals one of emails ignoring case, with User (and its Email) loaded. Members of other
	// organizations are never returned.
	FindMembershipsByEmails(ctx context.Context, organizationID uint, emails []string) ([]*OrganizationMembership, error)
	// ListMembershipsByUserIDs returns the organization's memberships of the given users, with User
	// (and its Email) loaded. Users of other organizations are never returned.
	ListMembershipsByUserIDs(ctx context.Context, organizationID uint, userIDs []uint) ([]*OrganizationMembership, error)
	// ListManagerUserIDs returns the user ids of the organization's owners and admins, ascending.
	ListManagerUserIDs(ctx context.Context, organizationID uint) ([]uint, error)
}

// OrganizationInvitationRepository owns invitation persistence and atomic acceptance.
type OrganizationInvitationRepository interface {
	CreateInvitation(ctx context.Context, invitation *OrganizationInvitation, now time.Time) error
	ListInvitations(ctx context.Context, organizationID uint, page, pageSize int) ([]*OrganizationInvitation, int64, error)
	RevokeInvitation(ctx context.Context, organizationID, invitationID, revokedBy uint, now time.Time) (*OrganizationInvitation, error)
	AcceptInvitation(ctx context.Context, tokenHash string, userID uint, now time.Time) (*OrganizationMembership, *OrganizationInvitation, error)
}

// Organization membership events, published after the membership change commits.
const (
	EventOrganizationMemberRemoved = "organization.member.removed"
	EventOrganizationMemberJoined  = "organization.member.joined"
)

// OrganizationMemberRemovedEvent says a user left an organization or was removed from it. The
// membership no longer exists when it is published.
type OrganizationMemberRemovedEvent struct {
	OrganizationID uint
	UserID         uint
	MembershipID   uint
	occurredAt     time.Time
}

// NewOrganizationMemberRemovedEvent describes a membership removed at the given time.
func NewOrganizationMemberRemovedEvent(organizationID, userID, membershipID uint, at time.Time) OrganizationMemberRemovedEvent {
	return OrganizationMemberRemovedEvent{OrganizationID: organizationID, UserID: userID, MembershipID: membershipID, occurredAt: at}
}

func (e OrganizationMemberRemovedEvent) EventName() string     { return EventOrganizationMemberRemoved }
func (e OrganizationMemberRemovedEvent) OccurredAt() time.Time { return e.occurredAt }

// OrganizationMemberJoinedEvent says a membership was created: the organization's creator, an
// accepted invitation, or a returning user.
type OrganizationMemberJoinedEvent struct {
	OrganizationID uint
	UserID         uint
	MembershipID   uint
	occurredAt     time.Time
}

// NewOrganizationMemberJoinedEvent describes a membership created at the given time.
func NewOrganizationMemberJoinedEvent(organizationID, userID, membershipID uint, at time.Time) OrganizationMemberJoinedEvent {
	return OrganizationMemberJoinedEvent{OrganizationID: organizationID, UserID: userID, MembershipID: membershipID, occurredAt: at}
}

func (e OrganizationMemberJoinedEvent) EventName() string     { return EventOrganizationMemberJoined }
func (e OrganizationMemberJoinedEvent) OccurredAt() time.Time { return e.occurredAt }
