package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailZonePO is the persistence object of a managed zone.
type MailZonePO struct {
	ID                  uint   `gorm:"primaryKey"`
	OrganizationID      uint   `gorm:"not null;index:idx_mail_zones_organization"`
	Name                string `gorm:"size:253;not null;uniqueIndex:idx_mail_zones_name"`
	CloudflareZoneID    string `gorm:"size:64;not null;uniqueIndex:idx_mail_zones_cloudflare_zone"`
	Status              string `gorm:"size:20;not null;default:'pending'"`
	NameServers         string `gorm:"type:jsonb;not null;default:'[]'"`
	OriginalNameServers string `gorm:"type:jsonb;not null;default:'[]'"`
	ActivatedAt         *time.Time
	LastCheckedAt       *time.Time
	CreatedBy           uint `gorm:"not null"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (MailZonePO) TableName() string {
	return "mail_zones"
}

func (po *MailZonePO) toDomain() *domain.MailZone {
	return &domain.MailZone{
		ID:                  po.ID,
		OrganizationID:      po.OrganizationID,
		Name:                po.Name,
		CloudflareZoneID:    po.CloudflareZoneID,
		Status:              domain.MailZoneStatus(po.Status),
		NameServers:         decodeNameServers(po.NameServers),
		OriginalNameServers: decodeNameServers(po.OriginalNameServers),
		ActivatedAt:         po.ActivatedAt,
		LastCheckedAt:       po.LastCheckedAt,
		CreatedBy:           po.CreatedBy,
		CreatedAt:           po.CreatedAt,
		UpdatedAt:           po.UpdatedAt,
	}
}

func mailZonePO(zone *domain.MailZone) (*MailZonePO, error) {
	nameServers, err := encodeNameServers(zone.NameServers)
	if err != nil {
		return nil, err
	}
	originalNameServers, err := encodeNameServers(zone.OriginalNameServers)
	if err != nil {
		return nil, err
	}
	status := zone.Status
	if status == "" {
		status = domain.MailZoneStatusPending
	}
	return &MailZonePO{
		ID:                  zone.ID,
		OrganizationID:      zone.OrganizationID,
		Name:                strings.ToLower(zone.Name),
		CloudflareZoneID:    zone.CloudflareZoneID,
		Status:              string(status),
		NameServers:         nameServers,
		OriginalNameServers: originalNameServers,
		ActivatedAt:         zone.ActivatedAt,
		LastCheckedAt:       zone.LastCheckedAt,
		CreatedBy:           zone.CreatedBy,
		CreatedAt:           zone.CreatedAt,
	}, nil
}

func encodeNameServers(nameServers []string) (string, error) {
	if nameServers == nil {
		nameServers = []string{}
	}
	encoded, err := json.Marshal(nameServers)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeNameServers(encoded string) []string {
	nameServers := []string{}
	if encoded != "" {
		// A row written by this code always decodes; an unreadable one shows no name servers.
		if err := json.Unmarshal([]byte(encoded), &nameServers); err != nil || nameServers == nil {
			nameServers = []string{}
		}
	}
	return nameServers
}

func isForeignKeyViolation(err error) bool {
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return true
	}
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "23503"
}

// CreateZone inserts a zone while holding a per-organization advisory lock, so two concurrent
// requests cannot both pass the limit. The unique indexes decide between organizations.
func (r *repository) CreateZone(ctx context.Context, zone *domain.MailZone, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po, err := mailZonePO(zone)
	if err != nil {
		return err
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mail_zone_owner'), ?)`, int32(zone.OrganizationID)).Error; err != nil { //nolint:gosec // lock key only needs to be stable per organization
			return err
		}
		var owned int64
		if err := tx.Model(&MailZonePO{}).Where("organization_id = ?", zone.OrganizationID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return ErrMailZoneLimitReached
		}
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrMailDomainTaken
			}
			return err
		}
		*zone = *po.toDomain()
		return nil
	})
}

