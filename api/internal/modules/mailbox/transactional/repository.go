package transactional

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/database"
)

const (
	logDefaultPerPage = 50
	logMaxPerPage     = 100
)

var leaseSeconds = int(domain.TransactionalLease / time.Second)

const (
	lostAttempt    = `(lease_until IS NOT NULL AND send_attempted_at IS NOT NULL AND send_attempted_at >= lease_until - ? * interval '1 second')`
	notLostAttempt = `NOT ` + lostAttempt
)

var problemEvents = []string{
	string(domain.MailEventBounced), string(domain.MailEventComplained),
	string(domain.MailEventFailed), string(domain.MailEventRejected),
}

// Repository implements the transactional message persistence subset of domain.TransactionalMailRepository.
type Repository struct {
	db          *gorm.DB
	errNotFound error
	errInvalid  error
}

// NewRepository builds a transactional persistence repository.
func NewRepository(db *gorm.DB, errNotFound, errInvalid error) *Repository {
	return &Repository{db: db, errNotFound: errNotFound, errInvalid: errInvalid}
}

func (r *Repository) conn(ctx context.Context) *gorm.DB {
	return database.ResolveContextDB(ctx, r.db).WithContext(ctx)
}

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

func (r *Repository) CreateTransactional(ctx context.Context, message *domain.TransactionalMessage) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	po, err := toMessagePO(message)
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

func (r *Repository) FindTransactionalByIdempotencyKey(ctx context.Context, organizationID uint, mode domain.APIKeyMode, key string) (*domain.TransactionalMessage, error) {
	return r.findTransactional(ctx, "organization_id = ? AND mode = ? AND idempotency_key = ?", organizationID, string(mode), key)
}

func (r *Repository) ReleaseIdempotencyKey(ctx context.Context, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&TransactionalMessagePO{}).Where("id = ?", id).
		Updates(map[string]any{"idempotency_key": nil, "updated_at": time.Now().UTC()}).Error
}

func (r *Repository) FindTransactional(ctx context.Context, organizationID, id uint) (*domain.TransactionalMessage, error) {
	return r.findTransactional(ctx, "organization_id = ? AND id = ?", organizationID, id)
}

func (r *Repository) FindTransactionalByID(ctx context.Context, id uint) (*domain.TransactionalMessage, error) {
	return r.findTransactional(ctx, "id = ?", id)
}

func (r *Repository) OldestQueuedTransactional(ctx context.Context) (*time.Time, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var oldest sql.NullTime
	err := r.conn(ctx).Model(&TransactionalMessagePO{}).
		Where("status = ? AND mode = ? AND NOT simulated AND (queued_reason IS NULL OR queued_reason <> ?)",
			string(domain.TransactionalStatusQueued), string(domain.APIKeyModeLive), string(domain.TransactionalQueuedSendLimit)).
		Select("min(created_at)").Scan(&oldest).Error
	if err != nil || !oldest.Valid {
		return nil, err
	}
	return &oldest.Time, nil
}

func (r *Repository) FindTransactionalByProviderID(ctx context.Context, providerMessageID string) (*domain.TransactionalMessage, error) {
	return r.findTransactional(ctx, "provider_message_id = ?", providerMessageID)
}

