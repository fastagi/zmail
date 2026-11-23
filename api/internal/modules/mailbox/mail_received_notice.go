package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	mailReceivedKind = "mail.received"
	// maxMailReceivedTitle is what the notification center stores for a title.
	maxMailReceivedTitle = 160
	// maxMailReceivedBody keeps a lock-screen push to the start of the subject.
	maxMailReceivedBody   = 80
	mailReceivedNoSubject = "(no subject)"
	mailboxesConsolePath  = "/console/mailboxes"
)

// publishMailReceived tells everyone who can read the mailbox that a new message arrived: an
// in-app notification and, on subscribed devices, a browser push. The audience is exactly the
// users MailboxAccessFor grants View today: the personal owner, or the shared mailbox's members
// with at least view, each still a member of the organization. The text carries the sender and
// the masked subject only, never the body. It runs after the message is stored, and a failure is
// logged without content and never fails receipt; without the notification starter it does
// nothing.
func (s *service) publishMailReceived(ctx context.Context, mailbox *domain.Mailbox, message *domain.MailMessage) {
	if s.notifications == nil || mailbox == nil || message == nil || message.ID == 0 {
		return
	}
	recipients, err := s.mailboxReaders(ctx, mailbox)
	if err != nil {
		slog.WarnContext(ctx, "mailbox new-mail notification audience unavailable",
			slog.Uint64("mailbox_id", uint64(mailbox.ID)), slog.Uint64("message_id", uint64(message.ID)),
			slog.String("error", err.Error()))
		return
	}
	title, body := mailReceivedText(message)
	actionURL := mailboxesConsolePath +
		"?org=" + strconv.FormatUint(uint64(mailbox.OrganizationID), 10) +
		"&mailbox=" + strconv.FormatUint(uint64(mailbox.ID), 10) +
		"&open=" + strconv.FormatUint(uint64(message.ID), 10)
	key := mailReceivedKind + ":" + strconv.FormatUint(uint64(message.ID), 10)
	for _, userID := range recipients {
		_, err := s.notifications.Publish(ctx, domain.NotificationPublication{
			UserID:         userID,
			IdempotencyKey: key,
			Kind:           mailReceivedKind,
			Title:          title,
			Body:           body,
			ActionURL:      actionURL,
			Channels: []domain.NotificationChannel{
				domain.NotificationChannelInApp,
				domain.NotificationChannelWebPush,
			},
		})
		if errors.Is(err, domain.ErrServiceUnavailable) {
			// The notification starter does not run.
			return
		}
		if err != nil {
			slog.WarnContext(ctx, "mailbox new-mail notification not published",
				slog.Uint64("mailbox_id", uint64(mailbox.ID)), slog.Uint64("message_id", uint64(message.ID)),
				slog.Uint64("user_id", uint64(userID)), slog.String("error", err.Error()))
		}
	}
}

// mailboxReaders lists the users who may read the mailbox's mail, by the same rule every read path
// applies: MailboxAccessFor with each candidate's current organization membership.
func (s *service) mailboxReaders(ctx context.Context, mailbox *domain.Mailbox) ([]uint, error) {
	members := map[uint]*domain.MailboxMember{}
	var candidates []uint
	switch mailbox.Type {
	case domain.MailboxTypePersonal:
		if mailbox.OwnerUserID != nil && *mailbox.OwnerUserID != 0 {
			candidates = append(candidates, *mailbox.OwnerUserID)
		}
	case domain.MailboxTypeShared:
		rows, err := s.repo.ListMailboxMembers(ctx, mailbox.ID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row == nil || row.UserID == 0 {
				continue
			}
			members[row.UserID] = row
			candidates = append(candidates, row.UserID)
		}
	}
	if len(candidates) == 0 || s.memberships == nil {
		return nil, nil
	}
	memberships, err := s.memberships.ListMembershipsByUserIDs(ctx, mailbox.OrganizationID, candidates)
	if err != nil {
		return nil, err
	}
	readers := make([]uint, 0, len(memberships))
	seen := make(map[uint]struct{}, len(memberships))
	for _, membership := range memberships {
		if membership == nil || membership.OrganizationID != mailbox.OrganizationID {
			continue
		}
		if _, dup := seen[membership.UserID]; dup {
			continue
		}
		viewer := domain.OrganizationContext{
			OrganizationID: membership.OrganizationID,
			MembershipID:   membership.ID,
			UserID:         membership.UserID,
			Role:           membership.Role,
			Features:       membership.Features,
		}
		if domain.MailboxAccessFor(mailbox, viewer, members[membership.UserID]).View {
			seen[membership.UserID] = struct{}{}
			readers = append(readers, membership.UserID)
		}
	}
	return readers, nil
}

// mailReceivedText is the notification's title (the sender's name, or address without one) and
// body (the subject, with codes and sensitive links masked, cut to maxMailReceivedBody runes).
func mailReceivedText(message *domain.MailMessage) (string, string) {
	title := notificationLine(message.FromName)
	if title == "" {
		title = notificationLine(message.FromAddress)
	}
	if title == "" {
		title = "New mail"
	}
	body := notificationLine(maskedSubject(message))
	if body == "" {
		body = mailReceivedNoSubject
	}
	return cutWithEllipsis(title, maxMailReceivedTitle), cutWithEllipsis(body, maxMailReceivedBody)
}

// notificationLine folds control characters and runs of whitespace into single spaces.
func notificationLine(value string) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "")
	}
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " ")
}

func cutWithEllipsis(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return strings.TrimSpace(string([]rune(value)[:limit-1])) + "…"
}
