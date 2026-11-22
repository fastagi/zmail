package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// maxConsecutiveFailures keeps the failure count inside its smallint column; the backoff is
// capped long before it.
const maxConsecutiveFailures = 10000

// ExternalMailSyncRunPO is the persistent object of one fetch of one connection.
type ExternalMailSyncRunPO struct {
	ID           uint      `gorm:"primaryKey"`
	ConnectionID uint      `gorm:"not null;index"`
	StartedAt    time.Time `gorm:"not null"`
	FinishedAt   time.Time `gorm:"not null"`
	Outcome      string    `gorm:"size:24;not null"`
	FetchedCount int       `gorm:"not null;default:0"`
	StoredCount  int       `gorm:"not null;default:0"`
	More         bool      `gorm:"not null;default:false"`
	ErrorCode    *string   `gorm:"size:64"`
	TimedOut     bool      `gorm:"not null;default:false"`
	Throttled    bool      `gorm:"not null;default:false"`
	TriggeredBy  string    `gorm:"size:8;not null;default:'poll'"`
}

func (ExternalMailSyncRunPO) TableName() string {
	return "external_mail_sync_runs"
}

func (po *ExternalMailSyncRunPO) toDomain() *domain.ExternalMailSyncRun {
	run := &domain.ExternalMailSyncRun{
		ID: po.ID, ConnectionID: po.ConnectionID, StartedAt: po.StartedAt, FinishedAt: po.FinishedAt,
		Outcome: domain.ExternalMailSyncOutcome(po.Outcome), Fetched: po.FetchedCount, Stored: po.StoredCount, More: po.More,
		TimedOut: po.TimedOut, Throttled: po.Throttled, Trigger: domain.ExternalMailSyncTrigger(po.triggerOrPoll()),
	}
	if po.ErrorCode != nil {
		run.ErrorCode = *po.ErrorCode
	}
	return run
}

// triggerOrPoll reads an empty trigger as a poll.
func (po *ExternalMailSyncRunPO) triggerOrPoll() string {
	if po.TriggeredBy == "" {
		return string(domain.ExternalMailSyncTriggerPoll)
	}
	return po.TriggeredBy
}

var _ domain.ExternalMailOpsRepository = (*repository)(nil)

// recordExternalMailSyncRun stores a run and keeps the connection's latest
// MaxExternalMailSyncRuns. The connection row is share-locked first, so a run of a connection
// deleted meanwhile is skipped instead of failing the pass.
func recordExternalMailSyncRun(tx *gorm.DB, run *domain.ExternalMailSyncRun) error {
	var present []uint
	if err := tx.Raw(`SELECT id FROM external_mail_connections WHERE id = ? FOR KEY SHARE`, run.ConnectionID).
		Scan(&present).Error; err != nil || len(present) == 0 {
		return err
	}
	po := &ExternalMailSyncRunPO{
		ConnectionID: run.ConnectionID, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Outcome: string(run.Outcome), FetchedCount: run.Fetched, StoredCount: run.Stored, More: run.More,
		ErrorCode: optionalText(run.ErrorCode), TimedOut: run.TimedOut, Throttled: run.Throttled,
		TriggeredBy: string(domain.ExternalMailSyncTriggerPoll),
	}
	if run.Trigger == domain.ExternalMailSyncTriggerPush {
		po.TriggeredBy = string(domain.ExternalMailSyncTriggerPush)
	}
	if err := tx.Create(po).Error; err != nil {
		return err
	}
	run.ID = po.ID
	// The connection's runs are read newest first through (connection_id, id DESC); everything
	// past the newest MaxExternalMailSyncRuns goes, at most one row per insert in steady state.
	return tx.Exec(`DELETE FROM external_mail_sync_runs WHERE connection_id = ? AND id < (
		SELECT id FROM external_mail_sync_runs WHERE connection_id = ?
		ORDER BY id DESC OFFSET ? LIMIT 1)`,
		run.ConnectionID, run.ConnectionID, domain.MaxExternalMailSyncRuns-1).Error
}

func (r *repository) ListExternalMailSyncRuns(ctx context.Context, connectionID uint, limit int) ([]*domain.ExternalMailSyncRun, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if limit <= 0 || limit > domain.MaxExternalMailSyncRuns {
		limit = domain.MaxExternalMailSyncRuns
	}
	var rows []ExternalMailSyncRunPO
	if err := r.conn(ctx).Where("connection_id = ?", connectionID).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	runs := make([]*domain.ExternalMailSyncRun, 0, len(rows))
	for i := range rows {
		runs = append(runs, rows[i].toDomain())
	}
	return runs, nil
}

