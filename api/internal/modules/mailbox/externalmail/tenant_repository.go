package externalmail

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

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

// CreateExternalMailTenant inserts a new active tenant authorization.
func (r *Repository) CreateExternalMailTenant(ctx context.Context, tenant *domain.ExternalMailTenant) error {
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

// FindExternalMailTenant loads a tenant by organization and id.
func (r *Repository) FindExternalMailTenant(ctx context.Context, organizationID, id uint) (*domain.ExternalMailTenant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po ExternalMailTenantPO
	err := r.conn(ctx).Where("organization_id = ? AND id = ?", organizationID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, r.errs.TenantNotFound
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

// FindLiveExternalMailTenant loads a non-revoked tenant by provider and tenant identifier.
func (r *Repository) FindLiveExternalMailTenant(ctx context.Context, provider domain.ExternalMailProvider, identifier string) (*domain.ExternalMailTenant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po ExternalMailTenantPO
	err := r.conn(ctx).
		Where("provider = ? AND lower(tenant_identifier) = lower(?) AND status <> ?", string(provider), identifier,
			string(domain.ExternalMailTenantRevoked)).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, r.errs.TenantNotFound
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

// ListExternalMailTenants lists an organization's tenants with live ones first.
func (r *Repository) ListExternalMailTenants(ctx context.Context, organizationID uint) ([]*domain.ExternalMailTenant, error) {
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

// ReauthorizeExternalMailTenant updates a tenant's credential and resumes connections stopped by tenant auth failure.
func (r *Repository) ReauthorizeExternalMailTenant(ctx context.Context, tenantID uint, encryptedCredential, displayName string, authorizedBy uint, now time.Time) (*domain.ExternalMailTenant, error) {
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
			return r.errs.TenantNotFound
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

// MarkExternalMailTenantLost marks an active tenant and its connections as needing reauthorization.
func (r *Repository) MarkExternalMailTenantLost(ctx context.Context, tenantID uint, authorizedAt, now time.Time, refusedConnectionID uint) (bool, error) {
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

// RevokeExternalMailTenant revokes a tenant and all of its non-revoked connections.
func (r *Repository) RevokeExternalMailTenant(ctx context.Context, organizationID, tenantID uint, now time.Time) ([]*domain.ExternalMailConnection, error) {
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

// CountLiveTenantConnections counts non-revoked connections assigned from a tenant.
func (r *Repository) CountLiveTenantConnections(ctx context.Context, tenantID uint) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&ExternalMailConnectionPO{}).
		Where("tenant_id = ? AND status <> ?", tenantID, string(domain.ExternalMailStatusRevoked)).Count(&count).Error
	return int(count), err
}

// ListLiveConnectionsByAccounts returns non-revoked connections matching the given account addresses.
func (r *Repository) ListLiveConnectionsByAccounts(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, addresses []string) ([]*domain.ExternalMailConnection, error) {
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

// ListTenantPersonalConnections lists a member's tenant-assigned personal connections.
func (r *Repository) ListTenantPersonalConnections(ctx context.Context, organizationID, ownerUserID uint, limit int) ([]*domain.ExternalMailConnection, error) {
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

// RestoreTenantConnection reactivates a revoked tenant connection and cancels any pending purge.
func (r *Repository) RestoreTenantConnection(ctx context.Context, connectionID, tenantID uint, directoryID string, notBefore *time.Time, now time.Time) (*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []ExternalMailConnectionPO
	err := r.conn(ctx).Model(&rows).Clauses(clause.Returning{}).
		Where("id = ? AND tenant_id IS NOT NULL AND status = ?", connectionID, string(domain.ExternalMailStatusRevoked)).
		Updates(map[string]any{
			"status": string(domain.ExternalMailStatusActive), "sync_cursor": "", "last_error_code": nil,
			"tenant_id": tenantID, "tenant_directory_id": directoryID, "next_sync_at": now, "updated_at": now,
			// The floor never goes down, and a nil one keeps what is set.
			"received_not_before": gorm.Expr("GREATEST(received_not_before, ?::timestamptz)", notBefore),
			// A restore before the retention ends keeps the copies: the purge is canceled.
			"consecutive_failures": 0, "consecutive_more": 0, "revoked_at": nil, "purge_retry_at": nil,
		}).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, r.errs.ConnectionNotFound
	}
	if err = r.conn(ctx).Model(&mailboxRow{}).Where("id = ?", rows[0].MailboxID).
		Update("external_mail_tenant_id", tenantID).Error; err != nil {
		return nil, err
	}
	restored := rows[0].toDomain()
	// The address is read again: what an earlier revocation left to do at the provider must not run.
	if err = r.DeleteExternalMailProviderReleasesForAccount(ctx, restored.OrganizationID, restored.Provider, restored.AccountAddress, true); err != nil {
		return nil, err
	}
	return restored, nil
}

// ShareTenantConnection converts a tenant connection to shared and raises its address privacy floor.
func (r *Repository) ShareTenantConnection(ctx context.Context, mailboxID uint, boundary time.Time) error {
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
		// The address changed hands through the handover: it is released, and nothing received before
		// the seal boundary ever reaches a later holder. The record itself is kept.
		for _, row := range rows {
			// Someone else now reads the address: the first holder's exemption ends for good.
			if err := tx.Exec(`UPDATE external_mail_address_records
				SET released = true, user_id = NULL, exempt_user_id = NULL, floor_at = GREATEST(floor_at, ?::timestamptz)
				WHERE organization_id = ? AND provider = ? AND address_key = lower(?)`,
				boundary, row.OrganizationID, row.Provider, row.AccountAddress).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// CountLiveExternalMailTenants counts an organization's non-revoked tenants.
func (r *Repository) CountLiveExternalMailTenants(ctx context.Context, organizationID uint) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&ExternalMailTenantPO{}).
		Where("organization_id = ? AND status <> ?", organizationID, string(domain.ExternalMailTenantRevoked)).Count(&count).Error
	return int(count), err
}

// LockActiveExternalMailTenant acquires a shared row lock on an active tenant.
func (r *Repository) LockActiveExternalMailTenant(ctx context.Context, tenantID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	var rows []ExternalMailTenantPO
	if err := r.conn(ctx).Clauses(clause.Locking{Strength: "SHARE"}).
		Where("id = ?", tenantID).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 || rows[0].Status != string(domain.ExternalMailTenantActive) {
		return r.errs.TenantInactive
	}
	return nil
}

// ExternalMailAddressRecordPO is the record of an organization address assigned from a tenant:
// its holder, its privacy floor, and the member exempt from the floor.
type ExternalMailAddressRecordPO struct {
	OrganizationID uint   `gorm:"primaryKey;autoIncrement:false"`
	Provider       string `gorm:"primaryKey;size:16"`
	AddressKey     string `gorm:"primaryKey;size:320"`
	DirectoryID    string `gorm:"size:255;not null;default:''"`
	UserID         *uint
	TenantID       *uint
	TenantHeld     bool `gorm:"not null;default:false"`
	AssignedAt     time.Time
	FloorAt        *time.Time
	ExemptUserID   *uint
	Released       bool `gorm:"not null;default:false"`
}

func (ExternalMailAddressRecordPO) TableName() string {
	return "external_mail_address_records"
}

// RecordAddressHolder upserts the holder and privacy floor for a tenant-assigned address.
func (r *Repository) RecordAddressHolder(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, address, directoryID string, holderUserID, tenantID uint, floor *time.Time, exemptUserID uint, at time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// One record names a directory user: an older record of the user under another address keeps
		// its floor, which the new record takes over, but no longer matches by directory id.
		var inherited *time.Time
		if directoryID != "" {
			var older []ExternalMailAddressRecordPO
			if err := tx.Where("organization_id = ? AND provider = ? AND directory_id = ? AND address_key <> lower(?)",
				organizationID, string(provider), directoryID, address).Find(&older).Error; err != nil {
				return err
			}
			for _, row := range older {
				inherited = laterTime(inherited, row.FloorAt)
			}
			if err := tx.Exec(`UPDATE external_mail_address_records SET directory_id = ''
				WHERE organization_id = ? AND provider = ? AND directory_id = ? AND address_key <> lower(?)`,
				organizationID, string(provider), directoryID, address).Error; err != nil {
				return err
			}
		}
		raised := laterTime(floor, inherited)
		return tx.Exec(`INSERT INTO external_mail_address_records
			(organization_id, provider, address_key, directory_id, user_id, tenant_id, tenant_held, assigned_at, floor_at, exempt_user_id, released)
			VALUES (?, ?, lower(?), ?, ?, ?, ?, ?, ?::timestamptz, ?, false)
			ON CONFLICT (organization_id, provider, address_key) DO UPDATE SET
			  directory_id = CASE WHEN EXCLUDED.directory_id <> '' THEN EXCLUDED.directory_id ELSE external_mail_address_records.directory_id END,
			  user_id = EXCLUDED.user_id,
			  tenant_id = COALESCE(EXCLUDED.tenant_id, external_mail_address_records.tenant_id),
			  tenant_held = external_mail_address_records.tenant_held OR EXCLUDED.tenant_held,
			  assigned_at = EXCLUDED.assigned_at, released = false, exempt_user_id = EXCLUDED.exempt_user_id,
			  floor_at = GREATEST(external_mail_address_records.floor_at, EXCLUDED.floor_at)`,
			organizationID, string(provider), address, directoryID, optionalUserID(holderUserID), optionalUserID(tenantID), tenantID != 0,
			at, raised, optionalUserID(exemptUserID)).Error
	})
}

// FindPersonalAssignment resolves the effective holder and privacy floor for an address.
func (r *Repository) FindPersonalAssignment(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, address, directoryID string) (*domain.ExternalMailPersonalAssignment, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	// The address is one mailbox whichever connector reads it, so its records under every provider
	// count; a directory id only means something under its own provider.
	query := r.conn(ctx).Where("organization_id = ?", organizationID)
	if directoryID != "" {
		query = query.Where("(address_key = lower(?) OR (provider = ? AND directory_id = ?))", address, string(provider), directoryID)
	} else {
		query = query.Where("address_key = lower(?)", address)
	}
	var rows []ExternalMailAddressRecordPO
	// The record of the address under this provider comes first; every other one adds its floor and
	// ends an exemption it does not share.
	if err := query.Order(clause.Expr{SQL: "(address_key = lower(?) AND provider = ?) DESC, assigned_at DESC", Vars: []any{address, string(provider)}}).
		Limit(10).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	record := &domain.ExternalMailPersonalAssignment{Released: rows[0].Released}
	if rows[0].UserID != nil {
		record.UserID = *rows[0].UserID
	}
	if rows[0].ExemptUserID != nil {
		record.ExemptUserID = *rows[0].ExemptUserID
	}
	for _, row := range rows {
		record.FloorAt = laterTime(record.FloorAt, row.FloorAt)
		record.TenantHeld = record.TenantHeld || row.TenantHeld
		if row.ExemptUserID == nil || *row.ExemptUserID != record.ExemptUserID {
			record.ExemptUserID = 0
		}
	}
	return record, nil
}

// MarkPersonalBoundary raises the privacy floor for all addresses currently held by userID.
func (r *Repository) MarkPersonalBoundary(ctx context.Context, organizationID, userID uint, at time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Exec(`UPDATE external_mail_address_records
		SET floor_at = GREATEST(floor_at, ?::timestamptz)
		WHERE organization_id = ? AND user_id = ?`, at, organizationID, userID).Error
}
