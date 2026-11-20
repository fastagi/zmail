package mailbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/fastagi/zmail/api/internal/domain"
)

// membershipDirectory finds a user's current membership of an organization. The organization
// starter owns memberships; the mailbox starter only reads them through this seam.
type membershipDirectory interface {
	FindForUser(ctx context.Context, organizationID, userID uint) (*domain.OrganizationMembership, error)
}

// CreateMailboxInput is a request to create a mailbox.
type CreateMailboxInput struct {
	LocalPart   string
	DisplayName string
	// Domain is the address domain; empty means the shared domain.
	Domain string
	Type   domain.MailboxType
	// OwnerUserID is the owner of a personal mailbox; zero for a shared one.
	OwnerUserID uint
	// Members are the first members of a shared mailbox, 1 to domain.MaxMailboxMembers.
	Members []MailboxMemberInput
}

// MailboxMemberInput names one member and its permission.
type MailboxMemberInput struct {
	UserID     uint
	Permission domain.MailboxPermission
}

// Capability checks passed to authorizeMailbox.
func canView(access domain.MailboxAccess) bool          { return access.View }
func canSend(access domain.MailboxAccess) bool          { return access.Send }
func canAdminister(access domain.MailboxAccess) bool    { return access.Administer }
func canManageMembers(access domain.MailboxAccess) bool { return access.ManageMembers }
func canSeeHistory(access domain.MailboxAccess) bool    { return access.SeeHistory }

// accessFor derives the viewer's access to a mailbox loaded in the viewer's organization.
func (s *service) accessFor(ctx context.Context, viewer domain.OrganizationContext, mailbox *domain.Mailbox) (domain.MailboxAccess, error) {
	var member *domain.MailboxMember
	if mailbox.Type == domain.MailboxTypeShared {
		var err error
		if member, err = s.repo.FindMailboxMember(ctx, mailbox.ID, viewer.MembershipID); err != nil {
			return domain.MailboxAccess{}, err
		}
	}
	return domain.MailboxAccessFor(mailbox, viewer, member), nil
}

// authorizeMailbox is the one chokepoint every mailbox path goes through before touching mail.
// A viewer without any access gets ErrMailboxNotFound, exactly like a mailbox that does not
// exist; a viewer with some access but not the needed one gets domain.ErrPermissionDenied. A
// nil need only requires some access. The returned mailbox carries the viewer's access.
func (s *service) authorizeMailbox(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID uint,
	need func(domain.MailboxAccess) bool,
) (*domain.Mailbox, error) {
	mailbox, err := s.repo.FindMailbox(ctx, viewer.OrganizationID, mailboxID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, viewer, mailbox, need, ErrMailboxNotFound); err != nil {
		return nil, err
	}
	return mailbox, nil
}

// checkAccess fills the viewer's access on mailbox and applies need; hidden is the error for a
// viewer without any access.
func (s *service) checkAccess(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailbox *domain.Mailbox,
	need func(domain.MailboxAccess) bool,
	hidden error,
) error {
	access, err := s.accessFor(ctx, viewer, mailbox)
	if err != nil {
		return err
	}
	mailbox.Access = access
	if !access.Any() {
		return hidden
	}
	if need != nil && !need(access) {
		return domain.ErrPermissionDenied
	}
	return nil
}

// visibleMailboxes lists the organization's mailboxes the viewer has any access to, each with the
// viewer's access filled in.
func (s *service) visibleMailboxes(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.Mailbox, error) {
	mailboxes, err := s.repo.ListMailboxes(ctx, viewer.OrganizationID, viewer.MembershipID)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListMembershipMailboxMembers(ctx, viewer.OrganizationID, viewer.MembershipID)
	if err != nil {
		return nil, err
	}
	members := make(map[uint]*domain.MailboxMember, len(rows))
	for _, row := range rows {
		members[row.MailboxID] = row
	}
	visible := make([]*domain.Mailbox, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		mailbox.Access = domain.MailboxAccessFor(mailbox, viewer, members[mailbox.ID])
		if mailbox.Access.Any() {
			visible = append(visible, mailbox)
		}
	}
	return visible, nil
}

