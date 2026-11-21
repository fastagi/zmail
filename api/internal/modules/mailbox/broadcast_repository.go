package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailBroadcastRepository = (*repository)(nil)

type broadcastCountRow struct {
	BroadcastID uint
	Status      string
	Count       int
}

// fillCounts loads recipient counts by status for the given broadcasts.
func fillCounts(tx *gorm.DB, broadcasts []*domain.MailBroadcast) error {
	if len(broadcasts) == 0 {
		return nil
	}
	byID := make(map[uint]*domain.MailBroadcast, len(broadcasts))
	ids := make([]uint, 0, len(broadcasts))
	for _, broadcast := range broadcasts {
		byID[broadcast.ID] = broadcast
		ids = append(ids, broadcast.ID)
	}
	var rows []broadcastCountRow
	err := tx.Model(&MailBroadcastRecipientPO{}).
		Select("broadcast_id, status, COUNT(*) AS count").
		Where("broadcast_id IN ?", ids).
		Group("broadcast_id, status").
		Scan(&rows).Error
	if err != nil {
		return err
	}
	for _, row := range rows {
		counts := &byID[row.BroadcastID].Counts
		counts.Total += row.Count
		switch domain.MailRecipientStatus(row.Status) {
		case domain.MailRecipientPending:
			counts.Pending += row.Count
		case domain.MailRecipientSent:
			counts.Sent += row.Count
		case domain.MailRecipientFailed:
			counts.Failed += row.Count
		case domain.MailRecipientSkipped:
			counts.Skipped += row.Count
		case domain.MailRecipientCanceled:
			counts.Canceled += row.Count
		}
	}
	// A recipient's message later reported bounced or complained; a send-time bounce counts too.
	var health []struct {
		BroadcastID uint
		Bounced     int
		Complained  int
	}
	err = tx.Raw(`SELECT r.broadcast_id,
			COUNT(*) FILTER (WHERE m.last_event IN ('bounced', 'rejected') OR r.failure_code = ?) AS bounced,
			COUNT(*) FILTER (WHERE m.last_event = 'complained') AS complained
		FROM mail_broadcast_recipients r
		LEFT JOIN mail_messages m ON m.id = r.message_id
		WHERE r.broadcast_id IN ? AND r.status IN ('sent', 'failed')
		GROUP BY r.broadcast_id`, failurePermanentBounce, ids).Scan(&health).Error
	if err != nil {
		return err
	}
	for _, row := range health {
		counts := &byID[row.BroadcastID].Counts
		counts.Bounced, counts.Complained = row.Bounced, row.Complained
	}
	return nil
}

// failurePermanentBounce is the failure code of a message the provider refused for good.
const failurePermanentBounce = "transport.permanent_bounce"

func (r *repository) ListBroadcasts(ctx context.Context, organizationID uint, mailboxIDs []uint, page, perPage int) ([]*domain.MailBroadcast, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	if len(mailboxIDs) == 0 {
		return []*domain.MailBroadcast{}, 0, nil
	}
	query := r.conn(ctx).Model(&MailBroadcastPO{}).Where("organization_id = ? AND mailbox_id IN ?", organizationID, mailboxIDs).
		Where(unsealedBroadcast)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MailBroadcastPO
	// The listing never needs the template snapshot.
	err := query.Omit("subject", "html_body", "text_body").
		Order("id DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	broadcasts := make([]*domain.MailBroadcast, 0, len(rows))
	for i := range rows {
		broadcasts = append(broadcasts, rows[i].toDomain())
	}
	if err := fillCounts(r.conn(ctx), broadcasts); err != nil {
		return nil, 0, err
	}
	return broadcasts, total, nil
}

func (r *repository) FindBroadcast(ctx context.Context, organizationID, id uint) (*domain.MailBroadcast, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailBroadcastPO
	if err := r.conn(ctx).Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailBroadcastNotFound
		}
		return nil, err
	}
	broadcast := po.toDomain()
	if err := fillCounts(r.conn(ctx), []*domain.MailBroadcast{broadcast}); err != nil {
		return nil, err
	}
	return broadcast, nil
}

