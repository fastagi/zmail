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
}

func (ExternalMailSyncRunPO) TableName() string {
	return "external_mail_sync_runs"
}

func (po *ExternalMailSyncRunPO) toDomain() *domain.ExternalMailSyncRun {
	run := &domain.ExternalMailSyncRun{
		ID: po.ID, ConnectionID: po.ConnectionID, StartedAt: po.StartedAt, FinishedAt: po.FinishedAt,
		Outcome: domain.ExternalMailSyncOutcome(po.Outcome), Fetched: po.FetchedCount, Stored: po.StoredCount, More: po.More,
	}
	if po.ErrorCode != nil {
		run.ErrorCode = *po.ErrorCode
	}
	return run
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
		ErrorCode: optionalText(run.ErrorCode),
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
		query = query.Where("revoked_at <= ?", step.RevokedBefore).Order("revoked_at, id")
	}
	var po ExternalMailConnectionPO
	err := query.Limit(1).Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Only the connection's own mailbox is ever touched: its id is unique to the connection.
	var found []uint
	if lockErr := tx.Raw(`SELECT id FROM mail_messages WHERE mailbox_id = ? ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED`,
		po.MailboxID, max(step.Messages, 1)).Scan(&found).Error; lockErr != nil {
		return nil, lockErr
	}
	deleted, err := deleteMessages(tx, found)
	if err != nil {
		return nil, err
	}
	var left []uint
	if err := tx.Raw(`SELECT id FROM mail_messages WHERE mailbox_id = ? LIMIT 1`, po.MailboxID).Scan(&left).Error; err != nil {
		return nil, err
	}
	return &domain.ExternalMailPurge{Connection: po.toDomain(), DeletedMessages: deleted, Completed: len(left) == 0}, nil
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
