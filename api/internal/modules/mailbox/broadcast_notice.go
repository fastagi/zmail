package mailbox

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/email"
)

const (
	noticeKindSent   = "mail.broadcast_sent"
	noticeKindFailed = "mail.broadcast_failed"
	noticeKindPaused = "mail.broadcast_paused"

	noticeStateSent   = "sent"
	noticeStateFailed = "failed"

	// maxNoticesPerRound bounds the notices one worker round sends.
	maxNoticesPerRound = 50
	// noticeLookback leaves out broadcasts that finished longer ago, so the worker never scans
	// the whole history.
	noticeLookback = 7 * 24 * time.Hour
	// maxNoticeNameLength keeps a title within what the notification center stores.
	maxNoticeNameLength  = 80
	broadcastConsolePath = "/console/mail-broadcasts"
)

// BroadcastNotice tells the person who started a broadcast how it ended or why it stopped.
type BroadcastNotice struct {
	UserID uint
	// Email is the person's account address.
	Email string
	// Key is stable for one notice, so a retry never delivers it twice.
	Key   string
	Kind  string
	Title string
	Body  string
	// Path is the broadcast in the console; URL is the same page on the public Web origin.
	Path string
	URL  string
}

// broadcastNoticeSender delivers a broadcast notice. An error means it may be tried again later.
type broadcastNoticeSender interface {
	SendBroadcastNotice(ctx context.Context, notice BroadcastNotice) error
}

// notifyBroadcastOutcomes tells the starters of broadcasts that finished, failed, or paused on their
// own since the last round. A notice that could not be delivered is tried again next round; a
// failure never stops sending.
func (s *service) notifyBroadcastOutcomes(ctx context.Context) error {
	if s.notices == nil || s.broadcasts == nil {
		return nil
	}
	due, err := s.broadcasts.ListBroadcastNoticesDue(ctx, s.now().UTC().Add(-noticeLookback), maxNoticesPerRound)
	if err != nil {
		return err
	}
	for _, item := range due {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := item.NoticeCount
		if item.State != "" {
			notice, ok, err := s.composeBroadcastNotice(ctx, item, count+1)
			if err != nil {
				return err
			}
			if ok {
				if err := s.notices.SendBroadcastNotice(ctx, notice); err != nil {
					slog.WarnContext(ctx, "mailbox broadcast notice not sent",
						slog.Uint64("broadcast_id", uint64(item.Broadcast.ID)), slog.String("kind", notice.Kind),
						slog.String("error", err.Error()))
					continue
				}
				count++
			}
		}
		// A state that needs no notice is recorded too, so the same pause later is told again.
		if _, err := s.broadcasts.MarkBroadcastNoticed(ctx, item.Broadcast.ID, item.NoticedState, item.State, count); err != nil {
			return err
		}
	}
	return nil
}

// composeBroadcastNotice writes the notice for a broadcast's state. It reports false when there is
// no one to tell: the starter left the organization.
func (s *service) composeBroadcastNotice(ctx context.Context, item *domain.MailBroadcastNoticeDue, sequence int) (BroadcastNotice, bool, error) {
	broadcast := item.Broadcast
	starter := broadcast.StartedBy
	if starter == 0 {
		starter = broadcast.CreatedBy
	}
	memberships, err := s.memberships.ListMembershipsByUserIDs(ctx, broadcast.OrganizationID, []uint{starter})
	if err != nil {
		return BroadcastNotice{}, false, err
	}
	if len(memberships) == 0 || memberships[0].User == nil || memberships[0].User.Email == "" {
		return BroadcastNotice{}, false, nil
	}
	path := broadcastConsolePath + "?org=" + strconv.FormatUint(uint64(broadcast.OrganizationID), 10) +
		"&open=" + strconv.FormatUint(uint64(broadcast.ID), 10)
	notice := BroadcastNotice{
		UserID: starter,
		Email:  memberships[0].User.Email,
		Key:    "mail-broadcast." + strconv.FormatUint(uint64(broadcast.ID), 10) + "." + strconv.Itoa(sequence),
		Path:   path,
		URL:    s.settings.PublicURL + path,
	}
	name := noticeName(broadcast.Name)
	counts := broadcast.Counts
	waiting := fmt.Sprintf("%s waiting.", countOf(counts.Pending, "recipient is", "recipients are"))
	switch item.State {
	case noticeStateSent:
		notice.Kind = noticeKindSent
		notice.Title = fmt.Sprintf("Broadcast %q finished", name)
		body := fmt.Sprintf("%d of %s sent.", counts.Sent, countOf(counts.Total, "recipient was", "recipients were"))
		if counts.Failed > 0 {
			body += fmt.Sprintf(" %d failed.", counts.Failed)
		}
		if counts.Skipped > 0 {
			body += fmt.Sprintf(" %d skipped because the contact unsubscribed, was deleted, or is on the suppression list.", counts.Skipped)
		}
		if counts.Failed > 0 {
			body += " Open the broadcast to see who failed and why."
		} else {
			body += " Open the broadcast to see deliveries, bounces, and spam reports."
		}
		notice.Body = body
	case noticeStateFailed:
		notice.Kind = noticeKindFailed
		notice.Title = fmt.Sprintf("Broadcast %q could not be sent", name)
		notice.Body = fmt.Sprintf("None of the %s could be sent, so the broadcast stopped. "+
			"Open it to see the reason for each recipient, fix the list or the template, and send a new broadcast.",
			countOf(counts.Failed, "recipient", "recipients"))
	case stopReasonBounceRate:
		notice.Kind = noticeKindPaused
		notice.Title = fmt.Sprintf("Broadcast %q paused: too many bounces", name)
		notice.Body = fmt.Sprintf("%d of %d messages bounced, at or above the 5%% limit, so Zmail paused the broadcast "+
			"to protect your domain's reputation. %s Check the list for old or mistyped addresses, then resume or cancel the broadcast.",
			counts.Bounced, counts.Sent+counts.Failed, waiting)
	case stopReasonComplaintRate:
		notice.Kind = noticeKindPaused
		notice.Title = fmt.Sprintf("Broadcast %q paused: spam reports", name)
		notice.Body = fmt.Sprintf("%s the message as spam, at or above the 0.3%% limit, so Zmail paused the broadcast "+
			"to protect your domain's reputation. %s Check the content and who is on the list, then resume or cancel the broadcast.",
			countOf(counts.Complained, "recipient reported", "recipients reported"), waiting)
	case stopReasonSendLimit:
		notice.Kind = noticeKindPaused
		notice.Title = fmt.Sprintf("Broadcast %q is waiting for the daily send limit", name)
		resumes := "when the 24-hour window has room"
		progress, err := s.BroadcastProgress(ctx, broadcast)
		if err != nil {
			return BroadcastNotice{}, false, err
		}
		if progress != nil && progress.SendLimitResumesAt != nil {
			resumes = "at about " + progress.SendLimitResumesAt.UTC().Format("2006-01-02 15:04") + " UTC"
		}
		notice.Body = fmt.Sprintf("Your organization reached its 24-hour broadcast limit. %d of %d sent so far; %s "+
			"Sending continues on its own %s. Nothing needs to be done; you can still pause or cancel the broadcast.",
			counts.Sent, counts.Total, strings.ToLower(waiting[:1])+waiting[1:], resumes)
	default:
		return BroadcastNotice{}, false, nil
	}
	return notice, true, nil
}

