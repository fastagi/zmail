package domainsetup

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

// MailDomainPO is the persistence object of a connected domain.
type MailDomainPO struct {
	CloudflareConnectionID *string `gorm:"type:uuid"`
	ID                     uint    `gorm:"primaryKey"`
	OrganizationID         uint    `gorm:"not null;index:idx_mail_domains_organization;uniqueIndex:idx_mail_domains_default,where:is_default"`
	Name                   string  `gorm:"size:253;not null;uniqueIndex:idx_mail_domains_name"`
	ZoneID                 string  `gorm:"size:64;not null"`
	ZoneName               string  `gorm:"size:253;not null"`
	MailZoneID             *uint   `gorm:"index:idx_mail_domains_mail_zone"`
	Capability             string  `gorm:"size:20;not null;default:'send_receive'"`
	SendingTag             string  `gorm:"size:64;not null;default:''"`
	Status                 string  `gorm:"size:20;not null;default:'pending'"`
	Records                string  `gorm:"type:jsonb;not null;default:'[]'"`
	LastError              string  `gorm:"size:500;not null;default:''"`
	LastErrorCode          string  `gorm:"size:64;not null;default:''"`
	IsDefault              bool    `gorm:"not null;default:false"`
	CheckedAt              *time.Time
	VerifiedAt             *time.Time
	CreatedBy              uint `gorm:"not null"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (MailDomainPO) TableName() string {
	return "mail_domains"
}

func (po *MailDomainPO) toDomain() *domain.MailDomain {
	mailDomain := &domain.MailDomain{
		CloudflareConnectionID: po.CloudflareConnectionID,
		ID:                     po.ID,
		OrganizationID:         po.OrganizationID,
		Name:                   po.Name,
		ZoneID:                 po.ZoneID,
		ZoneName:               po.ZoneName,
		MailZoneID:             po.MailZoneID,
		Capability:             domain.MailDomainCapability(po.Capability),
		SendingTag:             po.SendingTag,
		Status:                 domain.MailDomainStatus(po.Status),
		LastError:              po.LastError,
		LastErrorCode:          po.LastErrorCode,
		MarkedDefault:          po.IsDefault,
		CheckedAt:              po.CheckedAt,
		VerifiedAt:             po.VerifiedAt,
		CreatedBy:              po.CreatedBy,
		CreatedAt:              po.CreatedAt,
		UpdatedAt:              po.UpdatedAt,
	}
	if po.Records != "" {
		// A row written by this code always decodes; an unreadable one shows no records.
		if err := json.Unmarshal([]byte(po.Records), &mailDomain.Records); err != nil {
			mailDomain.Records = nil
		}
	}
	if mailDomain.Records == nil {
		mailDomain.Records = []domain.MailDomainRecord{}
	}
	return mailDomain
}

func mailDomainPO(mailDomain *domain.MailDomain) (*MailDomainPO, error) {
	records := mailDomain.Records
	if records == nil {
		records = []domain.MailDomainRecord{}
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		return nil, err
	}
	status := mailDomain.Status
	if status == "" {
		status = domain.MailDomainStatusPending
	}
	capability := mailDomain.Capability
	if capability == "" {
		capability = domain.MailDomainSendReceive
	}
	return &MailDomainPO{
		CloudflareConnectionID: mailDomain.CloudflareConnectionID,
		ID:                     mailDomain.ID,
		OrganizationID:         mailDomain.OrganizationID,
		Name:                   mailDomain.Name,
		ZoneID:                 mailDomain.ZoneID,
		ZoneName:               mailDomain.ZoneName,
		MailZoneID:             mailDomain.MailZoneID,
		Capability:             string(capability),
		SendingTag:             mailDomain.SendingTag,
		Status:                 string(status),
		Records:                string(encoded),
		LastError:              TruncateRunes(mailDomain.LastError, 500),
		LastErrorCode:          mailDomain.LastErrorCode,
		IsDefault:              mailDomain.MarkedDefault,
		CheckedAt:              mailDomain.CheckedAt,
		VerifiedAt:             mailDomain.VerifiedAt,
		CreatedBy:              mailDomain.CreatedBy,
		CreatedAt:              mailDomain.CreatedAt,
	}, nil
}

func (r *Repository) CreateDomain(ctx context.Context, mailDomain *domain.MailDomain, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po, err := mailDomainPO(mailDomain)
	if err != nil {
		return err
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mail_domain_owner'), ?)`, int32(mailDomain.OrganizationID)).Error; err != nil { //nolint:gosec // lock key only needs to be stable per organization
			return err
		}
		var owned int64
		if err := tx.Model(&MailDomainPO{}).Where("organization_id = ?", mailDomain.OrganizationID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return r.errs.DomainLimitReached
		}
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return r.errs.DomainTaken
			}
			return err
		}
		*mailDomain = *po.toDomain()
		return nil
	})
}

// fastbuild:bounded-list max=10 reason=an organization connects at most domain.MaxMailDomainsPerOrganization domains
func (r *Repository) ListDomains(ctx context.Context, organizationID uint) ([]*domain.MailDomain, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailDomainPO
	if err := r.conn(ctx).Where("organization_id = ?", organizationID).Order("name asc").
		Limit(domain.MaxMailDomainsPerOrganization).Find(&rows).Error; err != nil {
		return nil, err
	}
	domains := make([]*domain.MailDomain, 0, len(rows))
	for i := range rows {
		domains = append(domains, rows[i].toDomain())
	}
	return domains, nil
}

