package mailbox

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailSnoozeRepository = (*repository)(nil)

// CountWaitingSends counts the organization's messages waiting for room in the send limit.
// Future scheduled sends are excluded so scheduling an email never blocks immediate sends.
func (r *repository) CountWaitingSends(ctx context.Context, organizationID uint) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&MailMessagePO{}).
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id").
		Where("mailboxes.organization_id = ? AND mail_messages.queued_reason = ?",
			organizationID, domain.MailQueuedForSendLimit).
		Count(&count).Error
	return count, err
}

// WaitingSendOrganizations lists the organizations that have queued messages (waiting for room in
// the send limit or scheduled for later delivery) outside the trash.
func (r *repository) WaitingSendOrganizations(ctx context.Context) ([]uint, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var organizations []uint
	err := r.conn(ctx).Model(&MailMessagePO{}).
		Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id").
		Where("mail_messages.queued_reason IS NOT NULL AND mail_messages.placement <> ?",
			string(domain.MailPlacementTrash)).
		Distinct().Order("mailboxes.organization_id").
		Pluck("mailboxes.organization_id", &organizations).Error
	return organizations, err
}

// LeaseWaitingSends leases the organization's oldest eligible waiting or due scheduled messages to
// one worker, bounded by the organization's remaining personal lane room.
func (r *repository) LeaseWaitingSends(
	ctx context.Context,
	organizationID uint,
	now, leaseUntil, windowStart time.Time,
	sendLimit, limit int,
) ([]*domain.MailMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return nil, nil
	}
	var rows []MailMessagePO
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// One worker at a time works out an organization's room; the lock ends with the transaction.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('zmail.mail_send_queue'), ?::int)`,
			organizationID).Error; err != nil {
			return err
		}
		// Only direct sends wait, so only the personal lane's attempts take their room.
		var held int64
		attempts, err := countLaneAttempts(tx, organizationID, domain.SendLanePersonal, windowStart)
		if err != nil {
			return err
		}
		if err := tx.Model(&MailMessagePO{}).
			Joins("JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id").
			Where("mailboxes.organization_id = ? AND mail_messages.queued_reason IS NOT NULL AND mail_messages.send_lease_until > ?",
				organizationID, now).
			Count(&held).Error; err != nil {
			return err
		}
		take := min(sendLimit-int(attempts)-int(held), limit)
		if take <= 0 {
			return nil
		}
		if err := tx.Raw(`SELECT mail_messages.* FROM mail_messages
			JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id
			WHERE mailboxes.organization_id = ?
			  AND mail_messages.placement <> ?
			  AND (
			    mail_messages.queued_reason = ?
			    OR (mail_messages.queued_reason = ? AND mail_messages.scheduled_at IS NOT NULL AND mail_messages.scheduled_at <= ?)
			  )
			  AND (mail_messages.send_lease_until IS NULL OR mail_messages.send_lease_until <= ?)
			ORDER BY mail_messages.id
			LIMIT ?
			FOR UPDATE OF mail_messages SKIP LOCKED`,
			organizationID,
			string(domain.MailPlacementTrash),
			domain.MailQueuedForSendLimit,
			domain.MailQueuedForSchedule,
			now,
			now,
			take,
		).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uint, 0, len(rows))
		for i := range rows {
			ids = append(ids, rows[i].ID)
		}
		return tx.Model(&MailMessagePO{}).Where("id IN ?", ids).Update("send_lease_until", leaseUntil).Error
	})
	if err != nil {
		return nil, err
	}
	messages := make([]*domain.MailMessage, 0, len(rows))
	for i := range rows {
		messages = append(messages, rows[i].toDomain())
	}
	return messages, nil
}

// StartWaitingSend turns a waiting or scheduled message into a started attempt, clearing its
// queued_reason and scheduled_at.
func (r *repository) StartWaitingSend(ctx context.Context, id uint, attemptedAt time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailMessagePO{}).
		Where("id = ? AND queued_reason IS NOT NULL AND placement <> ?", id, string(domain.MailPlacementTrash)).
		Updates(map[string]any{
			"queued_reason":     nil,
			"scheduled_at":      nil,
			"send_lease_until":  nil,
			"send_attempted_at": attemptedAt,
			"updated_at":        time.Now().UTC(),
		})
	return result.RowsAffected == 1, result.Error
}

// CancelWaitingSend marks a waiting or scheduled message failed, never to be sent.
func (r *repository) CancelWaitingSend(ctx context.Context, id uint, failureCode string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MailMessagePO{}).
		Where("id = ? AND queued_reason IS NOT NULL", id).
		Updates(map[string]any{
			"status":           string(domain.MailStatusFailed),
			"failure_code":     failureCode,
			"queued_reason":    nil,
			"scheduled_at":     nil,
			"send_lease_until": nil,
			"updated_at":       time.Now().UTC(),
		}).Error
}

// WakeDueSnoozedMessages wakes every default-placement (Inbox) message whose snooze window has
// elapsed by clearing snoozed_until and removing its read rows so it pops back into the Inbox as
// unread. Uses the partial index idx_mail_messages_snoozed_until so empty ticks touch zero heap
// pages.
func (r *repository) WakeDueSnoozedMessages(ctx context.Context, now time.Time) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	at := now.UTC()
	var wokenIDs []uint
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(`UPDATE mail_messages
			SET snoozed_until = NULL, updated_at = ?
			WHERE snoozed_until IS NOT NULL AND snoozed_until <= ? AND placement = ?
			RETURNING id`, at, at, string(domain.MailPlacementDefault)).Scan(&wokenIDs).Error; err != nil {
			return err
		}
		if len(wokenIDs) == 0 {
			return nil
		}
		return tx.Where("message_id IN ?", wokenIDs).Delete(&MailMessageReadPO{}).Error
	})
	if err != nil {
		return 0, err
	}
	return len(wokenIDs), nil
}
