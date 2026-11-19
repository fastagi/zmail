package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// mailRoleFields are the fields of a mail role a write sets, in audit order.
var mailRoleFields = []string{"name", "color", "description", "owner_user_id"}

// MailRoleInput is the whole of a mail role a create or replace sets. The service trims the
// name and description; an empty color is gray and a nil owner is none.
type MailRoleInput struct {
	Name        string
	Color       domain.MailLabelColor
	Description string
	OwnerUserID *uint
}

// ListMailRoles lists the organization's mail roles with the mailboxes of each the viewer can
// see (any access, the set GET /v1/mailboxes lists), ascending. Owners and admins get every role;
// members only the roles holding a mailbox they can see. An owner who is no longer a member reads
// as none.
func (s *service) ListMailRoles(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.MailRole, error) {
	if s.roles == nil {
		return nil, domain.ErrServiceUnavailable
	}
	roles, err := s.roles.ListMailRoles(ctx, viewer.OrganizationID)
	if err != nil {
		return nil, err
	}
	visible, err := s.visibleRoleMailboxes(ctx, viewer)
	if err != nil {
		return nil, err
	}
	manager := viewer.Role.CanManageOrganization()
	listed := make([]*domain.MailRole, 0, len(roles))
	for _, role := range roles {
		role.MailboxIDs = visible[role.ID]
		if role.MailboxIDs == nil {
			role.MailboxIDs = []uint{}
		}
		if manager || len(role.MailboxIDs) > 0 {
			listed = append(listed, role)
		}
	}
	if err := s.hideFormerOwners(ctx, viewer.OrganizationID, listed); err != nil {
		return nil, err
	}
	return listed, nil
}

// CreateMailRole adds a mail role to the organization. Only owners and admins may.
func (s *service) CreateMailRole(ctx context.Context, viewer domain.OrganizationContext, input MailRoleInput) (*domain.MailRole, error) {
	if s.roles == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	role, err := s.validMailRole(ctx, viewer.OrganizationID, input)
	if err != nil {
		return nil, err
	}
	if err := s.roles.CreateMailRole(ctx, role, domain.MaxMailRolesPerOrganization); err != nil {
		return nil, err
	}
	role.MailboxIDs = []uint{}
	s.auditMailRole(ctx, "create", role.ID, mailRoleFields)
	slog.InfoContext(ctx, "mail.role.created",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("role_id", uint64(role.ID)))
	return role, nil
}

// UpdateMailRole replaces every field of a mail role; the last write wins. Only owners and
// admins may. The result carries the role's mailboxes the viewer can see.
func (s *service) UpdateMailRole(ctx context.Context, viewer domain.OrganizationContext, roleID uint, input MailRoleInput) (*domain.MailRole, error) {
	if s.roles == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	before, err := s.roles.FindMailRole(ctx, viewer.OrganizationID, roleID)
	if err != nil {
		return nil, err
	}
	role, err := s.validMailRole(ctx, viewer.OrganizationID, input)
	if err != nil {
		return nil, err
	}
	role.ID = roleID
	if err = s.roles.UpdateMailRole(ctx, role); err != nil {
		return nil, err
	}
	visible, err := s.visibleRoleMailboxes(ctx, viewer)
	if err != nil {
		return nil, err
	}
	role.MailboxIDs = visible[role.ID]
	if role.MailboxIDs == nil {
		role.MailboxIDs = []uint{}
	}
	s.auditMailRole(ctx, "update", role.ID, changedMailRoleFields(before, role))
	slog.InfoContext(ctx, "mail.role.updated",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("role_id", uint64(role.ID)))
	return role, nil
}

