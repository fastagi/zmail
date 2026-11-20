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

// MailDomainPO is the persistence object of a connected domain.
type MailDomainPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;index:idx_mail_domains_organization"`
	Name           string `gorm:"size:253;not null;uniqueIndex:idx_mail_domains_name"`
	ZoneID         string `gorm:"size:64;not null"`
	ZoneName       string `gorm:"size:253;not null"`
	MailZoneID     *uint  `gorm:"index:idx_mail_domains_mail_zone"`
	Capability     string `gorm:"size:20;not null;default:'send_receive'"`
	SendingTag     string `gorm:"size:64;not null;default:''"`
	Status         string `gorm:"size:20;not null;default:'pending'"`
	Records        string `gorm:"type:jsonb;not null;default:'[]'"`
	LastError      string `gorm:"size:500;not null;default:''"`
	CheckedAt      *time.Time
	VerifiedAt     *time.Time
	CreatedBy      uint `gorm:"not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailDomainPO) TableName() string {
	return "mail_domains"
}

func (po *MailDomainPO) toDomain() *domain.MailDomain {
	mailDomain := &domain.MailDomain{
		ID:             po.ID,
		OrganizationID: po.OrganizationID,
		Name:           po.Name,
		ZoneID:         po.ZoneID,
		ZoneName:       po.ZoneName,
		MailZoneID:     po.MailZoneID,
		Capability:     domain.MailDomainCapability(po.Capability),
		SendingTag:     po.SendingTag,
		Status:         domain.MailDomainStatus(po.Status),
		LastError:      po.LastError,
		CheckedAt:      po.CheckedAt,
		VerifiedAt:     po.VerifiedAt,
		CreatedBy:      po.CreatedBy,
		CreatedAt:      po.CreatedAt,
		UpdatedAt:      po.UpdatedAt,
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
		ID:             mailDomain.ID,
		OrganizationID: mailDomain.OrganizationID,
		Name:           mailDomain.Name,
		ZoneID:         mailDomain.ZoneID,
		ZoneName:       mailDomain.ZoneName,
		MailZoneID:     mailDomain.MailZoneID,
		Capability:     string(capability),
		SendingTag:     mailDomain.SendingTag,
		Status:         string(status),
		Records:        string(encoded),
		LastError:      truncateRunes(mailDomain.LastError, 500),
		CheckedAt:      mailDomain.CheckedAt,
		VerifiedAt:     mailDomain.VerifiedAt,
		CreatedBy:      mailDomain.CreatedBy,
		CreatedAt:      mailDomain.CreatedAt,
	}, nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func (r *repository) CreateDomain(ctx context.Context, mailDomain *domain.MailDomain, limit int) error {
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
			return ErrMailDomainLimitReached
		}
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrMailDomainTaken
			}
			return err
		}
		*mailDomain = *po.toDomain()
		return nil
	})
}

// fastbuild:bounded-list max=10 reason=an organization connects at most domain.MaxMailDomainsPerOrganization domains
func (r *repository) ListDomains(ctx context.Context, organizationID uint) ([]*domain.MailDomain, error) {
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

func (r *repository) FindDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailDomainPO
	if err := r.conn(ctx).Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailDomainNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) FindDomainByName(ctx context.Context, name string) (*domain.MailDomain, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailDomainPO
	if err := r.conn(ctx).Where("name = ?", strings.ToLower(name)).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailDomainNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) UpdateDomain(ctx context.Context, mailDomain *domain.MailDomain) error {
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
			"sending_tag": po.SendingTag,
			"status":      po.Status,
			"records":     po.Records,
			"last_error":  po.LastError,
			"checked_at":  po.CheckedAt,
			"verified_at": po.VerifiedAt,
			"updated_at":  time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailDomainNotFound
	}
	return nil
}

// DeleteDomain refuses while a live mailbox has an address on the domain or a transactional
// template sends from it. Deleted mailboxes keep their address reserved but no longer hold the
// domain.
func (r *repository) DeleteDomain(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var po MailDomainPO
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMailDomainNotFound
			}
			return err
		}
		var used int64
		if err := tx.Model(&MailboxPO{}).Where("address LIKE ?", "%@"+escapeLike(po.Name)).Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			return ErrMailDomainInUse
		}
		// A transactional template sending from the domain holds it too.
		if err := tx.Model(&MailTemplatePO{}).Where("from_address LIKE ?", "%@"+escapeLike(po.Name)).Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			return ErrMailDomainInUse
		}
		return tx.Delete(&MailDomainPO{}, po.ID).Error
	})
}

func (r *repository) SetMailboxRoutingRule(ctx context.Context, mailboxID uint, ruleID string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MailboxPO{}).Where("id = ?", mailboxID).Update("routing_rule_id", ruleID).Error
}

// SetDomainCapability changes whether a domain receives through Zmail, and nothing else.
func (r *repository) SetDomainCapability(ctx context.Context, organizationID, id uint, capability domain.MailDomainCapability) error {
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
		return ErrMailDomainNotFound
	}
	return nil
}

// ListMailboxesWithoutRoutingRule returns live mailboxes only; a deleted mailbox keeps its address
// but receives nothing.
// fastbuild:bounded-list max=5000 reason=an organization owns at most domain.MailboxesPerOrganizationCeiling mailboxes
func (r *repository) ListMailboxesWithoutRoutingRule(ctx context.Context, organizationID uint, domainName string) ([]*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailboxPO
	if err := r.conn(ctx).
		Where("organization_id = ? AND routing_rule_id = '' AND address LIKE ?", organizationID, "%@"+escapeLike(strings.ToLower(domainName))).
		Order("id asc").Limit(domain.MailboxesPerOrganizationCeiling).Find(&rows).Error; err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	for i := range rows {
		mailboxes = append(mailboxes, rows[i].toDomain())
	}
	return mailboxes, nil
}