// usableMailboxes lists the mailboxes the viewer can read or send from, plus, for owners and admins,
// personal mailboxes awaiting handover, each with the viewer's access filled in. It reads only the
// viewer's own mailboxes, so it stays cheap however many mailboxes the organization owns.
func (s *service) usableMailboxes(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.Mailbox, error) {
	manager := viewer.Role.CanManageOrganization()
	mailboxes, err := s.repo.ListUsableMailboxes(ctx, viewer.OrganizationID, viewer.MembershipID, viewer.UserID, manager)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListMembershipMailboxMembers(ctx, viewer.OrganizationID, viewer.MembershipID)
	if err != nil {
		return nil, err
	}
	members := make(map[uint]*domain.MailboxMember, len(rows))
	for _, row := range rows {
		members[row.MailboxID] = row
	}
	usable := make([]*domain.Mailbox, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		mailbox.Access = domain.MailboxAccessFor(mailbox, viewer, members[mailbox.ID])
		awaiting := manager && mailbox.Type == domain.MailboxTypePersonal && mailbox.OwnerLeftAt != nil
		if mailbox.Access.View || mailbox.Access.Send || (awaiting && mailbox.Access.Any()) {
			usable = append(usable, mailbox)
		}
	}
	return usable, nil
}

// readableMailboxIDs lists the mailboxes whose mail the viewer may read.
func (s *service) readableMailboxIDs(ctx context.Context, viewer domain.OrganizationContext) ([]uint, error) {
	mailboxes, err := s.visibleMailboxes(ctx, viewer)
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		if mailbox.Access.View {
			ids = append(ids, mailbox.ID)
		}
	}
	return ids, nil
}

// resolvedMember is a validated member with its membership.
type resolvedMember struct {
	userID       uint
	membershipID uint
	permission   domain.MailboxPermission
}

// validateMailboxAccess checks the type, owner, and members of a new mailbox without loading
// anything.
func validateMailboxAccess(input CreateMailboxInput) error {
	switch input.Type {
	case domain.MailboxTypePersonal:
		if input.OwnerUserID == 0 {
			return fmt.Errorf("%w: a personal mailbox needs owner_user_id", ErrMailboxAccessInvalid)
		}
		if len(input.Members) > 0 {
			return fmt.Errorf("%w: a personal mailbox has no members", ErrMailboxAccessInvalid)
		}
	case domain.MailboxTypeShared:
		if input.OwnerUserID != 0 {
			return fmt.Errorf("%w: a shared mailbox has no owner", ErrMailboxAccessInvalid)
		}
		if len(input.Members) == 0 || len(input.Members) > domain.MaxMailboxMembers {
			return fmt.Errorf("%w: a shared mailbox needs 1 to %d members", ErrMailboxAccessInvalid, domain.MaxMailboxMembers)
		}
		seen := make(map[uint]struct{}, len(input.Members))
		for _, member := range input.Members {
			if member.UserID == 0 || !member.Permission.Valid() {
				return fmt.Errorf("%w: each member needs user_id and a permission of view, send, or manage", ErrMailboxAccessInvalid)
			}
			if _, duplicate := seen[member.UserID]; duplicate {
				return fmt.Errorf("%w: user %d is listed twice", ErrMailboxAccessInvalid, member.UserID)
			}
			seen[member.UserID] = struct{}{}
		}
	default:
		return fmt.Errorf("%w: type must be personal or shared", ErrMailboxAccessInvalid)
	}
	return nil
}

// currentMembership finds the membership of a user in the viewer's organization. A user who is
// not a member is invalid input, not a missing resource.
func (s *service) currentMembership(ctx context.Context, organizationID, userID uint) (*domain.OrganizationMembership, error) {
	if s.memberships == nil {
		return nil, domain.ErrServiceUnavailable
	}
	membership, err := s.memberships.FindForUser(ctx, organizationID, userID)
	if errors.Is(err, domain.ErrOrganizationNotFound) || (err == nil && membership == nil) {
		return nil, fmt.Errorf("%w: user %d is not a member of the organization", ErrMailboxAccessInvalid, userID)
	}
	if err != nil {
		return nil, err
	}
	return membership, nil
}

func (s *service) resolveMembers(ctx context.Context, organizationID uint, members []MailboxMemberInput) ([]resolvedMember, error) {
	resolved := make([]resolvedMember, 0, len(members))
	for _, member := range members {
		membership, err := s.currentMembership(ctx, organizationID, member.UserID)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, resolvedMember{userID: member.UserID, membershipID: membership.ID, permission: member.Permission})
	}
	return resolved, nil
}

// recordAccess writes one access event.
func (s *service) recordAccess(
	ctx context.Context,
	mailbox *domain.Mailbox,
	eventType domain.MailboxAccessEventType,
	actorUserID, subjectUserID uint,
	detail map[string]any,
) error {
	event := &domain.MailboxAccessEvent{
		OrganizationID: mailbox.OrganizationID,
		MailboxID:      mailbox.ID,
		Type:           eventType,
		ActorUserID:    optionalID(actorUserID),
		SubjectUserID:  optionalID(subjectUserID),
		Detail:         detail,
		CreatedAt:      s.now().UTC(),
	}
	return s.repo.AddMailboxAccessEvent(ctx, event)
}

