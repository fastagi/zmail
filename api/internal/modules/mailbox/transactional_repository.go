package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.TransactionalMailRepository = (*repository)(nil)

const (
	transactionalLogDefaultPerPage = 50
	transactionalLogMaxPerPage     = 100
)

// leaseSeconds is the lease length in seconds. An attempt started inside a lease has a
// send_attempted_at at or after lease_until minus it; the lease and the attempt may start at the
// same instant.
var leaseSeconds = int(domain.TransactionalLease / time.Second)

// terminalColumns are the columns every terminal transition clears: the payload, the queue
// fields, and the lease.
func terminalColumns(status domain.TransactionalStatus, failureCode string, now time.Time) map[string]any {
	return map[string]any{
		"status":             string(status),
		"failure_code":       failureCode,
		"payload_ciphertext": nil,
		"queued_reason":      nil,
		"next_attempt_at":    nil,
		"lease_until":        nil,
		"updated_at":         now,
	}
}

func (r *repository) CreateTransactional(ctx context.Context, message *domain.TransactionalMessage) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	po, err := transactionalMessageToPO(message)
	if err != nil {
		return false, err
	}
	result := r.conn(ctx).Clauses(clause.OnConflict{
		Columns:     []clause.Column{{Name: "organization_id"}, {Name: "mode"}, {Name: "idempotency_key"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "idempotency_key IS NOT NULL"}}},
		DoNothing:   true,
	}).Create(po)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	stored := po.toDomain()
	stored.Events = message.Events
	*message = *stored
	return true, nil
}

func (r *repository) FindTransactionalByIdempotencyKey(
	ctx context.Context,
	organizationID uint,
	mode domain.APIKeyMode,
	key string,
) (*domain.TransactionalMessage, error) {
	return r.findTransactional(ctx, "organization_id = ? AND mode = ? AND idempotency_key = ?", organizationID, string(mode), key)
}

func (r *repository) ReleaseIdempotencyKey(ctx context.Context, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&TransactionalMessagePO{}).Where("id = ?", id).
		Updates(map[string]any{"idempotency_key": nil, "updated_at": time.Now().UTC()}).Error
}

func (r *repository) FindTransactional(ctx context.Context, organizationID, id uint) (*domain.TransactionalMessage, error) {
	return r.findTransactional(ctx, "organization_id = ? AND id = ?", organizationID, id)
}

func (r *repository) FindTransactionalByID(ctx context.Context, id uint) (*domain.TransactionalMessage, error) {
	return r.findTransactional(ctx, "id = ?", id)
}

func (r *repository) OldestQueuedTransactional(ctx context.Context) (*time.Time, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var oldest *time.Time
	err := r.conn(ctx).Model(&TransactionalMessagePO{}).
		Where("status = ? AND mode = ? AND NOT simulated AND (queued_reason IS NULL OR queued_reason <> ?)",
			string(domain.TransactionalStatusQueued), string(domain.APIKeyModeLive), string(domain.TransactionalQueuedSendLimit)).
		Select("min(created_at)").Scan(&oldest).Error
	return oldest, err
}

func (r *repository) FindTransactionalByProviderID(ctx context.Context, providerMessageID string) (*domain.TransactionalMessage, error) {
	return r.findTransactional(ctx, "provider_message_id = ?", providerMessageID)
}

func (r *repository) findTransactional(ctx context.Context, query string, arguments ...any) (*domain.TransactionalMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po TransactionalMessagePO
	if err := r.conn(ctx).Where(query, arguments...).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTransactionalMessageNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

// CountTransactionalAttemptsSince counts provider calls; only live, real messages ever record one.
func (r *repository) CountTransactionalAttemptsSince(ctx context.Context, organizationID uint, since time.Time) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := transactionalAttempts(r.conn(ctx), organizationID, since).Count(&count).Error
	return count, err
}

func transactionalAttempts(db *gorm.DB, organizationID uint, since time.Time) *gorm.DB {
	return db.Model(&TransactionalMessagePO{}).
		Where("organization_id = ? AND send_attempted_at IS NOT NULL AND send_attempted_at >= ?", organizationID, since)
}

func (r *repository) CountTransactionalWaiting(ctx context.Context, organizationID uint) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&TransactionalMessagePO{}).
		Where("organization_id = ? AND status = ? AND queued_reason = ?",
			organizationID, string(domain.TransactionalStatusQueued), string(domain.TransactionalQueuedSendLimit)).
		Count(&count).Error
	return count, err
}

