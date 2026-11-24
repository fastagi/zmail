package mailbox

import (
	"context"
	"errors"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/externalmail"
)

// ErrExternalMailTenantNotFound hides a tenant that does not exist or belongs to another
// organization; it answers like a missing connection.
var ErrExternalMailTenantNotFound = errors.New("external mail tenant not found")

// ErrExternalMailTenantInactive means a tenant needs reauthorization or was revoked, so its
// directory cannot be read and nothing can be assigned from it.
var ErrExternalMailTenantInactive = errors.New("external mail tenant is not active")

// ExternalMailTenantPO is the persistent object of one authorized tenant.
type ExternalMailTenantPO = externalmail.ExternalMailTenantPO

// ExternalMailAddressRecordPO is the record of an organization address assigned from a tenant:
// its holder, its privacy floor, and the member exempt from the floor.
type ExternalMailAddressRecordPO = externalmail.ExternalMailAddressRecordPO

var _ domain.ExternalMailTenantRepository = (*repository)(nil)

func (r *repository) CreateExternalMailTenant(ctx context.Context, tenant *domain.ExternalMailTenant) error {
	return r.externalMailRepo().CreateExternalMailTenant(ctx, tenant)
}

func (r *repository) FindExternalMailTenant(ctx context.Context, organizationID, id uint) (*domain.ExternalMailTenant, error) {
	return r.externalMailRepo().FindExternalMailTenant(ctx, organizationID, id)
}

func (r *repository) FindLiveExternalMailTenant(ctx context.Context, provider domain.ExternalMailProvider, identifier string) (*domain.ExternalMailTenant, error) {
	return r.externalMailRepo().FindLiveExternalMailTenant(ctx, provider, identifier)
}

func (r *repository) ListExternalMailTenants(ctx context.Context, organizationID uint) ([]*domain.ExternalMailTenant, error) {
	return r.externalMailRepo().ListExternalMailTenants(ctx, organizationID)
}

func (r *repository) ReauthorizeExternalMailTenant(ctx context.Context, tenantID uint, encryptedCredential, displayName string, authorizedBy uint, now time.Time) (*domain.ExternalMailTenant, error) {
	return r.externalMailRepo().ReauthorizeExternalMailTenant(ctx, tenantID, encryptedCredential, displayName, authorizedBy, now)
}

func (r *repository) MarkExternalMailTenantLost(ctx context.Context, tenantID uint, authorizedAt, now time.Time, refusedConnectionID uint) (bool, error) {
	return r.externalMailRepo().MarkExternalMailTenantLost(ctx, tenantID, authorizedAt, now, refusedConnectionID)
}

func (r *repository) RevokeExternalMailTenant(ctx context.Context, organizationID, tenantID uint, now time.Time) ([]*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().RevokeExternalMailTenant(ctx, organizationID, tenantID, now)
}

func (r *repository) CountLiveTenantConnections(ctx context.Context, tenantID uint) (int, error) {
	return r.externalMailRepo().CountLiveTenantConnections(ctx, tenantID)
}

func (r *repository) ListLiveConnectionsByAccounts(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, addresses []string) ([]*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().ListLiveConnectionsByAccounts(ctx, organizationID, provider, addresses)
}

func (r *repository) ListTenantPersonalConnections(ctx context.Context, organizationID, ownerUserID uint, limit int) ([]*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().ListTenantPersonalConnections(ctx, organizationID, ownerUserID, limit)
}

func (r *repository) RestoreTenantConnection(ctx context.Context, connectionID, tenantID uint, directoryID string, notBefore *time.Time, now time.Time) (*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().RestoreTenantConnection(ctx, connectionID, tenantID, directoryID, notBefore, now)
}

func (r *repository) ShareTenantConnection(ctx context.Context, mailboxID uint, boundary time.Time) error {
	return r.externalMailRepo().ShareTenantConnection(ctx, mailboxID, boundary)
}

func (r *repository) CountLiveExternalMailTenants(ctx context.Context, organizationID uint) (int, error) {
	return r.externalMailRepo().CountLiveExternalMailTenants(ctx, organizationID)
}

func (r *repository) LockActiveExternalMailTenant(ctx context.Context, tenantID uint) error {
	return r.externalMailRepo().LockActiveExternalMailTenant(ctx, tenantID)
}

func (r *repository) RecordAddressHolder(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, address, directoryID string, holderUserID, tenantID uint, floor *time.Time, exemptUserID uint, at time.Time) error {
	return r.externalMailRepo().RecordAddressHolder(ctx, organizationID, provider, address, directoryID, holderUserID, tenantID, floor, exemptUserID, at)
}

func (r *repository) FindPersonalAssignment(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, address, directoryID string) (*domain.ExternalMailPersonalAssignment, error) {
	return r.externalMailRepo().FindPersonalAssignment(ctx, organizationID, provider, address, directoryID)
}

func (r *repository) MarkPersonalBoundary(ctx context.Context, organizationID, userID uint, at time.Time) error {
	return r.externalMailRepo().MarkPersonalBoundary(ctx, organizationID, userID, at)
}