func optionalID(id uint) *uint {
	if id == 0 {
		return nil
	}
	return &id
}

// setMember stores a member row and its member_set event. The event keeps the membership id so
// a later departure can be traced to the mailboxes it held. An unchanged permission writes nothing.
func (s *service) setMember(ctx context.Context, viewer domain.OrganizationContext, mailbox *domain.Mailbox, member resolvedMember) (*domain.MailboxMember, error) {
	existing, err := s.repo.FindMailboxMember(ctx, mailbox.ID, member.membershipID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Permission == member.permission {
		return existing, nil
	}
	row := &domain.MailboxMember{
		MailboxID:    mailbox.ID,
		MembershipID: member.membershipID,
		UserID:       member.userID,
		Permission:   member.permission,
		GrantedBy:    viewer.UserID,
	}
	if _, err = s.repo.UpsertMailboxMember(ctx, row, domain.MaxMailboxMembers); err != nil {
		return nil, err
	}
	err = s.recordAccess(ctx, mailbox, domain.MailboxAccessMemberSet, viewer.UserID, member.userID, map[string]any{
		"membership_id": member.membershipID,
		"permission":    string(member.permission),
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// CreateMailbox validates the address and the access, then creates the routing rule (for an
// organization domain), and stores the mailbox, its members, and its access events in one
// transaction. Only owners and admins may.
func (s *service) CreateMailbox(ctx context.Context, viewer domain.OrganizationContext, input CreateMailboxInput) (*domain.Mailbox, error) {
	normalized, err := normalizeLocalPart(input.LocalPart)
	if err != nil {
		return nil, err
	}
	displayName, err := normalizeDisplayName(input.DisplayName)
	if err != nil {
		return nil, err
	}
	if err = validateMailboxAccess(input); err != nil {
		return nil, err
	}
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	var owner *domain.OrganizationMembership
	var members []resolvedMember
	if input.Type == domain.MailboxTypePersonal {
		if owner, err = s.currentMembership(ctx, viewer.OrganizationID, input.OwnerUserID); err != nil {
			return nil, err
		}
	} else if members, err = s.resolveMembers(ctx, viewer.OrganizationID, input.Members); err != nil {
		return nil, err
	}

	addressDomain, connected, err := s.mailboxDomainFor(ctx, viewer.OrganizationID, input.Domain)
	if err != nil {
		return nil, err
	}
	mailbox := &domain.Mailbox{
		OrganizationID: viewer.OrganizationID,
		CreatedBy:      viewer.UserID,
		Address:        normalized + "@" + addressDomain,
		DisplayName:    displayName,
		Type:           input.Type,
	}
	if owner != nil {
		ownerID := owner.UserID
		mailbox.OwnerUserID = &ownerID
	}
	if connected != nil {
		// Claim the address before routing mail to it, so a taken address never gets a rule.
		if _, findErr := s.repo.FindMailboxByAddress(ctx, mailbox.Address); findErr == nil {
			return nil, ErrMailboxAddressTaken
		} else if !errors.Is(findErr, ErrMailboxNotFound) {
			return nil, findErr
		}
		// A send-only domain receives elsewhere, so Email Routing is never used for its addresses.
		if receives(connected) {
			ruleID, ruleErr := s.domainProvider.CreateRoutingRule(ctx, connected.ZoneID, mailbox.Address, s.settings.EmailWorker)
			if ruleErr != nil {
				return nil, providerError(ruleErr)
			}
			mailbox.RoutingRuleID = ruleID
		}
	}
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		if createErr := s.repo.CreateMailbox(tx, mailbox, s.MaxMailboxes()); createErr != nil {
			return createErr
		}
		detail := map[string]any{"type": string(mailbox.Type)}
		var subject uint
		if owner != nil {
			subject = owner.UserID
		} else {
			detail["members"] = len(members)
		}
		if recordErr := s.recordAccess(tx, mailbox, domain.MailboxAccessCreated, viewer.UserID, subject, detail); recordErr != nil {
			return recordErr
		}
		for _, member := range members {
			if _, setErr := s.setMember(tx, viewer, mailbox, member); setErr != nil {
				return setErr
			}
		}
		return nil
	})
	if err != nil {
		if connected != nil {
			s.removeRoutingRule(ctx, mailbox)
		}
		return nil, err
	}
	if mailbox.Access, err = s.accessFor(ctx, viewer, mailbox); err != nil {
		return nil, err
	}
	return mailbox, nil
}

// memberMailbox loads a mailbox for a member change: hidden without any access, a conflict on a
// personal mailbox (checked before the capability, so admins and owners see it), and forbidden
// without ManageMembers.
func (s *service) memberMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (*domain.Mailbox, error) {
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, nil)
	if err != nil {
		return nil, err
	}
	if mailbox.Type != domain.MailboxTypeShared {
		return nil, fmt.Errorf("%w: a personal mailbox has no members", ErrMailboxAccessConflict)
	}
	if !canManageMembers(mailbox.Access) {
		return nil, domain.ErrPermissionDenied
	}
	return mailbox, nil
}

// SetMailboxWebhooks turns mail webhooks of a shared mailbox on or off with the same checks as a
// member change. A change is recorded; setting the current value writes nothing.
func (s *service) SetMailboxWebhooks(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, enabled bool) (*domain.Mailbox, error) {
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, nil)
	if err != nil {
		return nil, err
	}
	if mailbox.Type != domain.MailboxTypeShared {
		return nil, fmt.Errorf("%w: a personal mailbox never publishes webhooks", ErrMailboxAccessConflict)
	}
	if !canManageMembers(mailbox.Access) {
		return nil, domain.ErrPermissionDenied
	}
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		changed, setErr := s.repo.SetMailboxWebhooks(tx, mailbox.ID, enabled)
		if setErr != nil || !changed {
			return setErr
		}
		return s.recordAccess(tx, mailbox, domain.MailboxAccessWebhooksChanged, viewer.UserID, 0, map[string]any{"enabled": enabled})
	})
	if err != nil {
		return nil, err
	}
	return s.authorizeMailbox(ctx, viewer, mailbox.ID, nil)
}

