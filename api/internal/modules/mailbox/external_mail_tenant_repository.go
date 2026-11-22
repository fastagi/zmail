package mailbox

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ErrExternalMailTenantNotFound hides a tenant that does not exist or belongs to another
// organization; it answers like a missing connection.
var ErrExternalMailTenantNotFound = errors.New("external mail tenant not found")

// ErrExternalMailTenantInactive means a tenant needs reauthorization or was revoked, so its
// directory cannot be read and nothing can be assigned from it.
var ErrExternalMailTenantInactive = errors.New("external mail tenant is not active")

// ExternalMailTenantPO is the persistent object of one authorized tenant.
type ExternalMailTenantPO struct {
	ID                  uint   `gorm:"primaryKey"`
	OrganizationID      uint   `gorm:"not null"`
	Provider            string `gorm:"size:16;not null"`
	TenantIdentifier    string `gorm:"size:255;not null"`
	DisplayName         string `gorm:"size:255;not null;default:''"`
	Status              string `gorm:"size:24;not null;default:'active'"`
	EncryptedCredential string `gorm:"type:text;not null;default:''"`
	AuthorizedByUserID  *uint
	LastErrorCode       *string `gorm:"size:64"`
	AuthorizedAt        time.Time
	RevokedAt           *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (ExternalMailTenantPO) TableName() string {
	return "external_mail_tenants"
}

func (po *ExternalMailTenantPO) toDomain() *domain.ExternalMailTenant {
	tenant := &domain.ExternalMailTenant{
		ID: po.ID, OrganizationID: po.OrganizationID, Provider: domain.ExternalMailProvider(po.Provider),
		TenantIdentifier: po.TenantIdentifier, DisplayName: po.DisplayName, Status: domain.ExternalMailTenantStatus(po.Status),
		EncryptedCredential: po.EncryptedCredential, AuthorizedByUserID: po.AuthorizedByUserID,
		AuthorizedAt: po.AuthorizedAt, RevokedAt: po.RevokedAt, CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt,
	}
	if po.LastErrorCode != nil {
		tenant.LastErrorCode = *po.LastErrorCode
	}
	return tenant
}

var _ domain.ExternalMailTenantRepository = (*repository)(nil)

func (r *repository) CreateExternalMailTenant(ctx context.Context, tenant *domain.ExternalMailTenant) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := &ExternalMailTenantPO{
		OrganizationID: tenant.OrganizationID, Provider: string(tenant.Provider), TenantIdentifier: tenant.TenantIdentifier,
		DisplayName: tenant.DisplayName, Status: string(domain.ExternalMailTenantActive),
		EncryptedCredential: tenant.EncryptedCredential, AuthorizedByUserID: tenant.AuthorizedByUserID,
		AuthorizedAt: tenant.AuthorizedAt, CreatedAt: tenant.AuthorizedAt, UpdatedAt: tenant.AuthorizedAt,
	}
	if err := r.conn(ctx).Create(po).Error; err != nil {
		return err
	}
	*tenant = *po.toDomain()
	return nil
}