// DeleteMailRole deletes a mail role and leaves its mailboxes ungrouped. Only owners and admins
// may.
func (s *service) DeleteMailRole(ctx context.Context, viewer domain.OrganizationContext, roleID uint) error {
	if s.roles == nil {
		return domain.ErrServiceUnavailable
	}
	if !viewer.Role.CanManageOrganization() {
		return domain.ErrPermissionDenied
	}
	if err := s.roles.DeleteMailRole(ctx, viewer.OrganizationID, roleID); err != nil {
		return err
	}
	s.auditMailRole(ctx, "delete", roleID, mailRoleFields)
	slog.InfoContext(ctx, "mail.role.deleted",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("role_id", uint64(roleID)))
	return nil
}

// AssignMailboxRole puts a mailbox in one of the organization's mail roles, or takes it out of
// its role when roleID is nil. It needs administer on the mailbox, so only owners and admins may.
// A role id of zero is invalid: the handler passes it for a role_id that is missing or not a
// positive integer, so the field error answers after access. Assigning the current role succeeds
// again. The mailbox returned carries the viewer's access and, when they may read it, its unread
// count. The assignment grants and revokes no access.
func (s *service) AssignMailboxRole(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, roleID *uint) (*domain.Mailbox, error) {
	if s.roles == nil {
		return nil, domain.ErrServiceUnavailable
	}
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, canAdminister)
	if err != nil {
		return nil, err
	}
	if roleID != nil && *roleID == 0 {
		invalid := &ValidationError{}
		invalid.add("role_id", "must be a positive integer or null")
		return nil, invalid
	}
	if err = s.roles.AssignMailboxRole(ctx, viewer.OrganizationID, mailboxID, roleID); err != nil {
		return nil, err
	}
	s.auditChange(ctx, auditstarter.Change{
		Action: "update", Resource: "mailboxes.role", TargetType: "mailbox", TargetID: strconv.FormatUint(uint64(mailboxID), 10),
		Result:  domain.AuditResultSuccess,
		Changes: map[string]domain.AuditValueChange{"role_id": {Before: auditID(mailbox.RoleID), After: auditID(roleID)}},
	})
	logged := []any{slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("mailbox_id", uint64(mailboxID))}
	if roleID != nil {
		logged = append(logged, slog.Uint64("role_id", uint64(*roleID)))
	}
	slog.InfoContext(ctx, "mail.role.assigned", logged...)

	assigned, err := s.authorizeMailbox(ctx, viewer, mailboxID, nil)
	if err != nil {
		return nil, err
	}
	if assigned.Access.View {
		if s.inbox == nil {
			return nil, domain.ErrServiceUnavailable
		}
		unread, err := s.inbox.CountUnreadByMailboxIDs(ctx, viewer.OrganizationID, viewer.MembershipID, []uint{mailboxID})
		if err != nil {
			return nil, err
		}
		assigned.UnreadCount = unread[mailboxID]
	}
	return assigned, nil
}

// clearMailRoleOwner removes a member who left the organization as owner of its mail roles. A
// failure is logged and not returned: reads already hide an owner who is no longer a member.
func (s *service) clearMailRoleOwner(ctx context.Context, organizationID, userID uint) {
	if s.roles == nil {
		return
	}
	if _, err := s.roles.ClearMailRoleOwner(ctx, organizationID, userID); err != nil {
		slog.WarnContext(ctx, "mail.role.owner_clear_failed",
			slog.Uint64("organization_id", uint64(organizationID)), slog.Uint64("user_id", uint64(userID)),
			slog.String("error", err.Error()))
	}
}

// visibleRoleMailboxes maps each role id to the mailboxes in it the viewer has any access to,
// ascending. It reads access rows only and counts no mail.
func (s *service) visibleRoleMailboxes(ctx context.Context, viewer domain.OrganizationContext) (map[uint][]uint, error) {
	mailboxes, err := s.accessRows(ctx, viewer)
	if err != nil {
		return nil, err
	}
	visible := map[uint][]uint{}
	for _, mailbox := range mailboxes {
		if mailbox.RoleID != nil && mailbox.Access.Any() {
			visible[*mailbox.RoleID] = append(visible[*mailbox.RoleID], mailbox.ID)
		}
	}
	for _, ids := range visible {
		slices.Sort(ids)
	}
	return visible, nil
}