func (s *service) ListMailboxMembers(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) ([]*domain.MailboxMember, error) {
	if _, err := s.memberMailbox(ctx, viewer, mailboxID); err != nil {
		return nil, err
	}
	return s.repo.ListMailboxMembers(ctx, mailboxID)
}

// SetMailboxMember adds a member or changes its permission. An admin may add themselves; the
// access history records it like any other change.
func (s *service) SetMailboxMember(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID, userID uint,
	permission domain.MailboxPermission,
) (*domain.MailboxMember, error) {
	mailbox, err := s.memberMailbox(ctx, viewer, mailboxID)
	if err != nil {
		return nil, err
	}
	if userID == 0 || !permission.Valid() {
		return nil, fmt.Errorf("%w: permission must be view, send, or manage", ErrMailboxAccessInvalid)
	}
	membership, err := s.currentMembership(ctx, viewer.OrganizationID, userID)
	if err != nil {
		return nil, err
	}
	var member *domain.MailboxMember
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		var setErr error
		member, setErr = s.setMember(tx, viewer, mailbox, resolvedMember{userID: userID, membershipID: membership.ID, permission: permission})
		return setErr
	})
	if err != nil {
		return nil, err
	}
	return member, nil
}

// RemoveMailboxMember deletes a member row and the member's read rows of the mailbox in one
// transaction, and records it. The removed member's next request finds no access, and access
// granted again starts with everything unread.
func (s *service) RemoveMailboxMember(ctx context.Context, viewer domain.OrganizationContext, mailboxID, userID uint) error {
	mailbox, err := s.memberMailbox(ctx, viewer, mailboxID)
	if err != nil {
		return err
	}
	return s.repo.InTransaction(ctx, func(tx context.Context) error {
		removed, err := s.repo.DeleteMailboxMember(tx, mailbox.ID, userID)
		if err != nil {
			return err
		}
		if err := s.repo.DeleteMailboxReads(tx, mailbox.ID, removed.MembershipID); err != nil {
			return err
		}
		return s.recordAccess(tx, mailbox, domain.MailboxAccessMemberRemoved, viewer.UserID, removed.UserID, map[string]any{
			"membership_id": removed.MembershipID,
			"permission":    string(removed.Permission),
		})
	})
}

// ListMailboxAccessEvents returns the access history, newest first.
func (s *service) ListMailboxAccessEvents(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID uint,
	page, size int,
) ([]*domain.MailboxAccessEvent, int64, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canSeeHistory); err != nil {
		return nil, 0, err
	}
	return s.repo.ListMailboxAccessEvents(ctx, mailboxID, page, size)
}