func (r *repository) FindExternalMailTenant(ctx context.Context, organizationID, id uint) (*domain.ExternalMailTenant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po ExternalMailTenantPO
	err := r.conn(ctx).Where("organization_id = ? AND id = ?", organizationID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrExternalMailTenantNotFound
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) FindLiveExternalMailTenant(ctx context.Context, provider domain.ExternalMailProvider, identifier string) (*domain.ExternalMailTenant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po ExternalMailTenantPO
	err := r.conn(ctx).
		Where("provider = ? AND lower(tenant_identifier) = lower(?) AND status <> ?", string(provider), identifier,
			string(domain.ExternalMailTenantRevoked)).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrExternalMailTenantNotFound
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) ListExternalMailTenants(ctx context.Context, organizationID uint) ([]*domain.ExternalMailTenant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []ExternalMailTenantPO
	err := r.conn(ctx).Where("organization_id = ?", organizationID).
		Order("(status = 'revoked'), id ASC").Limit(domain.MaxExternalMailTenantsPerOrganization).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	tenants := make([]*domain.ExternalMailTenant, 0, len(rows))
	for i := range rows {
		tenants = append(tenants, rows[i].toDomain())
	}
	return tenants, nil
}

func (r *repository) ReauthorizeExternalMailTenant(ctx context.Context, tenantID uint, encryptedCredential, displayName string, authorizedBy uint, now time.Time) (*domain.ExternalMailTenant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var tenant *domain.ExternalMailTenant
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"status": string(domain.ExternalMailTenantActive), "encrypted_credential": encryptedCredential,
			"authorized_by_user_id": authorizedBy, "last_error_code": nil, "authorized_at": now, "updated_at": now,
		}
		if displayName != "" {
			updates["display_name"] = displayName
		}
		var rows []ExternalMailTenantPO
		if err := tx.Model(&rows).Clauses(clause.Returning{}).
			Where("id = ? AND status <> ?", tenantID, string(domain.ExternalMailTenantRevoked)).Updates(updates).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return ErrExternalMailTenantNotFound
		}
		tenant = rows[0].toDomain()
		// Only the connections the lost tenant credential stopped resume at once; one whose own mailbox
		// was refused stays stopped, and revoked ones stay revoked.
		return tx.Model(&ExternalMailConnectionPO{}).
			Where("tenant_id = ? AND status = ? AND last_error_code = ?", tenantID,
				string(domain.ExternalMailStatusNeedsReconnect), domain.ExternalMailErrorTenantAuthFailed).
			Updates(map[string]any{
				"status": string(domain.ExternalMailStatusActive), "last_error_code": nil, "next_sync_at": now,
				"consecutive_failures": 0, "updated_at": now,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return tenant, nil
}

func (r *repository) MarkExternalMailTenantLost(ctx context.Context, tenantID uint, authorizedAt, now time.Time, refusedConnectionID uint) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	lost := false
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&ExternalMailTenantPO{}).
			Where("id = ? AND status = ? AND authorized_at = ?", tenantID, string(domain.ExternalMailTenantActive), authorizedAt).
			Updates(map[string]any{
				"status":          string(domain.ExternalMailTenantNeedsReauthorization),
				"last_error_code": domain.ExternalMailErrorTenantAuthFailed, "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		lost = result.RowsAffected > 0
		if !lost {
			return nil
		}
		// Every connection stops at once, the one being synced included: its save finds it no longer
		// active and drops the page.
		return tx.Model(&ExternalMailConnectionPO{}).
			Where("tenant_id = ? AND (status = ? OR (id = ? AND status = ?))", tenantID, string(domain.ExternalMailStatusActive),
				refusedConnectionID, string(domain.ExternalMailStatusNeedsReconnect)).
			Updates(map[string]any{
				"status":          string(domain.ExternalMailStatusNeedsReconnect),
				"last_error_code": domain.ExternalMailErrorTenantAuthFailed, "updated_at": now,
			}).Error
	})
	return lost, err
}

func (r *repository) RevokeExternalMailTenant(ctx context.Context, organizationID, tenantID uint, now time.Time) ([]*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var revoked []*domain.ExternalMailConnection
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// The row lock waits for every assignment in progress, which holds it shared, so the
		// connections it creates are revoked below too.
		var locked []ExternalMailTenantPO
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("organization_id = ? AND id = ?", organizationID, tenantID).Find(&locked).Error; err != nil {
			return err
		}
		result := tx.Model(&ExternalMailTenantPO{}).
			Where("organization_id = ? AND id = ? AND status <> ?", organizationID, tenantID, string(domain.ExternalMailTenantRevoked)).
			Updates(map[string]any{
				"status": string(domain.ExternalMailTenantRevoked), "encrypted_credential": "",
				"revoked_at": now, "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		// The rows are read and locked before the update, so the caller records each one's revocation.
		var rows []ExternalMailConnectionPO
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND status <> ?", tenantID, string(domain.ExternalMailStatusRevoked)).
			Order("id").Find(&rows).Error; err != nil {
			return err
		}
		for i := range rows {
			revoked = append(revoked, rows[i].toDomain())
		}
		return tx.Model(&ExternalMailConnectionPO{}).
			Where("tenant_id = ? AND status <> ?", tenantID, string(domain.ExternalMailStatusRevoked)).
			Updates(map[string]any{
				"status": string(domain.ExternalMailStatusRevoked), "sync_cursor": "", "consecutive_failures": 0,
				"consecutive_more": 0, "revoked_at": gorm.Expr("COALESCE(revoked_at, ?)", now), "updated_at": now,
			}).Error
	})
	return revoked, err
}

func (r *repository) CountLiveTenantConnections(ctx context.Context, tenantID uint) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&ExternalMailConnectionPO{}).
		Where("tenant_id = ? AND status <> ?", tenantID, string(domain.ExternalMailStatusRevoked)).Count(&count).Error
	return int(count), err
}

