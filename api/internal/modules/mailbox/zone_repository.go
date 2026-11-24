package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/domainsetup"
)

var _ domain.MailZoneRepository = (*repository)(nil)

// MailZonePO is the persistence object of a managed zone.
type MailZonePO = domainsetup.MailZonePO

func (r *repository) CreateZone(ctx context.Context, zone *domain.MailZone, limit int) error {
	return r.domainSetupRepo().CreateZone(ctx, zone, limit)
}

func (r *repository) FindZone(ctx context.Context, organizationID, id uint) (*domain.MailZone, error) {
	return r.domainSetupRepo().FindZone(ctx, organizationID, id)
}

func (r *repository) FindZoneByName(ctx context.Context, name string) (*domain.MailZone, error) {
	return r.domainSetupRepo().FindZoneByName(ctx, name)
}

func (r *repository) ListZones(ctx context.Context, organizationID uint) ([]*domain.MailZone, error) {
	return r.domainSetupRepo().ListZones(ctx, organizationID)
}

func (r *repository) ListPendingZones(ctx context.Context, limit int) ([]*domain.MailZone, error) {
	return r.domainSetupRepo().ListPendingZones(ctx, limit)
}

func (r *repository) UpdateZone(ctx context.Context, zone *domain.MailZone) error {
	return r.domainSetupRepo().UpdateZone(ctx, zone)
}

func (r *repository) CompleteZone(ctx context.Context, zone *domain.MailZone) error {
	return r.domainSetupRepo().CompleteZone(ctx, zone)
}

func (r *repository) ActivateZone(ctx context.Context, id uint, at time.Time) (bool, error) {
	return r.domainSetupRepo().ActivateZone(ctx, id, at)
}

func (r *repository) TouchZone(ctx context.Context, id uint, at time.Time) error {
	return r.domainSetupRepo().TouchZone(ctx, id, at)
}

func (r *repository) DeleteZone(
	ctx context.Context,
	organizationID, id uint,
	removeAtProvider func(context.Context, *domain.MailZone) error,
) error {
	return r.domainSetupRepo().DeleteZone(ctx, organizationID, id, removeAtProvider)
}

func (r *repository) DeletePendingZone(ctx context.Context, id uint, removeAtProvider func(context.Context, *domain.MailZone) error) error {
	return r.domainSetupRepo().DeletePendingZone(ctx, id, removeAtProvider)
}

func (r *repository) ListDomainsInZone(ctx context.Context, zoneID uint) ([]*domain.MailDomain, error) {
	return r.domainSetupRepo().ListDomainsInZone(ctx, zoneID)
}

func (r *repository) ListUncheckedDomainsInActiveZones(ctx context.Context, createdBefore time.Time, limit int) ([]*domain.MailDomain, error) {
	return r.domainSetupRepo().ListUncheckedDomainsInActiveZones(ctx, createdBefore, limit)
}