// hideFormerOwners clears the owner of each role whose owner is no longer a member. Each owner
// is looked up once.
func (s *service) hideFormerOwners(ctx context.Context, organizationID uint, roles []*domain.MailRole) error {
	current := map[uint]bool{}
	for _, role := range roles {
		if role.OwnerUserID == nil {
			continue
		}
		userID := *role.OwnerUserID
		member, known := current[userID]
		if !known {
			_, err := s.currentMembership(ctx, organizationID, userID)
			switch {
			case err == nil:
				member = true
			case errors.Is(err, ErrMailboxAccessInvalid):
				member = false
			default:
				return err
			}
			current[userID] = member
		}
		if !member {
			role.OwnerUserID = nil
		}
	}
	return nil
}

// validMailRole trims and checks a role's fields, all of them at once. The owner must be a current
// member of the organization.
func (s *service) validMailRole(ctx context.Context, organizationID uint, input MailRoleInput) (*domain.MailRole, error) {
	invalid := &ValidationError{}
	name := checkOrganizeName(invalid, input.Name, domain.MaxMailRoleNameLength)
	color := input.Color
	if color == "" {
		color = domain.MailLabelColorGray
	}
	if !color.Valid() {
		invalid.add("color", "must be one of: gray, red, orange, yellow, green, teal, blue, purple, pink")
	}
	description := strings.TrimSpace(input.Description)
	if utf8.RuneCountInString(description) > domain.MaxMailRoleDescriptionLength {
		invalid.add("description", "must be at most "+strconv.Itoa(domain.MaxMailRoleDescriptionLength)+" characters after trimming")
	} else if strings.ContainsFunc(description, unicode.IsControl) {
		invalid.add("description", "must not contain control characters")
	}
	if input.OwnerUserID != nil {
		if *input.OwnerUserID == 0 {
			invalid.add("owner_user_id", "must be a current member of the organization or null")
		} else if _, err := s.currentMembership(ctx, organizationID, *input.OwnerUserID); err != nil {
			if !errors.Is(err, ErrMailboxAccessInvalid) {
				return nil, err
			}
			invalid.add("owner_user_id", "must be a current member of the organization or null")
		}
	}
	if err := invalid.orNil(); err != nil {
		return nil, err
	}
	return &domain.MailRole{
		OrganizationID: organizationID, Name: name, Color: color, Description: description, OwnerUserID: input.OwnerUserID,
	}, nil
}

// changedMailRoleFields names the fields a replace changed, in audit order.
func changedMailRoleFields(before, after *domain.MailRole) []string {
	changed := []string{}
	if before.Name != after.Name {
		changed = append(changed, "name")
	}
	if before.Color != after.Color {
		changed = append(changed, "color")
	}
	if before.Description != after.Description {
		changed = append(changed, "description")
	}
	if auditID(before.OwnerUserID) != auditID(after.OwnerUserID) {
		changed = append(changed, "owner_user_id")
	}
	return changed
}

// auditMailRole enriches the request's audit entry with the role id and the names of the fields
// written; names and descriptions never reach the audit.
func (s *service) auditMailRole(ctx context.Context, action string, roleID uint, fields []string) {
	s.auditChange(ctx, auditstarter.Change{
		Action: action, Resource: "mail_roles", TargetType: "mail_role", TargetID: strconv.FormatUint(uint64(roleID), 10),
		Result: domain.AuditResultSuccess, Metadata: map[string]any{"changed_fields": slices.Clone(fields)},
	})
}

func (s *service) auditChange(ctx context.Context, change auditstarter.Change) {
	if s.recordAudit != nil {
		s.recordAudit(ctx, change)
	}
}

// auditID is an optional id as an audit value: nil or the id.
func auditID(id *uint) any {
	if id == nil {
		return nil
	}
	return *id
}