func (r *repository) FindBroadcastForWorker(ctx context.Context, id uint) (*domain.MailBroadcast, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailBroadcastPO
	if err := r.conn(ctx).Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailBroadcastNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) CreateBroadcast(ctx context.Context, broadcast *domain.MailBroadcast) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := &MailBroadcastPO{
		OrganizationID:   broadcast.OrganizationID,
		Name:             broadcast.Name,
		MailboxID:        broadcast.MailboxID,
		TemplateID:       broadcast.TemplateID,
		ListID:           broadcast.ListID,
		UnsubscribeLabel: broadcast.UnsubscribeLabel,
		Variables:        encodeProperties(broadcast.Variables),
		Status:           string(domain.MailBroadcastDraft),
		TestPercent:      broadcast.TestPercent,
		TestWaitMinutes:  broadcast.TestWaitMinutes,
		CurrentWave:      1,
		CreatedBy:        broadcast.CreatedBy,
	}
	if err := r.conn(ctx).Create(po).Error; err != nil {
		return err
	}
	*broadcast = *po.toDomain()
	return nil
}

func (r *repository) UpdateDraft(ctx context.Context, broadcast *domain.MailBroadcast) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailBroadcastPO{}).
		Where("id = ? AND organization_id = ? AND status = ?", broadcast.ID, broadcast.OrganizationID, string(domain.MailBroadcastDraft)).
		Updates(map[string]any{
			"name":              broadcast.Name,
			"mailbox_id":        broadcast.MailboxID,
			"template_id":       broadcast.TemplateID,
			"list_id":           broadcast.ListID,
			"unsubscribe_label": broadcast.UnsubscribeLabel,
			"variables":         encodeProperties(broadcast.Variables),
			"test_percent":      broadcast.TestPercent,
			"test_wait_minutes": broadcast.TestWaitMinutes,
			"updated_at":        gorm.Expr("now()"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return r.notEditable(ctx, broadcast.OrganizationID, broadcast.ID)
	}
	stored, err := r.FindBroadcast(ctx, broadcast.OrganizationID, broadcast.ID)
	if err != nil {
		return err
	}
	*broadcast = *stored
	return nil
}

// notEditable tells a missing broadcast from one in the wrong state.
func (r *repository) notEditable(ctx context.Context, organizationID, id uint) error {
	if _, err := r.FindBroadcast(ctx, organizationID, id); err != nil {
		return err
	}
	return ErrMailBroadcastNotEditable
}

func (r *repository) DeleteBroadcast(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND organization_id = ? AND status IN ?", id, organizationID,
			[]string{string(domain.MailBroadcastDraft), string(domain.MailBroadcastSent), string(domain.MailBroadcastCanceled)}).
			Delete(&MailBroadcastPO{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return r.notEditable(ctx, organizationID, id)
		}
		return tx.Where("broadcast_id = ?", id).Delete(&MailBroadcastRecipientPO{}).Error
	})
}

