package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// queuedSendLease is how long one worker holds a waiting message before another may take it.
const queuedSendLease = 2 * time.Minute

// DispatchQueuedSends sends up to limit messages that waited for room in the send limit, oldest
// first in each organization, never more than the organization's rolling window has room for.
// It returns how many messages it finished, sent, failed, or canceled.
func (s *service) DispatchQueuedSends(ctx context.Context, limit int) (int, error) {
	organizations, err := s.repo.WaitingSendOrganizations(ctx)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, organizationID := range organizations {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if processed >= limit {
			break
		}
		finished, err := s.dispatchOrganizationQueue(ctx, organizationID, limit-processed)
		processed += finished
		if err != nil {
			return processed, err
		}
	}
	return processed, nil
}

func (s *service) dispatchOrganizationQueue(ctx context.Context, organizationID uint, limit int) (int, error) {
	now := s.now().UTC()
	if err := s.checkSendingHealth(ctx, organizationID); errors.Is(err, ErrMailSendingPaused) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	laneLimit, err := s.organizationLaneLimit(ctx, organizationID, domain.SendLanePersonal)
	if err != nil {
		return 0, err
	}
	messages, err := s.repo.LeaseWaitingSends(ctx, organizationID, now, now.Add(queuedSendLease),
		now.Add(-sendLimitWindow), laneLimit, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, message := range messages {
		if ctx.Err() != nil {
			break
		}
		if err := s.sendWaiting(ctx, organizationID, message); err != nil {
			return processed, err
		}
		processed++
	}
	if processed > 0 {
		slog.InfoContext(ctx, "mail_queued_send.dispatched",
			slog.Uint64("organization_id", uint64(organizationID)), slog.Int("count", processed))
	}
	return processed, nil
}

// sendWaiting sends one leased waiting message, or cancels it when its mailbox is gone or a
// handover sealed it.
func (s *service) sendWaiting(ctx context.Context, organizationID uint, message *domain.MailMessage) error {
	mailbox, err := s.repo.FindMailbox(ctx, organizationID, message.MailboxID)
	if errors.Is(err, ErrMailboxNotFound) || (err == nil && sealedFor(mailbox, message.CreatedAt)) {
		slog.InfoContext(ctx, "mail_queued_send.canceled", slog.Uint64("message_id", uint64(message.ID)))
		return s.repo.CancelWaitingSend(ctx, message.ID, failureSendCanceled)
	}
	if err != nil {
		return err
	}
	// Load the attachments before starting the attempt: a message that cannot be loaded stays
	// waiting and is leased again when its lease expires, instead of being started and never sent.
	files, err := s.storedFiles(ctx, message.ID)
	if err != nil {
		slog.WarnContext(ctx, "mail_queued_send.attachments_unavailable",
			slog.Uint64("message_id", uint64(message.ID)), slog.String("error", err.Error()))
		return nil
	}
	attemptedAt := s.now().UTC()
	started, err := s.repo.StartWaitingSend(ctx, message.ID, attemptedAt)
	if err != nil || !started {
		// Deleted or canceled since the lease: nothing to send.
		return err
	}
	message.QueuedReason = ""
	message.ScheduledAt = nil
	message.SendAttemptedAt = &attemptedAt
	// A provider refusal is recorded on the message; only a failure to record stops the worker.
	_, _, err = s.deliver(ctx, mailbox, message, files, nil)
	if err != nil && !isRecordedSendFailure(err) {
		return err
	}
	return nil
}

// storedFiles loads a stored message's attachments with their content for the provider.
func (s *service) storedFiles(ctx context.Context, messageID uint) ([]mailtransport.Attachment, error) {
	listed, err := s.repo.ListMessageAttachments(ctx, messageID)
	if err != nil {
		return nil, err
	}
	files := make([]mailtransport.Attachment, 0, len(listed))
	for _, attachment := range listed {
		stored, err := s.repo.FindMessageAttachment(ctx, messageID, attachment.ID)
		if err != nil {
			return nil, err
		}
		files = append(files, mailtransport.Attachment{
			Filename: stored.Filename, ContentType: stored.ContentType, Content: stored.Content,
			ContentID: stored.ContentID, Inline: stored.Inline,
		})
	}
	return files, nil
}

// isRecordedSendFailure reports a send whose provider failure is already stored on the message.
func isRecordedSendFailure(err error) bool {
	return errors.Is(err, ErrMailDeliveryFailed) || errors.Is(err, ErrMailSenderNotConfigured)
}