func (r *Repository) FindDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailDomainPO
	if err := r.conn(ctx).Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, r.errs.DomainNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *Repository) FindDomainByName(ctx context.Context, name string) (*domain.MailDomain, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailDomainPO
	if err := r.conn(ctx).Where("name = ?", strings.ToLower(name)).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, r.errs.DomainNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *Repository) UpdateDomain(ctx context.Context, mailDomain *domain.MailDomain) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po, err := mailDomainPO(mailDomain)
	if err != nil {
		return err
	}
	result := r.conn(ctx).Model(&MailDomainPO{}).
		Where("id = ? AND organization_id = ?", mailDomain.ID, mailDomain.OrganizationID).
		Updates(map[string]any{
			"sending_tag":     po.SendingTag,
			"status":          po.Status,
			"records":         po.Records,
			"last_error":      po.LastError,
			"last_error_code": po.LastErrorCode,
			"checked_at":      po.CheckedAt,
			"verified_at":     po.VerifiedAt,
			"updated_at":      time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return r.errs.DomainNotFound
	}
	return nil
}

// DeleteDomain refuses while a live mailbox has an address on the domain or a transactional
// template sends from it. Deleted mailboxes keep their address reserved but no longer hold the
// domain.
func (r *Repository) DeleteDomain(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var po MailDomainPO
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return r.errs.DomainNotFound
			}
			return err
		}
		// Only Zmail's own mailboxes hold the domain: a member's connected copy of an outside account
		// on it neither blocks the deletion nor may be revealed by it.
		var used int64
		if err := tx.Model(&mailboxRow{}).Where("address LIKE ? AND origin = ?", "%@"+escapeLike(po.Name),
			string(domain.MailboxOriginZmail)).Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			return r.errs.DomainInUse
		}
		// A transactional template sending from the domain holds it too.
		if err := tx.Table("mail_templates").Where("from_address LIKE ?", "%@"+escapeLike(po.Name)).Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			return r.errs.DomainInUse
		}
		return tx.Delete(&MailDomainPO{}, po.ID).Error
	})
}

func (r *Repository) SetMailboxRoutingRule(ctx context.Context, mailboxID uint, ruleID string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&mailboxRow{}).Where("id = ?", mailboxID).Update("routing_rule_id", ruleID).Error
}

// SetDomainCapability changes whether a domain receives through Zmail, and nothing else.
func (r *Repository) SetDomainCapability(ctx context.Context, organizationID, id uint, capability domain.MailDomainCapability) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailDomainPO{}).
		Where("id = ? AND organization_id = ?", id, organizationID).
		Updates(map[string]any{"capability": string(capability), "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return r.errs.DomainNotFound
	}
	return nil
}

// SetDefaultDomain takes the organization's domain lock, the one CreateDomain takes, so two
// choices at once cannot both pass the unique index on the chosen domain. A pending domain is
// refused, and the mark moves in one transaction.
func (r *Repository) SetDefaultDomain(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mail_domain_owner'), ?)`, int32(organizationID)).Error; err != nil { //nolint:gosec // lock key only needs to be stable per organization
			return err
		}
		var po MailDomainPO
		if err := tx.Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return r.errs.DomainNotFound
			}
			return err
		}
		if po.Status != string(domain.MailDomainStatusVerified) {
			return r.errs.DomainNotVerified
		}
		if po.IsDefault {
			return nil
		}
		now := time.Now()
		if err := tx.Model(&MailDomainPO{}).
			Where("organization_id = ? AND is_default AND id <> ?", organizationID, id).
			Updates(map[string]any{"is_default": false, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&MailDomainPO{}).Where("id = ?", id).
			Updates(map[string]any{"is_default": true, "updated_at": now}).Error
	})
}

// CountMailboxesByDomain counts live Zmail mailboxes only, as DeleteDomain does: a deleted mailbox
// keeps its address but no longer holds the domain, and a connected mailbox is an outside account.
func (r *Repository) CountMailboxesByDomain(ctx context.Context, organizationID uint) (map[string]int, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []struct {
		Domain string
		Total  int
	}
	if err := r.conn(ctx).Model(&mailboxRow{}).
		Select("lower(split_part(address, '@', 2)) AS domain, count(*) AS total").
		Where("organization_id = ? AND origin = ?", organizationID, string(domain.MailboxOriginZmail)).
		Group("lower(split_part(address, '@', 2))").Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.Domain] = row.Total
	}
	return counts, nil
}

// ListMailboxesWithoutRoutingRule returns live Zmail mailboxes only; a deleted mailbox keeps its
// address but receives nothing, and a connected mailbox's address is an outside account's, never
// routed through Zmail.
// fastbuild:bounded-list max=5000 reason=an organization owns at most domain.MailboxesPerOrganizationCeiling mailboxes
func (r *Repository) ListMailboxesWithoutRoutingRule(ctx context.Context, organizationID uint, domainName string) ([]*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []mailboxRow
	if err := r.conn(ctx).
		Where("organization_id = ? AND routing_rule_id = '' AND origin = ? AND address LIKE ?", organizationID,
			string(domain.MailboxOriginZmail), "%@"+escapeLike(strings.ToLower(domainName))).
		Order("id asc").Limit(domain.MailboxesPerOrganizationCeiling).Find(&rows).Error; err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	for i := range rows {
		mailboxes = append(mailboxes, rows[i].toDomain())
	}
	return mailboxes, nil
}
