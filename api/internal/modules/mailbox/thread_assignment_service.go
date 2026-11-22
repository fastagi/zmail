package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailThreadAssignmentInput replaces a thread's assignment.
type MailThreadAssignmentInput struct {
	AssigneeUserID *uint
	Status         domain.MailThreadStatus
}

// MailThreadAssignmentView is a thread's assignment with the people it may go to.
type MailThreadAssignmentView struct {
	domain.MailThreadAssignment
	// AssignableUserIDs are the mailbox's members who may send, ascending.
	AssignableUserIDs []uint
}

// MailThreadAssignment reads who works a shared mailbox's thread; it needs view.
func (s *service) MailThreadAssignment(
	ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint,
) (MailThreadAssignmentView, error) {
	store, assignable, err := s.assignableThread(ctx, viewer, mailboxID, threadID, canView)
	if err != nil {
		return MailThreadAssignmentView{}, err
	}
	assignment, err := store.FindMailThreadAssignment(ctx, mailboxID, threadID)
	return MailThreadAssignmentView{MailThreadAssignment: assignment, AssignableUserIDs: assignable}, err
}

// SaveMailThreadAssignment assigns a shared mailbox's thread and sets its status; it needs send,
// and the assignee must be a member who may send.
func (s *service) SaveMailThreadAssignment(
	ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint, input MailThreadAssignmentInput,
) (MailThreadAssignmentView, error) {
	store, assignable, err := s.assignableThread(ctx, viewer, mailboxID, threadID, canSend)
	if err != nil {
		return MailThreadAssignmentView{}, err
	}
	invalid := &ValidationError{}
	if input.Status != domain.MailThreadOpen && input.Status != domain.MailThreadClosed {
		invalid.add("status", "must be open or closed")
	}
	current, err := store.FindMailThreadAssignment(ctx, mailboxID, threadID)
	if err != nil {
		return MailThreadAssignmentView{}, err
	}
	if input.AssigneeUserID != nil && !containsUint(assignable, *input.AssigneeUserID) &&
		!sameUserID(current.AssigneeUserID, input.AssigneeUserID) {
		invalid.add("assignee_user_id", "must be a member of the mailbox who may send")
	}
	if err = invalid.orNil(); err != nil {
		return MailThreadAssignmentView{}, err
	}
	by := viewer.UserID
	saved, err := store.SaveMailThreadAssignment(ctx, domain.MailThreadAssignment{
		MailboxID: mailboxID, ThreadID: threadID, AssigneeUserID: input.AssigneeUserID, Status: input.Status, UpdatedBy: &by,
	}, s.now())
	return MailThreadAssignmentView{MailThreadAssignment: saved, AssignableUserIDs: assignable}, err
}

// assignableThread authorizes the viewer on a shared Zmail mailbox, checks the thread has a message
// the viewer may see, and lists who it may be assigned to.
func (s *service) assignableThread(
	ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint, need func(domain.MailboxAccess) bool,
) (domain.MailThreadAssignmentRepository, []uint, error) {
	if s.assignments == nil || s.organize == nil {
		return nil, nil, domain.ErrServiceUnavailable
	}
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, need)
	if err != nil {
		return nil, nil, err
	}
	if mailbox.Type != domain.MailboxTypeShared || mailbox.Connected() {
		return nil, nil, ErrMailThreadAssignmentUnavailable
	}
	messages, err := s.organize.ThreadMessages(ctx, mailbox.ID, threadID, viewer.MembershipID)
	if err != nil {
		return nil, nil, err
	}
	if len(messages) == 0 {
		return nil, nil, ErrMailMessageNotFound
	}
	members, err := s.repo.ListMailboxMembers(ctx, mailbox.ID)
	if err != nil {
		return nil, nil, err
	}
	assignable := make([]uint, 0, len(members))
	for _, member := range members {
		if member.Permission != domain.MailboxPermissionView {
			assignable = append(assignable, member.UserID)
		}
	}
	return s.assignments, assignable, nil
}

func containsUint(values []uint, want uint) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// sameUserID reports whether a kept assignee is unchanged, so a member who lost send access can
// stay on a thread while its status moves.
func sameUserID(a, b *uint) bool {
	return a != nil && b != nil && *a == *b
}