// countOf writes a count with the singular or plural phrase after it.
func countOf(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + plural
}

func noticeName(name string) string {
	if utf8.RuneCountInString(name) <= maxNoticeNameLength {
		return name
	}
	runes := []rune(name)
	return string(runes[:maxNoticeNameLength-1]) + "…"
}

// noticeMailer is the shared system mailer that sends account mail such as password resets.
type noticeMailer interface {
	IsConfigured() bool
	SendEmailIdempotent(ctx context.Context, to []string, subject, htmlContent, idempotencyKey string) error
}

// noticeDelivery puts a broadcast notice in the starter's notification center, when the
// notification starter runs, and emails it to their account address through the system mailer.
type noticeDelivery struct {
	publisher domain.NotificationPublisher
	mailer    noticeMailer
}

// NewBroadcastNoticeSender delivers broadcast notices in the app and by email.
func NewBroadcastNoticeSender(publisher domain.NotificationPublisher, mailer *email.Service) broadcastNoticeSender {
	delivery := &noticeDelivery{publisher: publisher}
	if mailer != nil {
		delivery.mailer = mailer
	}
	return delivery
}

// SendBroadcastNotice returns an error only when the email may succeed on a later try. The in-app
// notice is best effort: without the notification starter there is none, and a retry publishes it
// again under the same key, which the notification center keeps once.
func (d *noticeDelivery) SendBroadcastNotice(ctx context.Context, notice BroadcastNotice) error {
	if d.publisher != nil {
		_, err := d.publisher.Publish(ctx, domain.NotificationPublication{
			UserID:         notice.UserID,
			IdempotencyKey: notice.Key,
			Kind:           notice.Kind,
			Title:          notice.Title,
			Body:           notice.Body,
			ActionURL:      notice.Path,
			Channels:       []domain.NotificationChannel{domain.NotificationChannelInApp},
		})
		if err != nil && !errors.Is(err, domain.ErrServiceUnavailable) {
			slog.WarnContext(ctx, "mailbox broadcast in-app notice not published",
				slog.String("kind", notice.Kind), slog.String("error", err.Error()))
		}
	}
	if d.mailer == nil || !d.mailer.IsConfigured() {
		return nil
	}
	err := d.mailer.SendEmailIdempotent(ctx, []string{notice.Email}, notice.Title, renderNoticeEmail(notice), notice.Key)
	if errors.Is(err, email.ErrInvalidMessage) || errors.Is(err, email.ErrNotConfigured) {
		// Trying again cannot help.
		slog.WarnContext(ctx, "mailbox broadcast notice email refused",
			slog.String("kind", notice.Kind), slog.String("error", err.Error()))
		return nil
	}
	return err
}

func renderNoticeEmail(notice BroadcastNotice) string {
	var builder strings.Builder
	builder.WriteString(`<p>`)
	builder.WriteString(html.EscapeString(notice.Body))
	builder.WriteString(`</p>`)
	if notice.URL != "" {
		builder.WriteString(`<p><a href="`)
		builder.WriteString(html.EscapeString(notice.URL))
		builder.WriteString(`">Open the broadcast in Zmail</a></p>`)
	}
	builder.WriteString(`<p style="color:#71717a;font-size:12px;">You get this because you started this broadcast.</p>`)
	return builder.String()
}