func (r *repository) FindZone(ctx context.Context, organizationID, id uint) (*domain.MailZone, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailZonePO
	if err := r.conn(ctx).Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailZoneNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) FindZoneByName(ctx context.Context, name string) (*domain.MailZone, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailZonePO
	if err := r.conn(ctx).Where("name = ?", strings.ToLower(name)).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailZoneNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

// fastbuild:bounded-list max=20 reason=an organization holds at most domain.MaxMailZonesPerOrganization managed zones
func (r *repository) ListZones(ctx context.Context, organizationID uint) ([]*domain.MailZone, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailZonePO
	if err := r.conn(ctx).Where("organization_id = ?", organizationID).Order("name asc").
		Limit(domain.MaxMailZonesPerOrganization).Find(&rows).Error; err != nil {
		return nil, err
	}
	return mailZones(rows), nil
}

// ListPendingZones returns at most limit pending zones, never-checked ones first and then the
// least recently checked, so a poller works through every zone in turn.
func (r *repository) ListPendingZones(ctx context.Context, limit int) ([]*domain.MailZone, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailZonePO
	if err := r.conn(ctx).Where("status = ?", string(domain.MailZoneStatusPending)).
		Order("last_checked_at ASC NULLS FIRST").Order("id ASC").
		Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return mailZones(rows), nil
}

func mailZones(rows []MailZonePO) []*domain.MailZone {
	zones := make([]*domain.MailZone, 0, len(rows))
	for i := range rows {
		zones = append(zones, rows[i].toDomain())
	}
	return zones
}

func (r *repository) UpdateZone(ctx context.Context, zone *domain.MailZone) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po, err := mailZonePO(zone)
	if err != nil {
		return err
	}
	result := r.conn(ctx).Model(&MailZonePO{}).
		Where("id = ? AND organization_id = ?", zone.ID, zone.OrganizationID).
		Updates(map[string]any{
			"status":                po.Status,
			"name_servers":          po.NameServers,
			"original_name_servers": po.OriginalNameServers,
			"activated_at":          po.ActivatedAt,
			"last_checked_at":       po.LastCheckedAt,
			"updated_at":            time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailZoneNotFound
	}
	return nil
}

// ActivateZone moves a pending zone to active in one conditional update, so concurrent checks
// agree on a single activation and an active zone never returns to pending.
func (r *repository) ActivateZone(ctx context.Context, id uint, at time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailZonePO{}).
		Where("id = ? AND status = ?", id, string(domain.MailZoneStatusPending)).
		Updates(map[string]any{
			"status":          string(domain.MailZoneStatusActive),
			"activated_at":    at,
			"last_checked_at": at,
			"updated_at":      time.Now(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// TouchZone stores when a zone was last checked and nothing else.
func (r *repository) TouchZone(ctx context.Context, id uint, at time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailZonePO{}).Where("id = ?", id).
		Updates(map[string]any{"last_checked_at": at, "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailZoneNotFound
	}
	return nil
}

// DeleteZone refuses while any mail domain lives in the zone. The foreign key catches a domain
// added after the check.
func (r *repository) DeleteZone(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var po MailZonePO
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMailZoneNotFound
			}
			return err
		}
		var used int64
		if err := tx.Model(&MailDomainPO{}).Where("mail_zone_id = ?", po.ID).Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			return ErrMailZoneInUse
		}
		if err := tx.Delete(&MailZonePO{}, po.ID).Error; err != nil {
			if isForeignKeyViolation(err) {
				return ErrMailZoneInUse
			}
			return err
		}
		return nil
	})
}

// DeletePendingZone removes a pending zone and the domains in it in one transaction. A domain in
// a pending zone cannot have mailboxes, since it is never verified while the zone is pending; a
// verified domain in the zone all the same makes it refuse with ErrMailZoneInUse.
//
// The zone row stays locked from the pending check until commit, and removeAtProvider runs inside
// that window: ActivateZone waits on the lock, so the provider zone is removed only while the row
// is still pending, and a provider failure rolls the rows back for a later retry.
func (r *repository) DeletePendingZone(ctx context.Context, id uint, removeAtProvider func(context.Context) error) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var po MailZonePO
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND status = ?", id, string(domain.MailZoneStatusPending)).First(&po).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMailZoneNotFound
			}
			return err
		}
		var verified int64
		if err := tx.Model(&MailDomainPO{}).
			Where("mail_zone_id = ? AND status = ?", po.ID, string(domain.MailDomainStatusVerified)).
			Count(&verified).Error; err != nil {
			return err
		}
		if verified > 0 {
			return ErrMailZoneInUse
		}
		if err := tx.Where("mail_zone_id = ?", po.ID).Delete(&MailDomainPO{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&MailZonePO{}, po.ID).Error; err != nil {
			return err
		}
		return removeAtProvider(ctx)
	})
}

// fastbuild:bounded-list max=10 reason=a zone belongs to one organization, which connects at most domain.MaxMailDomainsPerOrganization domains
func (r *repository) ListDomainsInZone(ctx context.Context, zoneID uint) ([]*domain.MailDomain, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailDomainPO
	if err := r.conn(ctx).Where("mail_zone_id = ?", zoneID).Order("name asc").
		Limit(domain.MaxMailDomainsPerOrganization).Find(&rows).Error; err != nil {
		return nil, err
	}
	domains := make([]*domain.MailDomain, 0, len(rows))
	for i := range rows {
		domains = append(domains, rows[i].toDomain())
	}
	return domains, nil
}
