package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailAppPasswordPO is one device's mail client password; secret_hash is the keyed hash of the
// secret, which is never stored.
type MailAppPasswordPO struct {
	ID               uint   `gorm:"primaryKey"`
	OrganizationID   uint   `gorm:"not null"`
	MembershipID     uint   `gorm:"not null"`
	UserID           uint   `gorm:"not null"`
	Name             string `gorm:"size:64;not null"`
	ScopeIMAP        bool   `gorm:"column:scope_imap;not null"`
	ScopeSMTP        bool   `gorm:"column:scope_smtp;not null"`
	SecretHash       string `gorm:"size:64;not null"`
	Hint             string `gorm:"size:4;not null"`
	LastUsedAt       *time.Time
	LastUsedIP       *string `gorm:"column:last_used_ip;size:64"`
	LastUsedProtocol *string `gorm:"size:8"`
	RevokedAt        *time.Time
	RevokedBy        *uint
	RevokeReason     *string   `gorm:"size:16"`
	CreatedAt        time.Time `gorm:"not null"`
	UpdatedAt        time.Time `gorm:"not null"`
}

func (MailAppPasswordPO) TableName() string { return "mail_app_passwords" }

// MailClientSettingsPO is an organization's switch for mail app sign-in; a missing row is on.
type MailClientSettingsPO struct {
	OrganizationID uint `gorm:"primaryKey;autoIncrement:false"`
	AccessEnabled  bool `gorm:"not null"` // no default tag: GORM would drop an explicit false
	UpdatedBy      *uint
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (MailClientSettingsPO) TableName() string { return "mail_client_settings" }

// MailClientSettings is an organization's mail app sign-in switch; UpdatedAt is nil until saved.
type MailClientSettings struct {
	AccessEnabled bool
	UpdatedAt     *time.Time
}

// appPasswordRevocation names the password a revocation targets: MembershipID limits it to one
// member's own passwords, zero to any of the organization.
type appPasswordRevocation struct {
	OrganizationID uint
	MembershipID   uint
	ID             uint
	RevokedBy      uint
	Reason         domain.MailAppPasswordRevokeReason
	At             time.Time
}

// appPasswordStore stores mail client passwords and the organization switch.
type appPasswordStore interface {
	// CreateAppPassword inserts the password unless its membership already holds limit passwords
	// that are not revoked, which is ErrMailAppPasswordLimitReached.
	CreateAppPassword(ctx context.Context, password *domain.MailAppPassword, secretHash string, limit int) error
	// ListAppPasswords lists a membership's passwords that are not revoked, newest first.
	ListAppPasswords(ctx context.Context, organizationID, membershipID uint) ([]*domain.MailAppPassword, error)
	// RevokeAppPassword revokes a password that is not revoked, or answers ErrMailAppPasswordNotFound.
	RevokeAppPassword(ctx context.Context, revocation appPasswordRevocation) (*domain.MailAppPassword, error)
	// AppPasswordDirectory pages an organization's passwords, newest first, with those revoked
	// after revokedSince.
	AppPasswordDirectory(ctx context.Context, organizationID uint, revokedSince time.Time, offset, limit int) ([]*domain.MailAppPassword, int64, error)
	FindMailClientSettings(ctx context.Context, organizationID uint) (MailClientSettings, error)
	SaveMailClientSettings(ctx context.Context, organizationID uint, enabled bool, updatedBy uint, at time.Time) (MailClientSettings, error)
	// PurgeRevokedAppPasswords deletes up to limit passwords revoked before revokedBefore.
	PurgeRevokedAppPasswords(ctx context.Context, revokedBefore time.Time, limit int) (int, error)
}

var _ appPasswordStore = (*repository)(nil)

func (r *repository) CreateAppPassword(ctx context.Context, password *domain.MailAppPassword, secretHash string, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// The membership row lock serializes one member's creations, so two requests cannot both
		// pass the limit.
		var locked struct{ ID uint }
		if err := tx.Table("organization_memberships").Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").Where("id = ? AND organization_id = ?", password.MembershipID, password.OrganizationID).
			Take(&locked).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrOrganizationMemberNotFound
			}
			return err
		}
		var active int64
		if err := tx.Model(&MailAppPasswordPO{}).
			Where("membership_id = ? AND revoked_at IS NULL", password.MembershipID).Count(&active).Error; err != nil {
			return err
		}
		if active >= int64(limit) {
			return ErrMailAppPasswordLimitReached
		}
		po := MailAppPasswordPO{
			OrganizationID: password.OrganizationID, MembershipID: password.MembershipID, UserID: password.UserID,
			Name: password.Name, ScopeIMAP: password.ScopeIMAP, ScopeSMTP: password.ScopeSMTP,
			SecretHash: secretHash, Hint: password.Hint, CreatedAt: password.CreatedAt, UpdatedAt: password.CreatedAt,
		}
		if err := tx.Create(&po).Error; err != nil {
			return err
		}
		password.ID = po.ID
		return nil
	})
}

