package domain

import (
	"context"
	"time"
)

const (
	// MaxMailRolesPerOrganization bounds how many mail roles one organization keeps.
	MaxMailRolesPerOrganization = 50
	// MaxMailRoleNameLength bounds a trimmed role name, in characters.
	MaxMailRoleNameLength = 64
	// MaxMailRoleDescriptionLength bounds a trimmed role description, in characters.
	MaxMailRoleDescriptionLength = 280
)

// MailRole groups an organization's mailboxes by who they represent. It is for display only: a
// role grants and revokes no mailbox access, and neither does its owner. Names are unique in the
// organization ignoring case.
type MailRole struct {
	ID             uint
	OrganizationID uint
	Name           string
	Color          MailLabelColor
	Description    string
	// OwnerUserID is the user responsible for the role; nil when nobody is.
	OwnerUserID *uint
	// MailboxIDs are the role's mailboxes the viewer may see; filled by the service per viewer.
	MailboxIDs []uint
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// MailRoleRepository stores mail roles and the mailbox each is assigned to. Every call is scoped
// to one organization: a role or mailbox of another organization does not exist.
type MailRoleRepository interface {
	// ListMailRoles lists the organization's roles by name ignoring case, then id; at most
	// MaxMailRolesPerOrganization of them.
	ListMailRoles(ctx context.Context, organizationID uint) ([]*MailRole, error)
	FindMailRole(ctx context.Context, organizationID, id uint) (*MailRole, error)
	// CreateMailRole stores the role unless the organization already has limit roles.
	CreateMailRole(ctx context.Context, role *MailRole, limit int) error
	// UpdateMailRole replaces the role's name, color, description, and owner.
	UpdateMailRole(ctx context.Context, role *MailRole) error
	// DeleteMailRole deletes the role and leaves its mailboxes ungrouped.
	DeleteMailRole(ctx context.Context, organizationID, id uint) error
	// AssignMailboxRole puts a live mailbox under the role, or ungroups it when roleID is nil.
	AssignMailboxRole(ctx context.Context, organizationID, mailboxID uint, roleID *uint) error
	// ClearMailRoleOwner removes the user as owner of the organization's roles and returns how
	// many roles changed.
	ClearMailRoleOwner(ctx context.Context, organizationID, userID uint) (int64, error)
}
