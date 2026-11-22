package user

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// totpFactor is a user's authenticator factor; Secret is the encrypted base32 secret.
type totpFactor struct {
	UserID       uint
	Secret       string
	ConfirmedAt  *time.Time
	LastUsedStep int64
}

type totpFactorPO struct {
	UserID       uint `gorm:"primaryKey"`
	Secret       string
	ConfirmedAt  *time.Time
	LastUsedStep int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (totpFactorPO) TableName() string { return "user_totp_factors" }

type recoveryCodePO struct {
	ID        uint `gorm:"primaryKey"`
	UserID    uint
	CodeHash  string
	UsedAt    *time.Time
	CreatedAt time.Time
}

func (recoveryCodePO) TableName() string { return "user_recovery_codes" }

func (r *repository) FindTOTPFactor(ctx context.Context, userID uint) (*totpFactor, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var row totpFactorPO
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &totpFactor{UserID: row.UserID, Secret: row.Secret, ConfirmedAt: row.ConfirmedAt, LastUsedStep: row.LastUsedStep}, nil
}

// SavePendingTOTPFactor stores a new unconfirmed secret, replacing a pending one; it refuses to
// touch a confirmed factor.
func (r *repository) SavePendingTOTPFactor(ctx context.Context, userID uint, secret string, now time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{"secret": secret, "last_used_step": 0, "updated_at": now}),
		Where:     clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "user_totp_factors.confirmed_at IS NULL"}}},
	}).Create(&totpFactorPO{UserID: userID, Secret: secret, CreatedAt: now, UpdatedAt: now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrTOTPAlreadyEnabled
	}
	return nil
}

// ConfirmTOTPFactor confirms a pending factor at step and replaces the recovery codes, in one
// transaction; ErrTOTPInvalid when the factor changed or the step was used meanwhile.
func (r *repository) ConfirmTOTPFactor(ctx context.Context, userID uint, secret string, step int64, codeHashes []string, now time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&totpFactorPO{}).
			Where("user_id = ? AND secret = ? AND confirmed_at IS NULL AND last_used_step < ?", userID, secret, step).
			Updates(map[string]any{"confirmed_at": now, "last_used_step": step, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return domain.ErrTOTPInvalid
		}
		if err := tx.Where("user_id = ?", userID).Delete(&recoveryCodePO{}).Error; err != nil {
			return err
		}
		rows := make([]recoveryCodePO, 0, len(codeHashes))
		for _, hash := range codeHashes {
			rows = append(rows, recoveryCodePO{UserID: userID, CodeHash: hash, CreatedAt: now})
		}
		return tx.Create(&rows).Error
	})
}

// UseTOTPStep records a confirmed factor's step; false when that step or a later one was used.
func (r *repository) UseTOTPStep(ctx context.Context, userID uint, step int64, now time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.db.WithContext(ctx).Model(&totpFactorPO{}).
		Where("user_id = ? AND confirmed_at IS NOT NULL AND last_used_step < ?", userID, step).
		Updates(map[string]any{"last_used_step": step, "updated_at": now})
	return result.RowsAffected == 1, result.Error
}

// UseRecoveryCode consumes an unused recovery code; false when there is none with that hash.
func (r *repository) UseRecoveryCode(ctx context.Context, userID uint, codeHash string, now time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.db.WithContext(ctx).Model(&recoveryCodePO{}).
		Where("user_id = ? AND code_hash = ? AND used_at IS NULL", userID, codeHash).
		Update("used_at", now)
	return result.RowsAffected == 1, result.Error
}

func (r *repository) CountRecoveryCodes(ctx context.Context, userID uint) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&recoveryCodePO{}).Where("user_id = ? AND used_at IS NULL", userID).Count(&count).Error
	return count, err
}

// DeleteTOTPFactor removes the factor and its recovery codes.
func (r *repository) DeleteTOTPFactor(ctx context.Context, userID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&recoveryCodePO{}).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(&totpFactorPO{}).Error
	})
}
