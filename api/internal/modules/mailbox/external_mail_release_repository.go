package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ExternalMailProviderReleasePO is the persistent object of a pending provider release.
type ExternalMailProviderReleasePO struct {
	ID                 uint      `gorm:"primaryKey"`
	ConnectionID       uint      `gorm:"not null;index"`
	OrganizationID     uint      `gorm:"not null"`
	Provider           string    `gorm:"size:16;not null"`
	EncryptedGrant     string    `gorm:"type:text;not null;default:''"`
	StopPush           bool      `gorm:"not null;default:false"`
	StopSubscriptionID string    `gorm:"size:255;not null;default:''"`
	Attempts           int       `gorm:"type:smallint;not null;default:0"`
	NextAttemptAt      time.Time `gorm:"not null;index"`
	ExpiresAt          time.Time `gorm:"not null"`
	CreatedAt          time.Time `gorm:"not null"`
}

func (ExternalMailProviderReleasePO) TableName() string {
	return "external_mail_provider_releases"
}

func (po *ExternalMailProviderReleasePO) toDomain() *domain.ExternalMailProviderRelease {
	return &domain.ExternalMailProviderRelease{
		ID: po.ID, ConnectionID: po.ConnectionID, OrganizationID: po.OrganizationID, Provider: domain.ExternalMailProvider(po.Provider),
		EncryptedGrant: po.EncryptedGrant, StopPush: po.StopPush, StopSubscriptionID: po.StopSubscriptionID,
		Attempts: po.Attempts, NextAttemptAt: po.NextAttemptAt, ExpiresAt: po.ExpiresAt, CreatedAt: po.CreatedAt,
	}
}

var _ domain.ExternalMailReleaseRepository = (*repository)(nil)

func (r *repository) CreateExternalMailProviderRelease(ctx context.Context, release *domain.ExternalMailProviderRelease) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := &ExternalMailProviderReleasePO{
		ConnectionID: release.ConnectionID, OrganizationID: release.OrganizationID, Provider: string(release.Provider), EncryptedGrant: release.EncryptedGrant,
		StopPush: release.StopPush, StopSubscriptionID: release.StopSubscriptionID, NextAttemptAt: release.NextAttemptAt,
		ExpiresAt: release.ExpiresAt, CreatedAt: release.CreatedAt,
	}
	if err := r.conn(ctx).Create(po).Error; err != nil {
		return err
	}
	release.ID = po.ID
	return nil
}

func (r *repository) ClaimExternalMailProviderReleases(ctx context.Context, now, leaseUntil time.Time, limit int) ([]*domain.ExternalMailProviderRelease, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return nil, nil
	}
	var rows []ExternalMailProviderReleasePO
	err := r.conn(ctx).Raw(`UPDATE external_mail_provider_releases SET next_attempt_at = ?
		WHERE id IN (SELECT id FROM external_mail_provider_releases
		  WHERE next_attempt_at <= ? AND expires_at > ? ORDER BY next_attempt_at, id LIMIT ? FOR UPDATE SKIP LOCKED)
		RETURNING *`, leaseUntil, now, now, limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	releases := make([]*domain.ExternalMailProviderRelease, 0, len(rows))
	for i := range rows {
		releases = append(releases, rows[i].toDomain())
	}
	return releases, nil
}

func (r *repository) DeleteExternalMailProviderRelease(ctx context.Context, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Exec(`DELETE FROM external_mail_provider_releases WHERE id = ?`, id).Error
}

func (r *repository) DeferExternalMailProviderRelease(ctx context.Context, id uint, attempts int, nextAttemptAt time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Exec(`UPDATE external_mail_provider_releases SET attempts = ?, next_attempt_at = ? WHERE id = ?`,
		min(attempts, maxConsecutiveFailures), nextAttemptAt, id).Error
}

func (r *repository) DeleteExpiredExternalMailProviderReleases(ctx context.Context, now time.Time, limit int) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	result := r.conn(ctx).Exec(`DELETE FROM external_mail_provider_releases WHERE id IN (
		SELECT id FROM external_mail_provider_releases WHERE expires_at <= ? ORDER BY expires_at, id LIMIT ?
		FOR UPDATE SKIP LOCKED)`, now, limit)
	return int(result.RowsAffected), result.Error
}
