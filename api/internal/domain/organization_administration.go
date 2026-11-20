package domain

import "context"

// OrganizationAdminGrant is the outcome of making a user an organization admin.
type OrganizationAdminGrant struct {
	// Membership is the user's membership after the grant, with Organization loaded.
	Membership *OrganizationMembership
	// BeforeRole is the role the user had before; empty when the grant created the membership.
	BeforeRole OrganizationRole
}

// Created reports whether the grant created the membership.
func (g OrganizationAdminGrant) Created() bool { return g.BeforeRole == "" }

// Changed reports whether the grant created the membership or changed its role.
func (g OrganizationAdminGrant) Changed() bool {
	return g.Membership != nil && g.BeforeRole != g.Membership.Role
}

// OrganizationAdministrator is the organization starter's seam for operator console commands. It
// applies no membership check: the operator is not a member, so callers must only be operator
// tooling that runs on the server.
type OrganizationAdministrator interface {
	// FindOrganizationManagers returns the organization and its owner and admin memberships, with
	// User and its Email loaded: owners first, then by membership age. An unknown organization is
	// ErrOrganizationNotFound.
	FindOrganizationManagers(ctx context.Context, organizationID uint) (*Organization, []*OrganizationMembership, error)
	// FindMembership returns the user's membership in the organization, or nil when there is none.
	FindMembership(ctx context.Context, organizationID, userID uint) (*OrganizationMembership, error)
	// GrantAdmin makes the user an admin: it creates an admin membership, raises a member to
	// admin, and leaves an admin or owner as they are. A created membership is announced as
	// joined, like an accepted invitation.
	GrantAdmin(ctx context.Context, organizationID, userID uint) (*OrganizationAdminGrant, error)
}
