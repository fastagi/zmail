package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// reasonLeftOrganization is the detail.reason of access events Zmail writes for a departure.
const reasonLeftOrganization = "left_organization"

// SealedDeletion counts what deleting a mailbox's sealed history removed.
type SealedDeletion struct {
	Messages   int64
	Broadcasts int64
}

// HandOverMailbox turns a personal mailbox, awaiting handover or not, into a shared one whose
// successor is a send member. Messages and broadcasts created before the seal boundary become
// invisible to everyone: the time the owner left when they are gone and that time is known,
// otherwise now. Only owners and admins may; of two concurrent handovers exactly one succeeds.
func (s *service) HandOverMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID, successorUserID uint) (*domain.Mailbox, error) {
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, canAdminister)
	if err != nil {
		return nil, err
	}
	if mailbox.Type != domain.MailboxTypePersonal {
		return nil, fmt.Errorf("%w: only a personal mailbox is handed over", ErrMailboxAccessConflict)
	}
	successor, err := s.currentMembership(ctx, viewer.OrganizationID, successorUserID)
	if err != nil {
		return nil, err
	}
	var formerUserID uint
	if mailbox.OwnerUserID != nil {
		formerUserID = *mailbox.OwnerUserID
	}

	var boundary time.Time
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		// The former owner is read inside the transaction: an owner who rejoined before the
		// handover is sealed at the handover, never at their earlier departure.
		var boundaryErr error
		boundary, boundaryErr = s.sealBoundary(tx, viewer.OrganizationID, formerUserID, mailbox.OwnerLeftAt)
		if boundaryErr != nil {
			return boundaryErr
		}
		converted, handErr := s.repo.HandOverMailbox(tx, mailbox.ID, boundary)
		if handErr != nil {
			return handErr
		}
		if !converted {
			return fmt.Errorf("%w: the mailbox was already handed over", ErrMailboxAccessConflict)
		}
		// Only the former owner could read a personal mailbox, under any of their memberships.
		if readErr := s.repo.DeleteMailboxReads(tx, mailbox.ID, 0); readErr != nil {
			return readErr
		}
		member := &domain.MailboxMember{
			MailboxID: mailbox.ID, MembershipID: successor.ID, UserID: successorUserID,
			Permission: domain.MailboxPermissionSend, GrantedBy: viewer.UserID,
		}
		if _, upsertErr := s.repo.UpsertMailboxMember(tx, member, domain.MaxMailboxMembers); upsertErr != nil {
			return upsertErr
		}
		detail := map[string]any{
			"sealed_before": boundary.Format(time.RFC3339Nano),
			// membership_id and permission let a later departure of the successor be traced.
			"membership_id": successor.ID,
			"permission":    string(domain.MailboxPermissionSend),
		}
		if formerUserID != 0 {
			detail["former_owner_user_id"] = formerUserID
		}
		return s.recordAccess(tx, mailbox, domain.MailboxAccessHandedOver, viewer.UserID, successorUserID, detail)
	})
	if err != nil {
		return nil, err
	}
	return s.authorizeMailbox(ctx, viewer, mailbox.ID, nil)
}

// sealBoundary is the time the former owner left when they are gone and that time is known,
// otherwise now.
func (s *service) sealBoundary(ctx context.Context, organizationID, formerUserID uint, ownerLeftAt *time.Time) (time.Time, error) {
	now := s.now().UTC()
	if formerUserID == 0 || ownerLeftAt == nil {
		return now, nil
	}
	former, err := s.memberships.FindForUser(ctx, organizationID, formerUserID)
	switch {
	case err == nil && former != nil:
		return now, nil
	case err != nil && !errors.Is(err, domain.ErrOrganizationNotFound):
		return time.Time{}, err
	}
	return ownerLeftAt.UTC(), nil
}

// DeleteSealedHistory deletes for good the messages and broadcasts created before the mailbox's
// seal boundary, canceling sealed broadcasts still running first. The address confirms it.
func (s *service) DeleteSealedHistory(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, confirmAddress string) (*SealedDeletion, error) {
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, canAdminister)
	if err != nil {
		return nil, err
	}
	if mailbox.SealedBefore == nil {
		return nil, fmt.Errorf("%w: the mailbox has no sealed history", ErrMailboxAccessConflict)
	}
	if !strings.EqualFold(strings.TrimSpace(confirmAddress), mailbox.Address) {
		return nil, fmt.Errorf("%w: confirm_address must be the mailbox address", ErrMailboxAccessInvalid)
	}
	deleted := &SealedDeletion{}
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		var deleteErr error
		deleted.Messages, deleted.Broadcasts, deleteErr = s.repo.DeleteSealedHistory(tx, mailbox.ID, *mailbox.SealedBefore)
		if deleteErr != nil {
			return deleteErr
		}
		return s.recordAccess(tx, mailbox, domain.MailboxAccessSealedDeleted, viewer.UserID, 0, map[string]any{
			"deleted_messages":   deleted.Messages,
			"deleted_broadcasts": deleted.Broadcasts,
		})
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

// MemberRemoved handles a user leaving the organization. The database already removed their
// member rows and read rows with the membership; this records the departure: each personal
// mailbox they owned awaits handover, and each shared mailbox they were a member of, found from
// its own history, gets member_removed. Repeating it records nothing new on shared mailboxes.
func (s *service) MemberRemoved(ctx context.Context, event domain.OrganizationMemberRemovedEvent) error {
	at := event.OccurredAt().UTC()
	if at.IsZero() {
		at = s.now().UTC()
	}
	return s.repo.InTransaction(ctx, func(tx context.Context) error {
		owned, err := s.repo.MarkOwnerLeft(tx, event.OrganizationID, event.UserID, at)
		if err != nil {
			return err
		}
		for _, mailbox := range owned {
			if recordErr := s.recordAccess(tx, mailbox, domain.MailboxAccessOwnerLeft, 0, event.UserID,
				map[string]any{"reason": reasonLeftOrganization}); recordErr != nil {
				return recordErr
			}
		}
		granted, err := s.repo.MailboxesGrantedTo(tx, event.OrganizationID, event.MembershipID)
		if err != nil {
			return err
		}
		for _, mailboxID := range granted {
			mailbox := &domain.Mailbox{ID: mailboxID, OrganizationID: event.OrganizationID}
			if recordErr := s.recordAccess(tx, mailbox, domain.MailboxAccessMemberRemoved, 0, event.UserID, map[string]any{
				"reason":        reasonLeftOrganization,
				"membership_id": event.MembershipID,
			}); recordErr != nil {
				return recordErr
			}
		}
		return nil
	})
}

// MemberJoined handles a user joining the organization: a returning owner's personal mailboxes
// that still await handover are theirs again. Access never depended on the departure mark, so a
// missed event only leaves a stale label.
func (s *service) MemberJoined(ctx context.Context, event domain.OrganizationMemberJoinedEvent) error {
	return s.repo.InTransaction(ctx, func(tx context.Context) error {
		returned, err := s.repo.ClearOwnerLeft(tx, event.OrganizationID, event.UserID)
		if err != nil {
			return err
		}
		for _, mailbox := range returned {
			if err := s.recordAccess(tx, mailbox, domain.MailboxAccessOwnerReturned, 0, event.UserID, map[string]any{}); err != nil {
				return err
			}
		}
		return nil
	})
}
