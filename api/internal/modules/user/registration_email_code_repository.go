package user

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
	infradatabase "github.com/fastagi/zmail/api/internal/infra/database"
)

// RegistrationEmailCodePO is one mailed registration code, stored by hash only.
type RegistrationEmailCodePO struct {
	ID         uint `gorm:"primaryKey"`
	Email      string
	CodeHash   string
	ExpiresAt  time.Time
	Attempts   int
	ConsumedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// TableName specifies the registration code table name.
func (RegistrationEmailCodePO) TableName() string {
	return "registration_email_codes"
}

// registrationEmailCodeRetention keeps rows long enough to count the issue window, then lets the
// next issue for the address delete them.
const registrationEmailCodeRetention = 24 * time.Hour

var _ registrationEmailCodeStore = (*repository)(nil)

// IssueRegistrationEmailCode stores a new code for an address unless its cooldown or window cap
// refuses it. A transaction-scoped advisory lock on the address serializes concurrent requests, so
// two requests cannot both pass the limits.
func (r *repository) IssueRegistrationEmailCode(
	ctx context.Context,
	email string,
	codeHash string,
	now time.Time,
	policy registrationEmailCodePolicy,
) (registrationEmailCodeIssue, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return "", err
	}
	var issue registrationEmailCodeIssue
	err = db.Transaction(func(tx *gorm.DB) error {
		if lockErr := tx.Exec(
			`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, "registration_email_code:"+email,
		).Error; lockErr != nil {
			return lockErr
		}

		var recent struct {
			InWindow int64
			Latest   *time.Time
		}
		if queryErr := tx.Raw(
			`SELECT COUNT(*) FILTER (WHERE created_at > ?) AS in_window, MAX(created_at) AS latest
			   FROM registration_email_codes WHERE email = ?`,
			now.Add(-policy.Window), email,
		).Scan(&recent).Error; queryErr != nil {
			return queryErr
		}
		switch {
		case recent.Latest != nil && now.Before(recent.Latest.Add(policy.Cooldown)):
			issue = registrationEmailCodeCoolingDown
			return nil
		case recent.InWindow >= int64(policy.MaxPerWindow):
			issue = registrationEmailCodeCapped
			return nil
		}

		if deleteErr := tx.Where("email = ? AND created_at < ?", email, now.Add(-registrationEmailCodeRetention)).
			Delete(&RegistrationEmailCodePO{}).Error; deleteErr != nil {
			return deleteErr
		}
		if createErr := tx.Create(&RegistrationEmailCodePO{
			Email:     email,
			CodeHash:  codeHash,
			ExpiresAt: now.Add(policy.TTL),
			CreatedAt: now,
			UpdatedAt: now,
		}).Error; createErr != nil {
			return createErr
		}
		issue = registrationEmailCodeIssued
		return nil
	})
	if err != nil {
		return "", err
	}
	return issue, nil
}

// RegisterWithEmailCode locks the address's newest code, checks it, runs create inside the same
// transaction, and consumes the code. A wrong guess commits only its attempt count; any other
// failure rolls everything back, so the code stays usable.
func (r *repository) RegisterWithEmailCode(
	ctx context.Context,
	email string,
	codeHash string,
	now time.Time,
	maxAttempts int,
	create func(context.Context) error,
) error {
	db, err := r.withContext(ctx)
	if err != nil {
		return err
	}
	var rejection error
	err = db.Transaction(func(tx *gorm.DB) error {
		var code RegistrationEmailCodePO
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("email = ?", email).
			Order("created_at DESC, id DESC").
			Take(&code).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			rejection = domain.ErrRegistrationEmailCodeInvalid
			return nil
		}
		if findErr != nil {
			return findErr
		}

		switch {
		case code.ConsumedAt != nil:
			rejection = domain.ErrRegistrationEmailCodeInvalid
			return nil
		case !now.Before(code.ExpiresAt) || code.Attempts >= maxAttempts:
			rejection = domain.ErrRegistrationEmailCodeExpired
			return nil
		case subtle.ConstantTimeCompare([]byte(code.CodeHash), []byte(codeHash)) != 1:
			rejection = domain.ErrRegistrationEmailCodeInvalid
			return tx.Model(&RegistrationEmailCodePO{}).Where("id = ?", code.ID).Updates(map[string]any{
				"attempts":   gorm.Expr("attempts + 1"),
				"updated_at": now,
			}).Error
		}

		if createErr := create(infradatabase.ContextWithTransaction(ctx, tx)); createErr != nil {
			return createErr
		}
		return tx.Model(&RegistrationEmailCodePO{}).Where("id = ?", code.ID).Updates(map[string]any{
			"consumed_at": now,
			"updated_at":  now,
		}).Error
	})
	if err != nil {
		return err
	}
	return rejection
}