func (r *repository) ListAppPasswords(ctx context.Context, organizationID, membershipID uint) ([]*domain.MailAppPassword, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailAppPasswordPO
	if err := r.conn(ctx).Where("organization_id = ? AND membership_id = ? AND revoked_at IS NULL", organizationID, membershipID).
		Order("id DESC").Limit(MaxAppPasswordsPerMember).Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]*domain.MailAppPassword, 0, len(rows))
	for i := range rows {
		items = append(items, rows[i].toDomain())
	}
	return items, nil
}

func (r *repository) RevokeAppPassword(ctx context.Context, revocation appPasswordRevocation) (*domain.MailAppPassword, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailAppPasswordPO
	query := r.conn(ctx).Model(&po).Clauses(clause.Returning{}).
		Where("id = ? AND organization_id = ? AND revoked_at IS NULL", revocation.ID, revocation.OrganizationID)
	if revocation.MembershipID != 0 {
		query = query.Where("membership_id = ?", revocation.MembershipID)
	}
	result := query.Updates(map[string]any{
		"revoked_at": revocation.At, "revoked_by": revocation.RevokedBy,
		"revoke_reason": string(revocation.Reason), "updated_at": revocation.At,
	})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrMailAppPasswordNotFound
	}
	return po.toDomain(), nil
}

func (r *repository) AppPasswordDirectory(ctx context.Context, organizationID uint, revokedSince time.Time, offset, limit int) ([]*domain.MailAppPassword, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	scope := func() *gorm.DB {
		return r.conn(ctx).Model(&MailAppPasswordPO{}).
			Where("organization_id = ? AND (revoked_at IS NULL OR revoked_at > ?)", organizationID, revokedSince)
	}
	var total int64
	if err := scope().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MailAppPasswordPO
	if err := scope().Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]*domain.MailAppPassword, 0, len(rows))
	for i := range rows {
		items = append(items, rows[i].toDomain())
	}
	return items, total, nil
}

func (r *repository) FindMailClientSettings(ctx context.Context, organizationID uint) (MailClientSettings, error) {
	settings := MailClientSettings{AccessEnabled: true}
	if r.db == nil {
		return settings, domain.ErrServiceUnavailable
	}
	var po MailClientSettingsPO
	err := r.conn(ctx).Where("organization_id = ?", organizationID).Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	return MailClientSettings{AccessEnabled: po.AccessEnabled, UpdatedAt: new(po.UpdatedAt.UTC())}, nil
}

func (r *repository) SaveMailClientSettings(ctx context.Context, organizationID uint, enabled bool, updatedBy uint, at time.Time) (MailClientSettings, error) {
	if r.db == nil {
		return MailClientSettings{}, domain.ErrServiceUnavailable
	}
	po := MailClientSettingsPO{
		OrganizationID: organizationID, AccessEnabled: enabled, UpdatedBy: &updatedBy, CreatedAt: at, UpdatedAt: at,
	}
	if err := r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "organization_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"access_enabled", "updated_by", "updated_at"}),
	}).Create(&po).Error; err != nil {
		return MailClientSettings{}, err
	}
	return r.FindMailClientSettings(ctx, organizationID)
}

func (r *repository) PurgeRevokedAppPasswords(ctx context.Context, revokedBefore time.Time, limit int) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	result := r.conn(ctx).Exec(`DELETE FROM mail_app_passwords WHERE id IN (
		SELECT id FROM mail_app_passwords WHERE revoked_at < ? ORDER BY revoked_at LIMIT ?)`, revokedBefore, limit)
	return int(result.RowsAffected), result.Error
}

func (po *MailAppPasswordPO) toDomain() *domain.MailAppPassword {
	password := &domain.MailAppPassword{
		ID: po.ID, OrganizationID: po.OrganizationID, MembershipID: po.MembershipID, UserID: po.UserID,
		Name: po.Name, ScopeIMAP: po.ScopeIMAP, ScopeSMTP: po.ScopeSMTP, Hint: po.Hint,
		LastUsedAt: utcOrNil(po.LastUsedAt), RevokedAt: utcOrNil(po.RevokedAt), RevokedBy: po.RevokedBy,
		CreatedAt: po.CreatedAt.UTC(),
	}
	if po.LastUsedIP != nil {
		password.LastUsedIP = *po.LastUsedIP
	}
	if po.LastUsedProtocol != nil {
		password.LastUsedProtocol = domain.MailClientProtocol(*po.LastUsedProtocol)
	}
	if po.RevokeReason != nil {
		password.RevokeReason = domain.MailAppPasswordRevokeReason(*po.RevokeReason)
	}
	return password
}

func utcOrNil(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return new(value.UTC())
}