func (r *Repository) findTransactional(ctx context.Context, query string, arguments ...any) (*domain.TransactionalMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po TransactionalMessagePO
	if err := r.conn(ctx).Where(query, arguments...).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, r.errNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *Repository) CountTransactionalAttemptsSince(ctx context.Context, organizationID uint, since time.Time) (int64, error) {
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

func (r *Repository) CountTransactionalWaiting(ctx context.Context, organizationID uint) (int64, error) {
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

func (r *Repository) LeaseTransactional(
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
	now, leaseUntil = PGTime(now), PGTime(leaseUntil)
	var leased []TransactionalMessagePO
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		lockKey := int32(organizationID) //nolint:gosec // lock key only needs to be stable per organization
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('zmail.transactional_send'), ?)`, lockKey).Error; err != nil {
			return err
		}
		var attempts, held int64
		if err := transactionalAttempts(tx, organizationID, windowStart).Count(&attempts).Error; err != nil {
			return err
		}
		if err := tx.Model(&TransactionalMessagePO{}).
			Where(`organization_id = ? AND status = ? AND mode = ? AND NOT simulated AND lease_until > ?
			  AND (send_attempted_at IS NULL OR send_attempted_at < lease_until - ? * interval '1 second')`,
				organizationID, string(domain.TransactionalStatusQueued), string(domain.APIKeyModeLive), now, leaseSeconds).
			Count(&held).Error; err != nil {
			return err
		}
		room := laneLimit - int(attempts) - int(held)

		var candidates []TransactionalMessagePO
		for _, lane := range []string{
			`(mode = 'test' OR simulated)`,
			`(mode = 'live' AND NOT simulated)`,
		} {
			query := tx.Where(`organization_id = ? AND status = ? AND next_attempt_at <= ? AND (lease_until IS NULL OR lease_until <= ?)`,
				organizationID, string(domain.TransactionalStatusQueued), now, now).
				Where(notLostAttempt, leaseSeconds).
				Where(lane)
			if len(ids) > 0 {
				query = query.Where("id IN ?", ids)
			}
			var found []TransactionalMessagePO
			if err := query.Order("next_attempt_at, id").Limit(limit).
				Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
				Find(&found).Error; err != nil {
				return err
			}
			candidates = append(candidates, found...)
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			a, b := candidates[i], candidates[j]
			if !a.NextAttemptAt.Equal(*b.NextAttemptAt) {
				return a.NextAttemptAt.Before(*b.NextAttemptAt)
			}
			return a.ID < b.ID
		})
		var leaseIDs []uint
		for i := range candidates {
			candidate := &candidates[i]
			countsInLane := candidate.Mode == string(domain.APIKeyModeLive) && !candidate.Simulated
			if countsInLane && room <= 0 {
				continue
			}
			if len(leased) >= limit {
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
		if len(leaseIDs) > 0 {
			if err := tx.Model(&TransactionalMessagePO{}).Where("id IN ?", leaseIDs).
				Updates(map[string]any{"lease_until": leaseUntil, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if room > 0 {
			return nil
		}
		waiting := tx.Model(&TransactionalMessagePO{}).Select("id").
			Where(`organization_id = ? AND status = ? AND mode = ? AND NOT simulated AND next_attempt_at <= ?
			  AND (lease_until IS NULL OR lease_until <= ?)`,
				organizationID, string(domain.TransactionalStatusQueued), string(domain.APIKeyModeLive), now, now).
			Where(notLostAttempt, leaseSeconds).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		return tx.Model(&TransactionalMessagePO{}).Where("id IN (?)", waiting).
			Updates(map[string]any{
				"queued_reason":   string(domain.TransactionalQueuedSendLimit),
				"next_attempt_at": now.Add(domain.TransactionalLaneWait),
				"updated_at":      now,
			}).Error
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

func (r *Repository) TransactionalDueOrganizations(ctx context.Context, now time.Time, limit int) ([]uint, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var organizations []uint
	err := r.conn(ctx).Model(&TransactionalMessagePO{}).
		Where(`status = ? AND next_attempt_at <= ? AND (lease_until IS NULL OR lease_until <= ?)`,
			string(domain.TransactionalStatusQueued), now, now).
		Where(notLostAttempt, leaseSeconds).
		Group("organization_id").Order("min(next_attempt_at), organization_id").Limit(limit).
		Pluck("organization_id", &organizations).Error
	return organizations, err
}

func (r *Repository) casTransactional(ctx context.Context, id uint, lease time.Time, values map[string]any) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&TransactionalMessagePO{}).
		Where("id = ? AND status = ? AND lease_until = ?", id, string(domain.TransactionalStatusQueued), PGTime(lease)).
		Updates(values)
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) StartTransactionalAttempt(ctx context.Context, id uint, lease, at time.Time) (bool, error) {
	return r.casTransactional(ctx, id, lease, map[string]any{
		"send_attempted_at": PGTime(at),
		"attempts":          gorm.Expr("attempts + 1"),
		"queued_reason":     nil,
		"updated_at":        time.Now().UTC(),
	})
}

func (r *Repository) FinishTransactional(ctx context.Context, id uint, lease time.Time, outcome domain.TransactionalOutcome) (bool, error) {
	if !outcome.Status.Terminal() {
		return false, r.errInvalid
	}
	values := terminalColumns(outcome.Status, outcome.FailureCode, time.Now().UTC())
	if outcome.ProviderMessageID != "" {
		values["provider_message_id"] = outcome.ProviderMessageID
	}
	if outcome.SentAt != nil {
		values["sent_at"] = PGTime(*outcome.SentAt)
	}
	return r.casTransactional(ctx, id, lease, values)
}

func (r *Repository) ScheduleTransactionalRetry(ctx context.Context, id uint, lease, next time.Time, failureCode string) (bool, error) {
	return r.casTransactional(ctx, id, lease, map[string]any{
		"queued_reason":   string(domain.TransactionalQueuedRetry),
		"next_attempt_at": PGTime(next),
		"lease_until":     nil,
		"failure_code":    failureCode,
		"updated_at":      time.Now().UTC(),
	})
}

func (r *Repository) ExpireTransactional(ctx context.Context, now time.Time, limit int) ([]*domain.TransactionalMessage, error) {
	now = PGTime(now)
	return r.failQueued(ctx, now, limit, domain.TransactionalFailureExpired,
		`created_at < ? AND (lease_until IS NULL OR lease_until <= ?)
		  AND `+notLostAttempt,
		now.Add(-domain.TransactionalQueueTimeout), now, leaseSeconds)
}

func (r *Repository) FailUnknownTransactional(ctx context.Context, now time.Time, limit int) ([]*domain.TransactionalMessage, error) {
	now = PGTime(now)
	return r.failQueued(ctx, now, limit, domain.TransactionalFailureUnknownOutcome,
		`lease_until <= ? AND `+lostAttempt, now, leaseSeconds)
}

func (r *Repository) failQueued(
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

func (r *Repository) PurgeTransactional(ctx context.Context, before time.Time, limit int) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	result := r.conn(ctx).Exec(`DELETE FROM transactional_messages WHERE id IN (
		SELECT id FROM transactional_messages WHERE created_at < ? ORDER BY id LIMIT ?)`, before, limit)
	return int(result.RowsAffected), result.Error
}

func (r *Repository) AddTransactionalEvents(ctx context.Context, id uint, events []domain.MailEvent) (int, domain.MailEventType, error) {
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
				return r.errNotFound
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

func (r *Repository) ListTransactionalEvents(ctx context.Context, id uint, limit int) ([]domain.MailEvent, error) {
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

func (r *Repository) ListTransactionalLog(ctx context.Context, organizationID uint, filter domain.TransactionalLogFilter) ([]*domain.TransactionalMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	perPage := filter.PerPage
	if perPage < 1 {
		perPage = logDefaultPerPage
	}
	perPage = min(perPage, logMaxPerPage)

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
	if to := NormalizeEmail(filter.To); to != "" {
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
	if err := query.Omit("payload_ciphertext").Order("id DESC").Limit(perPage + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	messages := make([]*domain.TransactionalMessage, 0, len(rows))
	for i := range rows {
		messages = append(messages, rows[i].toDomain())
	}
	return messages, nil
}

func (r *Repository) CountTransactionalSince(ctx context.Context, organizationID uint, mode domain.APIKeyMode, templateKey string, since time.Time, limit int) (int64, error) {
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
