package mailbox

import (
	"context"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// unsealedMessage keeps the mail_messages rows created at or after their mailbox's seal boundary.
// A sealed message is treated as absent on every surface.
const unsealedMessage = `NOT EXISTS (SELECT 1 FROM mailboxes AS seal WHERE seal.id = mail_messages.mailbox_id
	AND seal.sealed_before IS NOT NULL AND mail_messages.created_at < seal.sealed_before)`

// unsealedBroadcast keeps the mail_broadcasts rows created at or after their sender mailbox's seal
// boundary.
const unsealedBroadcast = `NOT EXISTS (SELECT 1 FROM mailboxes AS seal WHERE seal.id = mail_broadcasts.mailbox_id
	AND seal.sealed_before IS NOT NULL AND mail_broadcasts.created_at < seal.sealed_before)`

// MarkOwnerLeft sets owner_left_at on the user's live personal mailboxes in the organization and
// returns them. A repeated departure records the later time, so the boundary is the latest one.
func (r *repository) MarkOwnerLeft(ctx context.Context, organizationID, userID uint, at time.Time) ([]*domain.Mailbox, error) {
	return r.updateOwnedMailboxes(ctx, organizationID, userID, "TRUE", at)
}

// ClearOwnerLeft clears owner_left_at on the user's live personal mailboxes that await handover
// and returns them.
func (r *repository) ClearOwnerLeft(ctx context.Context, organizationID, userID uint) ([]*domain.Mailbox, error) {
	return r.updateOwnedMailboxes(ctx, organizationID, userID, "owner_left_at IS NOT NULL", nil)
}

func (r *repository) updateOwnedMailboxes(ctx context.Context, organizationID, userID uint, condition string, leftAt any) ([]*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailboxPO
	err := r.conn(ctx).Raw(`UPDATE mailboxes SET owner_left_at = ?, updated_at = now()
		WHERE organization_id = ? AND owner_user_id = ? AND type = 'personal' AND deleted_at IS NULL AND `+condition+`
		RETURNING *`, leftAt, organizationID, userID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	for i := range rows {
		mailboxes = append(mailboxes, rows[i].toDomain())
	}
	return mailboxes, nil
}

// MailboxesGrantedTo reads the access history: a live shared mailbox whose latest event naming the
// membership grants it (created, member_set, handed_over) rather than removes it (member_removed).
// Bounded by the organization's mailbox limit.
func (r *repository) MailboxesGrantedTo(ctx context.Context, organizationID, membershipID uint) ([]uint, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var ids []uint
	err := r.conn(ctx).Raw(`SELECT latest.mailbox_id FROM (
		  SELECT DISTINCT ON (events.mailbox_id) events.mailbox_id, events.type
		  FROM mailbox_access_events events
		  JOIN mailboxes ON mailboxes.id = events.mailbox_id AND mailboxes.deleted_at IS NULL AND mailboxes.type = 'shared'
		  WHERE events.organization_id = ? AND events.detail->>'membership_id' = ?
		    AND events.type IN ('created', 'member_set', 'handed_over', 'member_removed')
		  ORDER BY events.mailbox_id, events.id DESC
		) latest
		WHERE latest.type <> 'member_removed'
		ORDER BY latest.mailbox_id
		LIMIT ?`, organizationID, strconv.FormatUint(uint64(membershipID), 10), domain.MailboxesPerOrganizationCeiling).
		Scan(&ids).Error
	return ids, err
}

// HandOverMailbox turns a personal mailbox into a shared one sealed before the boundary, and
// cancels its sealed broadcasts still running, so they send no new mail the successor could read.
// Only one of several concurrent calls finds the mailbox still personal; the others report false
// and change nothing.
func (r *repository) HandOverMailbox(ctx context.Context, mailboxID uint, sealedBefore time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	converted := false
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&MailboxPO{}).
			Where("id = ? AND type = ?", mailboxID, string(domain.MailboxTypePersonal)).
			Updates(map[string]any{
				"type":          string(domain.MailboxTypeShared),
				"owner_user_id": nil,
				"owner_left_at": nil,
				"sealed_before": sealedBefore,
				// The previous owner's AI triage consent does not carry over.
				"ai_triage_enabled": false,
				"updated_at":        time.Now().UTC(),
			})
		if result.Error != nil {
			return result.Error
		}
		if converted = result.RowsAffected == 1; !converted {
			return nil
		}
		// Waiting messages from before the boundary are sealed: they must never be sent.
		if err := tx.Model(&MailMessagePO{}).
			Where("mailbox_id = ? AND queued_reason IS NOT NULL AND created_at < ?", mailboxID, sealedBefore).
			Updates(map[string]any{
				"status":           string(domain.MailStatusFailed),
				"failure_code":     failureSendCanceled,
				"queued_reason":    nil,
				"send_lease_until": nil,
				"updated_at":       time.Now().UTC(),
			}).Error; err != nil {
			return err
		}
		return cancelSealedBroadcasts(tx, mailboxID, sealedBefore)
	})
	return converted, err
}