func (r *repository) ListLiveConnectionsByAccounts(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, addresses []string) ([]*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if len(addresses) == 0 {
		return nil, nil
	}
	lowered := make([]string, 0, len(addresses))
	for _, address := range addresses {
		lowered = append(lowered, strings.ToLower(address))
	}
	var rows []ExternalMailConnectionPO
	err := r.conn(ctx).
		Where("organization_id = ? AND provider = ? AND lower(account_address) IN ? AND status <> ?", organizationID,
			string(provider), lowered, string(domain.ExternalMailStatusRevoked)).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	connections := make([]*domain.ExternalMailConnection, 0, len(rows))
	for i := range rows {
		connections = append(connections, rows[i].toDomain())
	}
	return connections, nil
}

func (r *repository) ListTenantPersonalConnections(ctx context.Context, organizationID, ownerUserID uint, limit int) ([]*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []ExternalMailConnectionPO
	err := r.conn(ctx).
		Where("organization_id = ? AND owner_user_id = ? AND NOT shared AND tenant_id IS NOT NULL", organizationID, ownerUserID).
		Order("(status = 'revoked'), id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	connections := make([]*domain.ExternalMailConnection, 0, len(rows))
	for i := range rows {
		connections = append(connections, rows[i].toDomain())
	}
	return connections, nil
}

func (r *repository) RestoreTenantConnection(ctx context.Context, connectionID, tenantID uint, directoryID string, now time.Time) (*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []ExternalMailConnectionPO
	err := r.conn(ctx).Model(&rows).Clauses(clause.Returning{}).
		Where("id = ? AND tenant_id = ? AND status = ?", connectionID, tenantID, string(domain.ExternalMailStatusRevoked)).
		Updates(map[string]any{
			"status": string(domain.ExternalMailStatusActive), "sync_cursor": "", "last_error_code": nil,
			"tenant_directory_id": directoryID, "next_sync_at": now, "updated_at": now,
			// A restore before the retention ends keeps the copies: the purge is canceled.
			"consecutive_failures": 0, "consecutive_more": 0, "revoked_at": nil, "purge_retry_at": nil,
		}).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrExternalMailConnectionNotFound
	}
	return rows[0].toDomain(), nil
}

func (r *repository) ShareTenantConnection(ctx context.Context, mailboxID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []ExternalMailConnectionPO
		if err := tx.Model(&rows).Clauses(clause.Returning{}).
			Where("mailbox_id = ? AND tenant_id IS NOT NULL", mailboxID).
			Updates(map[string]any{"shared": true, "owner_user_id": nil, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		// The address changed hands through the handover: it is no longer any member's.
		for _, row := range rows {
			if err := tx.Exec(`DELETE FROM external_mail_personal_assignments
				WHERE organization_id = ? AND provider = ? AND address_key = lower(?)`,
				row.OrganizationID, row.Provider, row.AccountAddress).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *repository) CountLiveExternalMailTenants(ctx context.Context, organizationID uint) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&ExternalMailTenantPO{}).
		Where("organization_id = ? AND status <> ?", organizationID, string(domain.ExternalMailTenantRevoked)).Count(&count).Error
	return int(count), err
}

func (r *repository) LockActiveExternalMailTenant(ctx context.Context, tenantID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	var rows []ExternalMailTenantPO
	if err := r.conn(ctx).Clauses(clause.Locking{Strength: "SHARE"}).
		Where("id = ?", tenantID).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 || rows[0].Status != string(domain.ExternalMailTenantActive) {
		return ErrExternalMailTenantInactive
	}
	return nil
}

// ExternalMailPersonalAssignmentPO remembers the member an organization address was last personally
// assigned to.
type ExternalMailPersonalAssignmentPO struct {
	OrganizationID uint   `gorm:"primaryKey;autoIncrement:false"`
	Provider       string `gorm:"primaryKey;size:16"`
	AddressKey     string `gorm:"primaryKey;size:320"`
	UserID         uint   `gorm:"not null"`
	TenantID       uint   `gorm:"not null"`
	AssignedAt     time.Time
}

func (ExternalMailPersonalAssignmentPO) TableName() string {
	return "external_mail_personal_assignments"
}

func (r *repository) RecordPersonalAssignment(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, address string, userID, tenantID uint, at time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "organization_id"}, {Name: "provider"}, {Name: "address_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "tenant_id", "assigned_at"}),
	}).Create(&ExternalMailPersonalAssignmentPO{
		OrganizationID: organizationID, Provider: string(provider), AddressKey: strings.ToLower(address),
		UserID: userID, TenantID: tenantID, AssignedAt: at,
	}).Error
}

func (r *repository) FindPersonalAssignee(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, address string) (uint, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var rows []ExternalMailPersonalAssignmentPO
	if err := r.conn(ctx).Where("organization_id = ? AND provider = ? AND address_key = ?", organizationID,
		string(provider), strings.ToLower(address)).Limit(1).Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].UserID, nil
}