func (r *repository) StartBroadcast(ctx context.Context, broadcast *domain.MailBroadcast) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	added := 0
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var locked MailBroadcastPO
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND organization_id = ?", broadcast.ID, broadcast.OrganizationID).
			First(&locked).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMailBroadcastNotFound
			}
			return err
		}
		if locked.Status != string(domain.MailBroadcastDraft) {
			return ErrMailBroadcastNotEditable
		}
		result := tx.Exec(`INSERT INTO mail_broadcast_recipients (broadcast_id, contact_id, email, status, created_at, updated_at)
			SELECT ?, c.id, c.email, 'pending', now(), now()
			FROM mail_contacts c
			JOIN mail_contact_list_members m ON m.contact_id = c.id
			JOIN mail_contact_lists l ON l.id = m.list_id AND l.organization_id = c.organization_id
			WHERE m.list_id = ? AND c.organization_id = ? AND c.status = 'subscribed'
			ON CONFLICT DO NOTHING`,
			broadcast.ID, locked.ListID, broadcast.OrganizationID)
		if result.Error != nil {
			return result.Error
		}
		added = int(result.RowsAffected)
		if added == 0 {
			return ErrMailBroadcastNoRecipients
		}
		if locked.TestPercent > 0 {
			// A random test_percent of the recipients, at least one, form wave 1; the rest wait.
			testSize := (added*locked.TestPercent + 99) / 100
			if err := tx.Exec(`UPDATE mail_broadcast_recipients SET wave = 2 WHERE broadcast_id = ?`, broadcast.ID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE mail_broadcast_recipients SET wave = 1 WHERE id IN (
					SELECT id FROM mail_broadcast_recipients WHERE broadcast_id = ? ORDER BY random() LIMIT ?)`,
				broadcast.ID, testSize).Error; err != nil {
				return err
			}
		}
		return tx.Model(&MailBroadcastPO{}).Where("id = ?", broadcast.ID).Updates(map[string]any{
			"current_wave": 1,
			"resume_at":    nil,
			"status":       string(broadcast.Status),
			"subject":      broadcast.Subject,
			"html_body":    broadcast.HTMLBody,
			"text_body":    broadcast.TextBody,
			"scheduled_at": broadcast.ScheduledAt,
			"started_at":   broadcast.StartedAt,
			"stop_reason":  "",
			"updated_at":   gorm.Expr("now()"),
		}).Error
	})
	return added, err
}

func (r *repository) CancelBroadcast(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&MailBroadcastPO{}).
			Where("id = ? AND organization_id = ? AND status IN ?", id, organizationID,
				[]string{string(domain.MailBroadcastScheduled), string(domain.MailBroadcastSending), string(domain.MailBroadcastPaused)}).
			Updates(map[string]any{
				"status":      string(domain.MailBroadcastCanceled),
				"resume_at":   nil,
				"finished_at": gorm.Expr("now()"),
				"updated_at":  gorm.Expr("now()"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return r.notEditable(ctx, organizationID, id)
		}
		// A recipient the worker holds right now still finishes; everything else stops here.
		return tx.Model(&MailBroadcastRecipientPO{}).
			Where("broadcast_id = ? AND status = ?", id, string(domain.MailRecipientPending)).
			Updates(map[string]any{"status": string(domain.MailRecipientCanceled), "updated_at": gorm.Expr("now()")}).Error
	})
}

func (r *repository) ListRecipients(
	ctx context.Context,
	organizationID, broadcastID uint,
	status domain.MailRecipientStatus,
	page, perPage int,
) ([]*domain.MailBroadcastRecipient, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	if _, err := r.FindBroadcast(ctx, organizationID, broadcastID); err != nil {
		return nil, 0, err
	}
	query := r.conn(ctx).Model(&MailBroadcastRecipientPO{}).Where("broadcast_id = ?", broadcastID)
	if status != "" {
		query = query.Where("status = ?", string(status))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MailBroadcastRecipientPO
	if err := query.Order("id ASC").Offset((page - 1) * perPage).Limit(perPage).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	recipients := make([]*domain.MailBroadcastRecipient, 0, len(rows))
	for i := range rows {
		recipients = append(recipients, rows[i].toDomain())
	}
	return recipients, total, nil
}

func (r *repository) PromoteScheduled(ctx context.Context, now time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MailBroadcastPO{}).
		Where("status = ? AND scheduled_at <= ?", string(domain.MailBroadcastScheduled), now).
		Updates(map[string]any{
			"status":     string(domain.MailBroadcastSending),
			"started_at": now,
			"updated_at": gorm.Expr("now()"),
		}).Error
}

func (r *repository) LeasePending(ctx context.Context, now, leaseUntil time.Time, limit int) ([]*domain.MailBroadcastRecipient, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailBroadcastRecipientPO
	err := r.conn(ctx).Raw(`UPDATE mail_broadcast_recipients SET lease_until = ?, updated_at = now()
		WHERE id IN (
			SELECT r.id FROM mail_broadcast_recipients r
			JOIN mail_broadcasts b ON b.id = r.broadcast_id
			WHERE b.status = 'sending' AND r.status = 'pending' AND r.wave <= b.current_wave
			  AND (r.lease_until IS NULL OR r.lease_until < ?)
			ORDER BY r.id
			LIMIT ?
			FOR UPDATE OF r SKIP LOCKED
		)
		RETURNING *`, leaseUntil, now, limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	recipients := make([]*domain.MailBroadcastRecipient, 0, len(rows))
	for i := range rows {
		recipients = append(recipients, rows[i].toDomain())
	}
	return recipients, nil
}

func (r *repository) FinishRecipient(ctx context.Context, id uint, status domain.MailRecipientStatus, messageID uint, failureCode string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	values := map[string]any{
		"status":       string(status),
		"failure_code": failureCode,
		"lease_until":  nil,
		"updated_at":   gorm.Expr("now()"),
	}
	if messageID != 0 {
		values["message_id"] = messageID
	}
	return r.conn(ctx).Model(&MailBroadcastRecipientPO{}).
		Where("id = ? AND status = ?", id, string(domain.MailRecipientPending)).
		Updates(values).Error
}

func (r *repository) ReleaseRecipient(ctx context.Context, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MailBroadcastRecipientPO{}).Where("id = ?", id).
		Updates(map[string]any{"lease_until": nil, "updated_at": gorm.Expr("now()")}).Error
}

func (r *repository) SetStopReason(ctx context.Context, id uint, reason string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MailBroadcastPO{}).Where("id = ? AND stop_reason <> ?", id, reason).
		Updates(map[string]any{"stop_reason": reason, "updated_at": gorm.Expr("now()")}).Error
}

func (r *repository) CountWaitingBroadcastRecipients(ctx context.Context, organizationID uint, stopReason string) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&MailBroadcastRecipientPO{}).
		Joins("JOIN mail_broadcasts ON mail_broadcasts.id = mail_broadcast_recipients.broadcast_id").
		Where("mail_broadcasts.organization_id = ? AND mail_broadcasts.stop_reason = ? AND mail_broadcast_recipients.status = ?",
			organizationID, stopReason, string(domain.MailRecipientPending)).
		Count(&count).Error
	return count, err
}

func (r *repository) FinishBroadcasts(ctx context.Context, now time.Time, allFailedReason string) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	// A broadcast that reached no one is not reported as sent.
	result := r.conn(ctx).Exec(`UPDATE mail_broadcasts b SET
			status = CASE WHEN outcome.failed THEN 'canceled' ELSE 'sent' END,
			stop_reason = CASE WHEN outcome.failed THEN ? ELSE '' END,
			finished_at = ?, updated_at = now()
		FROM (
			SELECT b2.id,
				EXISTS (SELECT 1 FROM mail_broadcast_recipients r WHERE r.broadcast_id = b2.id AND r.status = 'failed')
				AND NOT EXISTS (SELECT 1 FROM mail_broadcast_recipients r WHERE r.broadcast_id = b2.id AND r.status = 'sent') AS failed
			FROM mail_broadcasts b2
			WHERE b2.status = 'sending'
			  AND NOT EXISTS (SELECT 1 FROM mail_broadcast_recipients r WHERE r.broadcast_id = b2.id AND r.status = 'pending')
		) outcome
		WHERE b.id = outcome.id AND b.status = 'sending'`, allFailedReason, now)
	return int(result.RowsAffected), result.Error
}

func (r *repository) ListPropertyNames(ctx context.Context, organizationID, listID uint) ([]string, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	names := []string{}
	err := r.conn(ctx).Raw(`SELECT DISTINCT key FROM mail_contacts c
		JOIN mail_contact_list_members m ON m.contact_id = c.id
		CROSS JOIN LATERAL jsonb_object_keys(c.properties::jsonb) AS key
		WHERE m.list_id = ? AND c.organization_id = ? AND c.status = 'subscribed'
		ORDER BY key`, listID, organizationID).Scan(&names).Error
	return names, err
}

func (r *repository) PauseBroadcast(ctx context.Context, organizationID, id uint, reason string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).Model(&MailBroadcastPO{}).Where("id = ?", id)
	if organizationID != 0 {
		// An owner pauses a sending broadcast, or one whose test wave is waiting.
		query = query.Where("organization_id = ? AND status IN ?", organizationID,
			[]string{string(domain.MailBroadcastSending), string(domain.MailBroadcastPaused)})
	} else {
		query = query.Where("status IN ?", []string{string(domain.MailBroadcastSending), string(domain.MailBroadcastPaused)})
	}
	result := query.Updates(map[string]any{
		"status":      string(domain.MailBroadcastPaused),
		"stop_reason": reason,
		"resume_at":   nil,
		"updated_at":  gorm.Expr("now()"),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 && organizationID != 0 {
		return r.notEditable(ctx, organizationID, id)
	}
	return nil
}

func (r *repository) ResumeBroadcast(ctx context.Context, organizationID, id uint, override bool) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).Model(&MailBroadcastPO{}).Where("id = ? AND status = ?", id, string(domain.MailBroadcastPaused))
	if organizationID != 0 {
		query = query.Where("organization_id = ?", organizationID)
	}
	// A test wave that finished, or a broadcast the owner overrides, continues with everyone; one
	// the owner paused by hand continues the wave it was in.
	values := map[string]any{
		"status":      string(domain.MailBroadcastSending),
		"stop_reason": "",
		"resume_at":   nil,
		"current_wave": gorm.Expr("CASE WHEN stop_reason = ? OR ? THEN 2 ELSE current_wave END",
			stopReasonTestWave, override),
		"updated_at": gorm.Expr("now()"),
	}
	if override {
		values["safety_override"] = true
	}
	result := query.Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 && organizationID != 0 {
		return r.notEditable(ctx, organizationID, id)
	}
	return nil
}

func (r *repository) CompleteTestWaves(ctx context.Context, now time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Exec(`UPDATE mail_broadcasts b
		SET status = 'paused', stop_reason = ?, resume_at = CAST(? AS timestamptz) + b.test_wait_minutes * interval '1 minute', updated_at = now()
		WHERE b.status = 'sending' AND b.current_wave = 1 AND b.test_percent > 0
		  AND NOT EXISTS (SELECT 1 FROM mail_broadcast_recipients r WHERE r.broadcast_id = b.id AND r.status = 'pending' AND r.wave = 1)
		  AND EXISTS (SELECT 1 FROM mail_broadcast_recipients r WHERE r.broadcast_id = b.id AND r.status = 'pending' AND r.wave = 2)`,
		stopReasonTestWave, now).Error
}

func (r *repository) ListActiveBroadcasts(ctx context.Context, limit int) ([]*domain.MailBroadcast, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailBroadcastPO
	err := r.conn(ctx).Select("id, organization_id, status, stop_reason, test_percent, test_wait_minutes, current_wave, resume_at, safety_override").
		Where("status = ? OR (status = ? AND stop_reason = ?)",
			string(domain.MailBroadcastSending), string(domain.MailBroadcastPaused), stopReasonTestWave).
		Order("id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	broadcasts := make([]*domain.MailBroadcast, 0, len(rows))
	for i := range rows {
		broadcasts = append(broadcasts, rows[i].toDomain())
	}
	if err := fillCounts(r.conn(ctx), broadcasts); err != nil {
		return nil, err
	}
	return broadcasts, nil
}

func (r *repository) FindContactForBroadcast(ctx context.Context, organizationID, id uint) (*domain.MailContact, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailContactPO
	if err := r.conn(ctx).Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailContactNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) SetContactStatus(ctx context.Context, organizationID, id uint, status domain.MailContactStatus, reason string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailContactPO{}).
		Where("id = ? AND organization_id = ?", id, organizationID).
		Updates(map[string]any{"status": string(status), "status_reason": reason, "updated_at": gorm.Expr("now()")})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailContactNotFound
	}
	return nil
}

func (r *repository) SuppressContactForMessage(ctx context.Context, messageID uint, reason string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Exec(`UPDATE mail_contacts c SET status = 'suppressed', status_reason = ?, updated_at = now()
		FROM mail_broadcast_recipients r
		JOIN mail_broadcasts b ON b.id = r.broadcast_id
		WHERE r.message_id = ? AND c.id = r.contact_id AND c.organization_id = b.organization_id
		  AND c.status <> 'suppressed'`, reason, messageID).Error
}