// cancelSealedBroadcasts cancels the mailbox's broadcasts created before the boundary that are
// still scheduled, sending, or paused, with their pending recipients.
func cancelSealedBroadcasts(tx *gorm.DB, mailboxID uint, before time.Time) error {
	sealedBroadcasts := tx.Model(&MailBroadcastPO{}).Select("id").Where("mailbox_id = ? AND created_at < ?", mailboxID, before)
	running := []string{string(domain.MailBroadcastScheduled), string(domain.MailBroadcastSending), string(domain.MailBroadcastPaused)}
	if err := tx.Model(&MailBroadcastPO{}).
		Where("mailbox_id = ? AND created_at < ? AND status IN ?", mailboxID, before, running).
		Updates(map[string]any{
			"status": string(domain.MailBroadcastCanceled), "resume_at": nil,
			"finished_at": gorm.Expr("now()"), "updated_at": gorm.Expr("now()"),
		}).Error; err != nil {
		return err
	}
	return tx.Model(&MailBroadcastRecipientPO{}).
		Where("broadcast_id IN (?) AND status = ?", sealedBroadcasts, string(domain.MailRecipientPending)).
		Updates(map[string]any{"status": string(domain.MailRecipientCanceled), "updated_at": gorm.Expr("now()")}).Error
}

// SetMailboxWebhooks updates the switch only when it changes, so a repeated value reports false.
func (r *repository) SetMailboxWebhooks(ctx context.Context, mailboxID uint, enabled bool) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailboxPO{}).
		Where("id = ? AND type = ? AND webhooks_enabled <> ?", mailboxID, string(domain.MailboxTypeShared), enabled).
		Updates(map[string]any{"webhooks_enabled": enabled, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// DeleteSealedHistory cancels the mailbox's sealed broadcasts still running, then deletes its
// sealed broadcasts with their recipients and its sealed messages with their attachments,
// events, read, star, and label rows. The boundary stays.
func (r *repository) DeleteSealedHistory(ctx context.Context, mailboxID uint, before time.Time) (int64, int64, error) {
	if r.db == nil {
		return 0, 0, domain.ErrServiceUnavailable
	}
	var messages, broadcasts int64
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := cancelSealedBroadcasts(tx, mailboxID, before); err != nil {
			return err
		}
		sealedBroadcasts := tx.Model(&MailBroadcastPO{}).Select("id").Where("mailbox_id = ? AND created_at < ?", mailboxID, before)
		if err := tx.Where("broadcast_id IN (?)", sealedBroadcasts).Delete(&MailBroadcastRecipientPO{}).Error; err != nil {
			return err
		}
		deleted := tx.Where("mailbox_id = ? AND created_at < ?", mailboxID, before).Delete(&MailBroadcastPO{})
		if deleted.Error != nil {
			return deleted.Error
		}
		broadcasts = deleted.RowsAffected

		sealedMessages := tx.Model(&MailMessagePO{}).Select("id").Where("mailbox_id = ? AND created_at < ?", mailboxID, before)
		// Recipients of other broadcasts never point at a deleted message.
		if err := tx.Model(&MailBroadcastRecipientPO{}).Where("message_id IN (?)", sealedMessages).
			Update("message_id", nil).Error; err != nil {
			return err
		}
		for _, dependant := range []any{&MailAttachmentPO{}, &MailMessageEventPO{}, &MailMessageReadPO{}, &MailMessageStarPO{}, &MailMessageLabelPO{}} {
			if err := tx.Where("message_id IN (?)", sealedMessages).Delete(dependant).Error; err != nil {
				return err
			}
		}
		deleted = tx.Where("mailbox_id = ? AND created_at < ?", mailboxID, before).Delete(&MailMessagePO{})
		if deleted.Error != nil {
			return deleted.Error
		}
		messages = deleted.RowsAffected
		return nil
	})
	return messages, broadcasts, err
}
