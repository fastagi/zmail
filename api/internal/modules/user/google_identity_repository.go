package user

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// identityProviderGoogle is the only provider user_identities accepts.
const identityProviderGoogle = "google"

// googleSignInStatePruneBatch bounds the expired states one new sign-in deletes.
const googleSignInStatePruneBatch = 100

// UserIdentityPO links an external sign-in subject to an account.
type UserIdentityPO struct {
	ID           uint `gorm:"primaryKey"`
	UserID       uint
	Provider     string
	Subject      string
	Email        string
	LastSignInAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TableName specifies the identity link table name.
func (UserIdentityPO) TableName() string {
	return "user_identities"
}

// OAuthSignInStatePO is one pending external sign-in. The state and nonce are stored as SHA-256
// digests; the PKCE verifier is kept because the code exchange needs it, and dies with the row.
type OAuthSignInStatePO struct {
	StateHash    string `gorm:"primaryKey"`
	Provider     string
	NonceHash    string
	CodeVerifier string
	RedirectURI  string
	ReturnURL    string
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

// TableName specifies the pending sign-in table name.
func (OAuthSignInStatePO) TableName() string {
	return "oauth_sign_in_states"
}

// googleIdentityStore persists Google links and pending Google sign-ins.
type googleIdentityStore interface {
	SaveOAuthSignInState(ctx context.Context, state *OAuthSignInStatePO, now time.Time) error
	// ConsumeOAuthSignInState deletes and returns an unexpired state; nil when there is none.
	ConsumeOAuthSignInState(ctx context.Context, provider, stateHash string, now time.Time) (*OAuthSignInStatePO, error)
	// FindIdentityUserID returns the account linked to the subject, or gorm.ErrRecordNotFound.
	FindIdentityUserID(ctx context.Context, provider, subject string) (uint, error)
	// FindUserIDByEmailFold returns the live account with the email, ignoring case and preferring an
	// exact match, or gorm.ErrRecordNotFound.
	FindUserIDByEmailFold(ctx context.Context, email string) (uint, error)
	// LinkIdentity links the subject to the account and marks its email verified. Linking an
	// unverified account clears its password and revokes its sessions, and reports tookOver.
	LinkIdentity(ctx context.Context, userID uint, provider, subject, email string, now time.Time) (tookOver bool, err error)
	// RecordIdentitySignIn stores the current email and the time of a sign-in through the subject.
	RecordIdentitySignIn(ctx context.Context, provider, subject, email string, now time.Time) error
	// CreateUserWithIdentity inserts the account and its link in one transaction.
	CreateUserWithIdentity(ctx context.Context, user *UserPO, provider, subject string, now time.Time) error
}

var _ googleIdentityStore = (*repository)(nil)

// SaveOAuthSignInState stores a pending sign-in and deletes a bounded batch of expired ones.
func (r *repository) SaveOAuthSignInState(ctx context.Context, state *OAuthSignInStatePO, now time.Time) error {
	db, err := r.withContext(ctx)
	if err != nil {
		return err
	}
	if err := db.Exec(`DELETE FROM oauth_sign_in_states WHERE state_hash IN (
		SELECT state_hash FROM oauth_sign_in_states WHERE expires_at < ? ORDER BY expires_at LIMIT ?)`,
		now, googleSignInStatePruneBatch).Error; err != nil {
		return err
	}
	return db.Create(state).Error
}

// ConsumeOAuthSignInState deletes the state whatever its age, so a state is used at most once.
func (r *repository) ConsumeOAuthSignInState(
	ctx context.Context,
	provider, stateHash string,
	now time.Time,
) (*OAuthSignInStatePO, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	var rows []OAuthSignInStatePO
	if err := db.Clauses(clause.Returning{}).
		Where("state_hash = ? AND provider = ?", stateHash, provider).
		Delete(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != 1 || !now.Before(rows[0].ExpiresAt) {
		return nil, nil
	}
	return &rows[0], nil
}

// FindIdentityUserID resolves a subject to a live account.
func (r *repository) FindIdentityUserID(ctx context.Context, provider, subject string) (uint, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return 0, err
	}
	var row struct{ UserID uint }
	err = db.Table("user_identities").
		Select("user_identities.user_id").
		Joins("JOIN users ON users.id = user_identities.user_id AND users.deleted_at IS NULL").
		Where("user_identities.provider = ? AND user_identities.subject = ?", provider, subject).
		Take(&row).Error
	if err != nil {
		return 0, err
	}
	return row.UserID, nil
}

// FindUserIDByEmailFold matches the email without regard to case. Two accounts that differ only in
// case resolve to the exact match, else to the oldest.
func (r *repository) FindUserIDByEmailFold(ctx context.Context, email string) (uint, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return 0, err
	}
	var po UserPO
	err = db.Select("id").
		Where("lower(email) = ?", strings.ToLower(email)).
		Order(clause.OrderBy{Expression: clause.Expr{SQL: "CASE WHEN email = ? THEN 0 ELSE 1 END, id", Vars: []any{email}}}).
		Take(&po).Error
	if err != nil {
		return 0, err
	}
	return po.ID, nil
}

// LinkIdentity inserts the link and proves the account's email in one transaction. A link that a
// concurrent sign-in already made for the same account is accepted; one to another account is a
// unique violation.
//
// An account whose email was not yet verified may have been registered by someone else in the
// owner's name, so linking it is a takeover: the same transaction clears its password and revokes
// every session, and tookOver reports it. A verified account keeps both.
func (r *repository) LinkIdentity(
	ctx context.Context,
	userID uint,
	provider, subject, email string,
	now time.Time,
) (tookOver bool, err error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return false, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var account UserPO
		if lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "email_verified_at").
			Take(&account, userID).Error; lockErr != nil {
			return lockErr
		}
		link := &UserIdentityPO{
			UserID: userID, Provider: provider, Subject: subject, Email: email,
			LastSignInAt: &now, CreatedAt: now, UpdatedAt: now,
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(link)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existing UserIdentityPO
			if findErr := tx.Where("provider = ? AND subject = ?", provider, subject).Take(&existing).Error; findErr != nil {
				return findErr
			}
			if existing.UserID != userID {
				return gorm.ErrDuplicatedKey
			}
		}
		if account.EmailVerifiedAt != nil {
			return nil
		}
		if updateErr := tx.Model(&UserPO{}).Where("id = ?", userID).Updates(map[string]any{
			"password": "", "email_verified_at": now, "updated_at": now,
		}).Error; updateErr != nil {
			return updateErr
		}
		tookOver = true
		return revokeUserSessionsDB(tx, userID, now, sessionRevocationIdentityLinked)
	})
	if err != nil {
		return false, err
	}
	return tookOver, nil
}

// RecordIdentitySignIn keeps the link's email current; the account's own email is not changed.
func (r *repository) RecordIdentitySignIn(ctx context.Context, provider, subject, email string, now time.Time) error {
	db, err := r.withContext(ctx)
	if err != nil {
		return err
	}
	return db.Model(&UserIdentityPO{}).
		Where("provider = ? AND subject = ?", provider, subject).
		Updates(map[string]any{"email": email, "last_sign_in_at": now, "updated_at": now}).Error
}

// CreateUserWithIdentity inserts a Google-made account and its link together.
func (r *repository) CreateUserWithIdentity(ctx context.Context, user *UserPO, provider, subject string, now time.Time) error {
	db, err := r.withContext(ctx)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		return tx.Create(&UserIdentityPO{
			UserID: user.ID, Provider: provider, Subject: subject, Email: user.Email,
			LastSignInAt: &now, CreatedAt: now, UpdatedAt: now,
		}).Error
	})
}

// isGoogleIdentityConflict reports a unique violation from a concurrent sign-in.
func isGoogleIdentityConflict(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "23505"
}
