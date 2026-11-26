package mailbox

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ListMessages lists a mailbox's messages for the viewer. The contract's order holds: a mailbox
// the viewer cannot see, then missing view access, then a folder or label that is not the
// mailbox's, and only then an invalid filter.
func (s *service) ListMessages(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID uint,
	filter domain.MailMessageFilter,
) ([]*domain.MailMessage, int64, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canView); err != nil {
		return nil, 0, err
	}
	if err := s.checkListReferences(ctx, mailboxID, filter); err != nil {
		return nil, 0, err
	}
	if err := validateMessageFilter(filter); err != nil {
		return nil, 0, err
	}
	if filter.Placement == domain.MailPlacementDefault || filter.Snoozed != nil {
		s.wakeSnoozedOpportunistically(ctx)
	}
	if filter.Now.IsZero() {
		filter.Now = s.now().UTC()
	}
	var (
		messages []*domain.MailMessage
		total    int64
		err      error
	)
	if filter.GroupThreads {
		messages, total, err = s.listThreads(ctx, viewer, mailboxID, filter)
	} else {
		messages, total, err = s.repo.ListMessages(ctx, mailboxID, viewer.MembershipID, filter)
	}
	if err != nil {
		return nil, 0, err
	}
	if err := s.fillViewerState(ctx, viewer, messages...); err != nil {
		return nil, 0, err
	}
	return messages, total, nil
}

func (s *service) listThreads(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID uint,
	filter domain.MailMessageFilter,
) ([]*domain.MailMessage, int64, error) {
	if s.organize == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	threads, total, err := s.organize.ListThreads(ctx, mailboxID, viewer.MembershipID, filter)
	if err != nil {
		return nil, 0, err
	}
	messages := make([]*domain.MailMessage, 0, len(threads))
	for i := range threads {
		message := threads[i].Latest
		info := threads[i].MailThreadInfo
		message.Thread = &info
		messages = append(messages, message)
	}
	return messages, total, nil
}

// checkListReferences answers ErrMailFolderNotFound or ErrMailLabelNotFound for a folder or label
// that is not the mailbox's.
func (s *service) checkListReferences(ctx context.Context, mailboxID uint, filter domain.MailMessageFilter) error {
	if filter.FolderID == 0 && filter.LabelID == 0 {
		return nil
	}
	if s.organize == nil {
		return domain.ErrServiceUnavailable
	}
	if filter.FolderID != 0 {
		folders, err := s.organize.ListFolders(ctx, mailboxID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(folders, func(folder *domain.MailFolder) bool { return folder.ID == filter.FolderID }) {
			return ErrMailFolderNotFound
		}
	}
	if filter.LabelID != 0 {
		labels, err := s.organize.ListLabels(ctx, mailboxID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(labels, func(label *domain.MailLabel) bool { return label.ID == filter.LabelID }) {
			return ErrMailLabelNotFound
		}
	}
	return nil
}

// validateMessageFilter applies the contract's listing rules.
func validateMessageFilter(filter domain.MailMessageFilter) error {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, reason)
	}
	switch {
	case len(filter.Malformed) > 0:
		return invalid("malformed " + strings.Join(filter.Malformed, ", "))
	case filter.Direction != "" && !filter.Direction.Valid():
		return invalid("direction")
	case filter.Placement != "" && !filter.Placement.Valid():
		return invalid("placement")
	case filter.Placement == domain.MailPlacementFolder && filter.FolderID == 0:
		return invalid("placement folder needs folder_id")
	case filter.Placement != domain.MailPlacementFolder && filter.FolderID != 0:
		return invalid("folder_id is only allowed with placement folder")
	case filter.Query != strings.TrimSpace(filter.Query) || !utf8.ValidString(filter.Query) ||
		utf8.RuneCountInString(filter.Query) > domain.MaxMailSearchLength ||
		strings.IndexFunc(filter.Query, unicode.IsControl) >= 0:
		return invalid("q")
	case filter.Query != "" && filter.Placement == domain.MailPlacementTrash:
		return invalid("the trash cannot be searched")
	}
	if !filter.GroupThreads {
		return nil
	}
	switch {
	case filter.Placement != domain.MailPlacementDefault && filter.Placement != domain.MailPlacementArchive &&
		filter.Placement != domain.MailPlacementFolder:
		return invalid("group needs placement default, archive, or folder")
	case filter.Placement == domain.MailPlacementDefault && filter.Direction == "":
		return invalid("group on default needs direction")
	case filter.LabelID != 0 || filter.Starred != nil || filter.Unread != nil || filter.HasAttachments != nil || filter.Snoozed != nil || filter.Query != "":
		return invalid("group lists placement views only")
	}
	return nil
}

func (s *service) GetThread(ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint) ([]*domain.MailMessage, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canView); err != nil {
		return nil, err
	}
	if s.organize == nil {
		return nil, domain.ErrServiceUnavailable
	}
	messages, err := s.organize.ThreadMessages(ctx, mailboxID, threadID, viewer.MembershipID)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, ErrMailMessageNotFound
	}
	hideBlindCopies(viewer.MembershipID, messages...)
	if err := s.fillLabels(ctx, messages...); err != nil {
		return nil, err
	}
	for _, message := range messages {
		if _, err := s.withEvents(ctx, message); err != nil {
			return nil, err
		}
	}
	return messages, nil
}

func (s *service) MessageCounts(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (domain.MailMessageCounts, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canView); err != nil {
		return domain.MailMessageCounts{}, err
	}
	if s.organize == nil {
		return domain.MailMessageCounts{}, domain.ErrServiceUnavailable
	}
	return s.organize.MessageCounts(ctx, mailboxID, viewer.MembershipID)
}