func (r *repository) PruneExternalMailSyncRuns(ctx context.Context, before time.Time, limit int) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	result := r.conn(ctx).Exec(`DELETE FROM external_mail_sync_runs WHERE id IN (
		SELECT id FROM external_mail_sync_runs WHERE started_at < ? ORDER BY started_at, id LIMIT ?
		FOR UPDATE SKIP LOCKED)`, before, limit)
	return int(result.RowsAffected), result.Error
}

// connectedMailboxKind is the SQL condition, on a mailboxes row aliased b, that marks a mailbox
// holding synced copies of an outside account. A purge only ever deletes mail of such a mailbox.
const connectedMailboxKind = "b.origin = 'connected'"

// ErrExternalMailPurgeRefused means a revoked connection points at a mailbox that does not hold
// synced copies; nothing is deleted.
var ErrExternalMailPurgeRefused = errors.New("external mail purge refused: the mailbox does not hold synced copies")

func (r *repository) PurgeRevokedExternalMail(ctx context.Context, step domain.ExternalMailPurgeStep) (*domain.ExternalMailPurge, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	tx := r.conn(ctx)
	query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("status = ?", string(domain.ExternalMailStatusRevoked))
	if step.ConnectionID != 0 {
		query = query.Where("id = ?", step.ConnectionID)
	} else {
		// A connection whose purge failed waits for its retry time, so it never blocks the
		// connections revoked after it.
		query = query.Where("revoked_at <= ? AND (purge_retry_at IS NULL OR purge_retry_at <= ?)", step.RevokedBefore, step.Now).
			// A mailbox whose owner left awaits its handover, which seals it; its copies are kept for
			// the successor or a returning owner, whatever the retention.
			Where(`NOT EXISTS (SELECT 1 FROM mailboxes AS left_box WHERE left_box.id = external_mail_connections.mailbox_id
				AND left_box.owner_left_at IS NOT NULL AND left_box.deleted_at IS NULL)`).
			Order("revoked_at, id")
	}
	// Find, not Take: no due connection is the usual case and must not be logged as an error.
	var rows []ExternalMailConnectionPO
	if err := query.Limit(1).Find(&rows).Error; err != nil || len(rows) == 0 {
		return nil, err
	}
	purge := &domain.ExternalMailPurge{Connection: rows[0].toDomain()}
	mailboxID := purge.Connection.MailboxID
	// The mailbox id is set by binding and unique to the connection; the kind is checked as well,
	// so a corrupt row can never point a purge at mail Zmail itself received or sent.
	var foreign []uint
	if err := tx.Raw(`SELECT b.id FROM mailboxes b WHERE b.id = ? AND NOT (`+connectedMailboxKind+`)`, mailboxID).
		Scan(&foreign).Error; err != nil {
		return purge, err
	}
	if len(foreign) > 0 {
		return purge, ErrExternalMailPurgeRefused
	}
	var found []uint
	if err := tx.Raw(`SELECT m.id FROM mail_messages m JOIN mailboxes b ON b.id = m.mailbox_id AND `+connectedMailboxKind+`
		WHERE m.mailbox_id = ? ORDER BY m.id LIMIT ? FOR UPDATE OF m SKIP LOCKED`,
		mailboxID, max(step.Messages, 1)).Scan(&found).Error; err != nil {
		return purge, err
	}
	deleted, err := deleteMessages(tx, found)
	if err != nil {
		return purge, err
	}
	var left []uint
	if err := tx.Raw(`SELECT id FROM mail_messages WHERE mailbox_id = ? LIMIT 1`, mailboxID).Scan(&left).Error; err != nil {
		return purge, err
	}
	purge.DeletedMessages, purge.Completed = deleted, len(left) == 0
	return purge, nil
}

func (r *repository) DeferExternalMailPurge(ctx context.Context, id uint, retryAt time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&ExternalMailConnectionPO{}).
		Where("id = ? AND status = ?", id, string(domain.ExternalMailStatusRevoked)).
		Update("purge_retry_at", retryAt).Error
}

func (r *repository) RenewExternalMailLease(ctx context.Context, id uint, heldUntil, until time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&ExternalMailConnectionPO{}).
		Where("id = ? AND status = ? AND next_sync_at = ?", id, string(domain.ExternalMailStatusActive), heldUntil).
		Update("next_sync_at", until)
	return result.RowsAffected == 1, result.Error
}

func (r *repository) DeleteExternalMailConnection(ctx context.Context, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("connection_id = ?", id).Delete(&ExternalMailSyncRunPO{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&ExternalMailConnectionPO{}).Error
	})
}

func (r *repository) ExpediteExternalMailPurge(ctx context.Context, id uint, revokedAt time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&ExternalMailConnectionPO{}).
		Where("id = ? AND status = ?", id, string(domain.ExternalMailStatusRevoked)).
		Update("revoked_at", revokedAt)
	return result.RowsAffected > 0, result.Error
}
