package mailbox

import (
	"context"
	"errors"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailThreadNotes reads the internal notes on a shared mailbox's thread, oldest first; it needs view.
func (s *service) MailThreadNotes(
	ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint,
) ([]domain.MailThreadNote, error) {
	if s.notes == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if err := s.sharedThread(ctx, viewer, mailboxID, threadID, canView); err != nil {
		return nil, err
	}
	return s.notes.ListMailThreadNotes(ctx, mailboxID, threadID)
}

// AddMailThreadNote leaves an internal note on a shared mailbox's thread; it needs send.
func (s *service) AddMailThreadNote(
	ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint, body string,
) (domain.MailThreadNote, error) {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	invalid := &ValidationError{}
	if body == "" {
		invalid.add("body", "is required")
	} else {
		checkDraftField(invalid, "body", body, domain.MailThreadNoteMaxLength, true)
	}
	if err := invalid.orNil(); err != nil {
		return domain.MailThreadNote{}, err
	}
	if s.notes == nil {
		return domain.MailThreadNote{}, domain.ErrServiceUnavailable
	}
	if err := s.sharedThread(ctx, viewer, mailboxID, threadID, canSend); err != nil {
		return domain.MailThreadNote{}, err
	}
	author := viewer.UserID
	note, err := s.notes.CreateMailThreadNote(ctx, domain.MailThreadNote{
		MailboxID: mailboxID, ThreadID: threadID, AuthorUserID: &author, Body: body, CreatedAt: s.now().UTC(),
	}, domain.MailThreadNoteLimit)
	if errors.Is(err, domain.ErrMailThreadNoteLimit) {
		return domain.MailThreadNote{}, ErrMailThreadNoteLimitReached
	}
	return note, err
}

// DeleteMailThreadNote removes one of the viewer's own notes; another member's note is not found.
func (s *service) DeleteMailThreadNote(
	ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID, noteID uint,
) error {
	if s.notes == nil {
		return domain.ErrServiceUnavailable
	}
	if err := s.sharedThread(ctx, viewer, mailboxID, threadID, canView); err != nil {
		return err
	}
	deleted, err := s.notes.DeleteMailThreadNote(ctx, mailboxID, threadID, noteID, viewer.UserID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrMailThreadNoteNotFound
	}
	return nil
}

// sharedThread authorizes the viewer on a shared Zmail mailbox and checks the thread has a message
// they may see. Assignments and notes exist only there.
func (s *service) sharedThread(
	ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint, need func(domain.MailboxAccess) bool,
) error {
	if s.organize == nil {
		return domain.ErrServiceUnavailable
	}
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, need)
	if err != nil {
		return err
	}
	if mailbox.Type != domain.MailboxTypeShared || mailbox.Connected() {
		return ErrMailThreadAssignmentUnavailable
	}
	messages, err := s.organize.ThreadMessages(ctx, mailbox.ID, threadID, viewer.MembershipID)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return ErrMailMessageNotFound
	}
	return nil
}
