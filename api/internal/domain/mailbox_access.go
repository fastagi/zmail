package domain

import "time"

// MaxMailboxMembers bounds the members of one shared mailbox.
const MaxMailboxMembers = 100

// MailboxType says who a mailbox belongs to.
type MailboxType string

const (
	// MailboxTypePersonal mailboxes belong to one member, their owner, and have no other members.
	MailboxTypePersonal MailboxType = "personal"
	// MailboxTypeShared mailboxes are worked by named members, each with a permission.
	MailboxTypeShared MailboxType = "shared"
)

// Valid reports whether the type is a known value.
func (t MailboxType) Valid() bool {
	return t == MailboxTypePersonal || t == MailboxTypeShared
}

// MailboxPermission is what a member may do in a shared mailbox. Each level includes the
// previous one: view < send < manage.
type MailboxPermission string

const (
	// MailboxPermissionView reads the mailbox's mail.
	MailboxPermissionView MailboxPermission = "view"
	// MailboxPermissionSend also sends, replies, and deletes mail.
	MailboxPermissionSend MailboxPermission = "send"
	// MailboxPermissionManage also changes the members and sees the access history.
	MailboxPermissionManage MailboxPermission = "manage"
)

func (p MailboxPermission) level() int {
	switch p {
	case MailboxPermissionView:
		return 1
	case MailboxPermissionSend:
		return 2
	case MailboxPermissionManage:
		return 3
	default:
		return 0
	}
}

// Valid reports whether the permission is a known value.
func (p MailboxPermission) Valid() bool {
	return p.level() > 0
}

// Includes reports whether holding p also grants other. An unknown permission includes nothing
// and is included by nothing.
func (p MailboxPermission) Includes(other MailboxPermission) bool {
	return p.Valid() && other.Valid() && p.level() >= other.level()
}

// MailboxMember is one organization membership's permission on a shared mailbox. It is keyed
// by membership, so leaving the organization removes it and a rejoin starts without it.
type MailboxMember struct {
	MailboxID    uint
	MembershipID uint
	UserID       uint
	Permission   MailboxPermission
	// GrantedBy is the user who last set the permission.
	GrantedBy uint
	CreatedAt time.Time
}

// MailboxAccess is what one viewer may do with one mailbox.
type MailboxAccess struct {
	// View reads messages.
	View bool
	// Send sends, replies, and deletes messages.
	Send bool
	// ManageMembers changes the members of a shared mailbox.
	ManageMembers bool
	// Administer deletes the mailbox and runs organization-level operations on it. It never
	// grants reading or sending mail.
	Administer bool
	// SeeHistory reads the access history.
	SeeHistory bool
	// Organize deletes messages for good, empties the trash, and changes the mailbox's folders and
	// labels. Only the personal owner and manage members have it; the organization role alone never
	// grants it, because it destroys mail the role cannot read.
	Organize bool
}

// Any reports whether the viewer may know the mailbox exists.
func (a MailboxAccess) Any() bool {
	return a.View || a.Send || a.ManageMembers || a.Administer || a.SeeHistory || a.Organize
}

// MailboxAccessFor derives a viewer's access to a mailbox from the viewer's verified
// organization membership and the viewer's member row, if any. Access is the union of three
// rules: the organization role administers, the personal owner uses their own mailbox, and a
// shared member gets what the member row grants. A mailbox of another organization grants
// nothing.
func MailboxAccessFor(m *Mailbox, viewer OrganizationContext, member *MailboxMember) MailboxAccess {
	var access MailboxAccess
	if m == nil || m.OrganizationID == 0 || m.OrganizationID != viewer.OrganizationID {
		return access
	}
	if viewer.Role.CanManageOrganization() {
		access.Administer = true
		access.SeeHistory = true
		if m.Type == MailboxTypeShared {
			access.ManageMembers = true
		}
	}
	switch m.Type {
	case MailboxTypePersonal:
		if m.OwnerUserID != nil && viewer.UserID != 0 && *m.OwnerUserID == viewer.UserID {
			access.View = true
			access.Send = true
			access.SeeHistory = true
			access.Organize = true
		}
	case MailboxTypeExternal:
		// A synced copy of an outside account: only the member who bound it reads it, and nobody
		// sends from it or deletes its mail for good.
		if m.OwnerUserID != nil && viewer.UserID != 0 && *m.OwnerUserID == viewer.UserID {
			access.View = true
			access.SeeHistory = true
		}
	case MailboxTypeShared:
		if member != nil && member.MailboxID == m.ID && viewer.MembershipID != 0 && member.MembershipID == viewer.MembershipID {
			access.View = access.View || member.Permission.Includes(MailboxPermissionView)
			access.Send = access.Send || member.Permission.Includes(MailboxPermissionSend)
			if member.Permission.Includes(MailboxPermissionManage) {
				access.ManageMembers = true
				access.SeeHistory = true
				access.Organize = true
			}
		}
	}
	return access
}

// MailboxAccessEventType names one change in a mailbox's access history.
type MailboxAccessEventType string

const (
	MailboxAccessCreated         MailboxAccessEventType = "created"
	MailboxAccessMemberSet       MailboxAccessEventType = "member_set"
	MailboxAccessMemberRemoved   MailboxAccessEventType = "member_removed"
	MailboxAccessOwnerLeft       MailboxAccessEventType = "owner_left"
	MailboxAccessOwnerReturned   MailboxAccessEventType = "owner_returned"
	MailboxAccessHandedOver      MailboxAccessEventType = "handed_over"
	MailboxAccessSealedDeleted   MailboxAccessEventType = "sealed_deleted"
	MailboxAccessWebhooksChanged MailboxAccessEventType = "webhooks_changed"
	// MailboxAccessRenamed records a display-name change with detail from and to.
	MailboxAccessRenamed MailboxAccessEventType = "renamed"
)

// MailboxAccessEvent is one entry of a mailbox's access history. It stores user ids, ids, and
// counts only, never message content.
type MailboxAccessEvent struct {
	ID             uint
	OrganizationID uint
	MailboxID      uint
	Type           MailboxAccessEventType
	// ActorUserID is who made the change; nil for a change the system made.
	ActorUserID *uint
	// SubjectUserID is whose access changed; nil when no single user is concerned.
	SubjectUserID *uint
	// Detail holds machine-readable values such as membership_id and permission.
	Detail    map[string]any
	CreatedAt time.Time
}
