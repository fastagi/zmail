package domain

import "testing"

func TestMailboxAccessFor(t *testing.T) {
	owner := uint(10)
	personal := &Mailbox{ID: 1, OrganizationID: 7, Type: MailboxTypePersonal, OwnerUserID: &owner}
	orphan := &Mailbox{ID: 2, OrganizationID: 7, Type: MailboxTypePersonal}
	shared := &Mailbox{ID: 3, OrganizationID: 7, Type: MailboxTypeShared}
	viewer := func(userID, membershipID uint, role OrganizationRole) OrganizationContext {
		return OrganizationContext{OrganizationID: 7, OrganizationName: "Acme", OrganizationSlug: "acme",
			MembershipID: membershipID, UserID: userID, Role: role}
	}
	member := func(membershipID uint, permission MailboxPermission) *MailboxMember {
		return &MailboxMember{MailboxID: 3, MembershipID: membershipID, Permission: permission}
	}
	none := MailboxAccess{}
	administer := MailboxAccess{Administer: true, SeeHistory: true}
	administerShared := MailboxAccess{Administer: true, SeeHistory: true, ManageMembers: true}
	ownerAccess := MailboxAccess{View: true, Send: true, SeeHistory: true}

	tests := []struct {
		name    string
		mailbox *Mailbox
		viewer  OrganizationContext
		member  *MailboxMember
		want    MailboxAccess
	}{
		{"personal owner", personal, viewer(10, 100, OrganizationRoleMember), nil, ownerAccess},
		{"personal colleague", personal, viewer(11, 110, OrganizationRoleMember), nil, none},
		{"personal admin", personal, viewer(12, 120, OrganizationRoleAdmin), nil, administer},
		{"personal organization owner", personal, viewer(12, 120, OrganizationRoleOwner), nil, administer},
		{"personal owner who is admin", personal, viewer(10, 100, OrganizationRoleAdmin), nil,
			MailboxAccess{View: true, Send: true, Administer: true, SeeHistory: true}},
		{"personal ignores a member row", personal, viewer(11, 110, OrganizationRoleMember),
			&MailboxMember{MailboxID: 1, MembershipID: 110, Permission: MailboxPermissionManage}, none},
		{"personal without owner", orphan, viewer(10, 100, OrganizationRoleMember), nil, none},
		{"shared non-member", shared, viewer(11, 110, OrganizationRoleMember), nil, none},
		{"shared view", shared, viewer(11, 110, OrganizationRoleMember), member(110, MailboxPermissionView),
			MailboxAccess{View: true}},
		{"shared send", shared, viewer(11, 110, OrganizationRoleMember), member(110, MailboxPermissionSend),
			MailboxAccess{View: true, Send: true}},
		{"shared manage", shared, viewer(11, 110, OrganizationRoleMember), member(110, MailboxPermissionManage),
			MailboxAccess{View: true, Send: true, ManageMembers: true, SeeHistory: true}},
		{"shared row of another membership", shared, viewer(11, 110, OrganizationRoleMember),
			member(999, MailboxPermissionManage), none},
		{"shared row of another mailbox", shared, viewer(11, 110, OrganizationRoleMember),
			&MailboxMember{MailboxID: 9, MembershipID: 110, Permission: MailboxPermissionManage}, none},
		{"shared unknown permission", shared, viewer(11, 110, OrganizationRoleMember), member(110, "owner"), none},
		{"shared admin without row", shared, viewer(12, 120, OrganizationRoleAdmin), nil, administerShared},
		{"shared admin with view row", shared, viewer(12, 120, OrganizationRoleAdmin), member(120, MailboxPermissionView),
			MailboxAccess{View: true, Administer: true, SeeHistory: true, ManageMembers: true}},
		{"other organization", shared, OrganizationContext{OrganizationID: 8, MembershipID: 110, UserID: 11,
			Role: OrganizationRoleOwner}, member(110, MailboxPermissionManage), none},
		{"nil mailbox", nil, viewer(10, 100, OrganizationRoleOwner), nil, none},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MailboxAccessFor(tt.mailbox, tt.viewer, tt.member)
			if got != tt.want {
				t.Fatalf("MailboxAccessFor = %+v; want %+v", got, tt.want)
			}
			if got.Any() != (tt.want != none) {
				t.Fatalf("Any() = %v for %+v", got.Any(), got)
			}
		})
	}
}

func TestMailboxPermissionIncludes(t *testing.T) {
	levels := []MailboxPermission{MailboxPermissionView, MailboxPermissionSend, MailboxPermissionManage}
	for i, holder := range levels {
		for j, needed := range levels {
			if got := holder.Includes(needed); got != (i >= j) {
				t.Fatalf("%s.Includes(%s) = %v", holder, needed, got)
			}
		}
		if holder.Includes("admin") || MailboxPermission("admin").Includes(holder) {
			t.Fatalf("unknown permissions must include nothing and be included by nothing")
		}
	}
}
