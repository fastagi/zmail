package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
)

func (s *service) pendingMemberStore() domain.MailboxPendingMemberRepository {
	if store, ok := s.repo.(domain.MailboxPendingMemberRepository); ok {
		return store
	}
	return nil
}

// SetPendingMailboxMember queues a shared-mailbox member grant for an invited email so the
// invitee receives mailbox access automatically upon joining the organization. Only organization
// owners and admins may create or update pending grants.
func (s *service) SetPendingMailboxMember(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID uint,
	email string,
	permission domain.MailboxPermission,
) (*domain.MailboxPendingMember, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	mailbox, err := s.memberMailbox(ctx, viewer, mailboxID)
	if err != nil {
		return nil, err
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	if !validRosterEmail(normalized) {
		return nil, fmt.Errorf("%w: email must be a valid email address", ErrMailboxAccessInvalid)
	}
	if !permission.Valid() {
		return nil, fmt.Errorf("%w: permission must be view, send, or manage", ErrMailboxAccessInvalid)
	}
	store := s.pendingMemberStore()
	if store == nil {
		return nil, domain.ErrServiceUnavailable
	}
	pending := &domain.MailboxPendingMember{
		OrganizationID: mailbox.OrganizationID,
		MailboxID:      mailbox.ID,
		Email:          normalized,
		Permission:     permission,
		GrantedBy:      viewer.UserID,
	}
	if err := store.UpsertPendingMailboxMember(ctx, pending); err != nil {
		return nil, err
	}
	return pending, nil
}

// claimPendingMembers converts any pending shared-mailbox grants queued for email into live
// mailbox_members rows inside the MemberJoined transaction.
func (s *service) claimPendingMembers(ctx context.Context, organizationID, userID, membershipID uint, email string) error {
	store := s.pendingMemberStore()
	if store == nil || email == "" {
		return nil
	}
	claimed, err := store.ClaimPendingMailboxMembers(ctx, organizationID, email)
	if err != nil || len(claimed) == 0 {
		return err
	}
	if membershipID == 0 && s.memberships != nil {
		if membership, findErr := s.memberships.FindForUser(ctx, organizationID, userID); findErr == nil && membership != nil {
			membershipID = membership.ID
		}
	}
	if membershipID == 0 {
		return nil
	}
	for _, pending := range claimed {
		mailbox, findErr := s.repo.FindMailbox(ctx, organizationID, pending.MailboxID)
		if errors.Is(findErr, ErrMailboxNotFound) {
			continue
		}
		if findErr != nil {
			return findErr
		}
		if mailbox.Type != domain.MailboxTypeShared {
			continue
		}
		existing, memberErr := s.repo.FindMailboxMember(ctx, mailbox.ID, membershipID)
		if memberErr != nil {
			return memberErr
		}
		if existing != nil {
			continue
		}
		member := &domain.MailboxMember{
			MailboxID:    mailbox.ID,
			MembershipID: membershipID,
			UserID:       userID,
			Permission:   pending.Permission,
			GrantedBy:    pending.GrantedBy,
		}
		if _, upsertErr := s.repo.UpsertMailboxMember(ctx, member, domain.MaxMailboxMembers); upsertErr != nil {
			if errors.Is(upsertErr, ErrMailboxMemberLimit) || errors.Is(upsertErr, ErrMailboxNotFound) {
				continue
			}
			return upsertErr
		}
		if recordErr := s.recordAccess(ctx, mailbox, domain.MailboxAccessMemberSet, pending.GrantedBy, userID, map[string]any{
			"membership_id": membershipID,
			"permission":    string(pending.Permission),
			"via":           "invitation",
		}); recordErr != nil {
			return recordErr
		}
	}
	return nil
}