// LeaseTransactional works out the lane room under the organization's advisory lock, so two
// dispatchers never both take the last room, and locks the candidate rows with SKIP LOCKED, so a
// row another transaction holds is passed over instead of waited for.
func (r *repository) LeaseTransactional(
	ctx context.Context,
	organizationID uint,
	ids []uint,
	now, leaseUntil, windowStart time.Time,
	laneLimit, limit int,
) ([]*domain.TransactionalMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return nil, nil
	}
	now, leaseUntil = pgTime(now), pgTime(leaseUntil)
	var leased []TransactionalMessagePO
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('zmail.transactional_send'), ?::int)`,
			organizationID).Error; err != nil {
			return err
		}
		var attempts, held int64
		if err := transactionalAttempts(tx, organizationID, windowStart).Count(&attempts).Error; err != nil {
			return err
		}
		// A held lease takes room until its attempt starts; from then on the attempt counts instead.
		if err := tx.Model(&TransactionalMessagePO{}).
			Where(`organization_id = ? AND status = ? AND mode = ? AND NOT simulated AND lease_until > ?
			  AND (send_attempted_at IS NULL OR send_attempted_at < lease_until - ? * interval '1 second')`,
				organizationID, string(domain.TransactionalStatusQueued), string(domain.APIKeyModeLive), now, leaseSeconds).
			Count(&held).Error; err != nil {
			return err
		}
		room := laneLimit - int(attempts) - int(held)

		query := tx.Where(`organization_id = ? AND status = ? AND next_attempt_at <= ? AND (lease_until IS NULL OR lease_until <= ?)`,
			organizationID, string(domain.TransactionalStatusQueued), now, now)
		if len(ids) > 0 {
			query = query.Where("id IN ?", ids)
		}
		var candidates []TransactionalMessagePO
		if err := query.Order("next_attempt_at, id").Limit(limit).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Find(&candidates).Error; err != nil {
			return err
		}
		var leaseIDs, waitIDs []uint
		for i := range candidates {
			candidate := &candidates[i]
			countsInLane := candidate.Mode == string(domain.APIKeyModeLive) && !candidate.Simulated
			if countsInLane && room <= 0 {
				waitIDs = append(waitIDs, candidate.ID)
				continue
			}
			if countsInLane {
				room--
			}
			candidate.LeaseUntil = &leaseUntil
			candidate.UpdatedAt = now
			leaseIDs = append(leaseIDs, candidate.ID)
			leased = append(leased, *candidate)
		}
		if len(waitIDs) > 0 {
			if err := tx.Model(&TransactionalMessagePO{}).Where("id IN ?", waitIDs).
				Updates(map[string]any{"queued_reason": string(domain.TransactionalQueuedSendLimit), "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if len(leaseIDs) == 0 {
			return nil
		}
		return tx.Model(&TransactionalMessagePO{}).Where("id IN ?", leaseIDs).
			Updates(map[string]any{"lease_until": leaseUntil, "updated_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	messages := make([]*domain.TransactionalMessage, 0, len(leased))
	for i := range leased {
		messages = append(messages, leased[i].toDomain())
	}
	return messages, nil
}

func (r *repository) TransactionalDueOrganizations(ctx context.Context, now time.Time, limit int) ([]uint, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var organizations []uint
	err := r.conn(ctx).Model(&TransactionalMessagePO{}).
		Where(`status = ? AND next_attempt_at <= ? AND (lease_until IS NULL OR lease_until <= ?)`,
			string(domain.TransactionalStatusQueued), now, now).
		Distinct().Order("organization_id").Limit(limit).
		Pluck("organization_id", &organizations).Error
	return organizations, err
}

// casTransactional updates the message only while it is queued and leased to the caller, so the
// holder of a lease that ran out cannot overwrite what the next holder does.
func (r *repository) casTransactional(ctx context.Context, id uint, lease time.Time, values map[string]any) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&TransactionalMessagePO{}).
		Where("id = ? AND status = ? AND lease_until = ?", id, string(domain.TransactionalStatusQueued), pgTime(lease)).
		Updates(values)
	return result.RowsAffected == 1, result.Error
}

func (r *repository) StartTransactionalAttempt(ctx context.Context, id uint, lease, at time.Time) (bool, error) {
	return r.casTransactional(ctx, id, lease, map[string]any{
		"send_attempted_at": pgTime(at),
		"attempts":          gorm.Expr("attempts + 1"),
		"queued_reason":     nil,
		"updated_at":        time.Now().UTC(),
	})
}

func (r *repository) FinishTransactional(ctx context.Context, id uint, lease time.Time, outcome domain.TransactionalOutcome) (bool, error) {
	if !outcome.Status.Terminal() {
		return false, ErrMailMessageInvalid
	}
	values := terminalColumns(outcome.Status, outcome.FailureCode, time.Now().UTC())
	if outcome.ProviderMessageID != "" {
		values["provider_message_id"] = outcome.ProviderMessageID
	}
	if outcome.SentAt != nil {
		values["sent_at"] = pgTime(*outcome.SentAt)
	}
	return r.casTransactional(ctx, id, lease, values)
}

func (r *repository) ScheduleTransactionalRetry(ctx context.Context, id uint, lease, next time.Time, failureCode string) (bool, error) {
	return r.casTransactional(ctx, id, lease, map[string]any{
		"queued_reason":   string(domain.TransactionalQueuedRetry),
		"next_attempt_at": pgTime(next),
		"lease_until":     nil,
		"failure_code":    failureCode,
		"updated_at":      time.Now().UTC(),
	})
}

// ExpireTransactional leaves alone a message whose provider call started inside a lease that
// ran out: the provider may have accepted it, and FailUnknownTransactional reports it as such.
func (r *repository) ExpireTransactional(ctx context.Context, now time.Time, limit int) ([]*domain.TransactionalMessage, error) {
	now = pgTime(now)
	return r.failQueued(ctx, now, limit, domain.TransactionalFailureExpired,
		`created_at < ? AND (lease_until IS NULL OR lease_until <= ?)
		  AND NOT (lease_until IS NOT NULL AND send_attempted_at IS NOT NULL
		           AND send_attempted_at >= lease_until - ? * interval '1 second')`,
		now.Add(-domain.TransactionalQueueTimeout), now, leaseSeconds)
}

func (r *repository) FailUnknownTransactional(ctx context.Context, now time.Time, limit int) ([]*domain.TransactionalMessage, error) {
	now = pgTime(now)
	return r.failQueued(ctx, now, limit, domain.TransactionalFailureUnknownOutcome,
		`lease_until <= ? AND send_attempted_at >= lease_until - ? * interval '1 second'`,
		now, leaseSeconds)
}

// failQueued fails at most limit queued messages matching the condition, oldest first, skipping
// rows another transaction holds, and returns them as stored.
func (r *repository) failQueued(
	ctx context.Context,
	now time.Time,
	limit int,
	failureCode, condition string,
	arguments ...any,
) ([]*domain.TransactionalMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return nil, nil
	}
	var rows []TransactionalMessagePO
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []uint
		if err := tx.Model(&TransactionalMessagePO{}).
			Where("status = ?", string(domain.TransactionalStatusQueued)).
			Where(condition, arguments...).
			Order("id").Limit(limit).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Model(&TransactionalMessagePO{}).Where("id IN ?", ids).
			Updates(terminalColumns(domain.TransactionalStatusFailed, failureCode, now)).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Order("id").Find(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	messages := make([]*domain.TransactionalMessage, 0, len(rows))
	for i := range rows {
		messages = append(messages, rows[i].toDomain())
	}
	return messages, nil
}

func (r *repository) PurgeTransactional(ctx context.Context, before time.Time, limit int) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	// Events go with their messages (ON DELETE CASCADE).
	result := r.conn(ctx).Exec(`DELETE FROM transactional_messages WHERE id IN (
		SELECT id FROM transactional_messages WHERE created_at < ? ORDER BY id LIMIT ?)`, before, limit)
	return int(result.RowsAffected), result.Error
}

// AddTransactionalEvents locks the message row so concurrent provider events for one message are
// applied one after another, stores the new events, and recomputes the summary from the full
// history, as AddMessageEvents does for mailbox messages.
func (r *repository) AddTransactionalEvents(
	ctx context.Context,
	id uint,
	events []domain.MailEvent,
) (int, domain.MailEventType, error) {
	if r.db == nil {
		return 0, "", domain.ErrServiceUnavailable
	}
	added := 0
	var summary domain.MailEventType
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var locked TransactionalMessagePO
		lock := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "to_address").Where("id = ?", id).First(&locked)
		if lock.Error != nil {
			if errors.Is(lock.Error, gorm.ErrRecordNotFound) {
				return ErrTransactionalMessageNotFound
			}
			return lock.Error
		}
		for _, event := range events {
			po := &TransactionalMessageEventPO{
				MessageID:  id,
				Type:       string(event.Type),
				Detail:     event.Detail,
				OccurredAt: event.OccurredAt,
			}
			query := tx
			if event.ProviderEventID != "" {
				providerEventID := event.ProviderEventID
				po.ProviderEventID = &providerEventID
				query = tx.Clauses(clause.OnConflict{
					Columns:     []clause.Column{{Name: "provider_event_id"}},
					TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "provider_event_id IS NOT NULL"}}},
					DoNothing:   true,
				})
			}
			result := query.Create(po)
			if result.Error != nil {
				return result.Error
			}
			added += int(result.RowsAffected)
		}

		var rows []TransactionalMessageEventPO
		if err := tx.Where("message_id = ?", id).Order("occurred_at ASC, id ASC").Find(&rows).Error; err != nil {
			return err
		}
		history := make([]domain.MailEvent, 0, len(rows))
		for i := range rows {
			event := rows[i].toDomain()
			// The single recipient's events; acceptance stays message-level, as for mailbox messages.
			if event.Type != domain.MailEventAccepted {
				event.Recipient = locked.ToAddress
			}
			history = append(history, event)
		}
		summary = domain.SummarizeMailEvents(history)
		var lastEvent any
		if summary != "" {
			lastEvent = string(summary)
		}
		return tx.Model(&TransactionalMessagePO{}).Where("id = ?", id).UpdateColumn("last_event", lastEvent).Error
	})
	if err != nil {
		return 0, "", err
	}
	return added, summary, nil
}

func (r *repository) ListTransactionalEvents(ctx context.Context, id uint, limit int) ([]domain.MailEvent, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []TransactionalMessageEventPO
	err := r.conn(ctx).Where("message_id = ?", id).Order("occurred_at ASC, id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	events := make([]domain.MailEvent, 0, len(rows))
	for i := range rows {
		events = append(events, rows[i].toDomain())
	}
	return events, nil
}

// problemEvents are the delivery events the "problem" outcome selects.
var problemEvents = []string{
	string(domain.MailEventBounced), string(domain.MailEventComplained),
	string(domain.MailEventFailed), string(domain.MailEventRejected),
}

func (r *repository) ListTransactionalLog(
	ctx context.Context,
	organizationID uint,
	filter domain.TransactionalLogFilter,
) ([]*domain.TransactionalMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	perPage := filter.PerPage
	if perPage < 1 {
		perPage = transactionalLogDefaultPerPage
	}
	perPage = min(perPage, transactionalLogMaxPerPage)

	query := r.conn(ctx).Model(&TransactionalMessagePO{}).Where("organization_id = ?", organizationID)
	if filter.Mode != "" {
		query = query.Where("mode = ?", string(filter.Mode))
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.LastEvent != "" {
		query = query.Where("last_event = ?", filter.LastEvent)
	}
	switch filter.Outcome {
	case domain.TransactionalOutcomeDelivered:
		query = query.Where("last_event = ?", string(domain.MailEventDelivered))
	case domain.TransactionalOutcomeProblem:
		query = query.Where("(last_event IN ? OR status IN ?)", problemEvents,
			[]string{string(domain.TransactionalStatusFailed), string(domain.TransactionalStatusSuppressed)})
	}
	if filter.TemplateKey != "" {
		query = query.Where("template_key = ?", filter.TemplateKey)
	}
	if to := normalizeEmail(filter.To); to != "" {
		query = query.Where("to_address = ?", to)
	}
	if !filter.Since.IsZero() {
		query = query.Where("created_at >= ?", filter.Since)
	}
	if !filter.Until.IsZero() {
		query = query.Where("created_at < ?", filter.Until)
	}
	if filter.Cursor != 0 {
		query = query.Where("id < ?", filter.Cursor)
	}
	var rows []TransactionalMessagePO
	// The log never needs the payload.
	if err := query.Omit("payload_ciphertext").Order("id DESC").Limit(perPage + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	messages := make([]*domain.TransactionalMessage, 0, len(rows))
	for i := range rows {
		messages = append(messages, rows[i].toDomain())
	}
	return messages, nil
}

func (r *repository) CountTransactionalSince(
	ctx context.Context,
	organizationID uint,
	mode domain.APIKeyMode,
	templateKey string,
	since time.Time,
	limit int,
) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	inner := r.conn(ctx).Model(&TransactionalMessagePO{}).Select("1").
		Where("organization_id = ? AND mode = ? AND created_at >= ?", organizationID, string(mode), since)
	if templateKey != "" {
		inner = inner.Where("template_key = ?", templateKey)
	}
	var count int64
	err := r.conn(ctx).Table("(?) AS capped", inner.Limit(limit)).Count(&count).Error
	return count, err
}
