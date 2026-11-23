package organization

import (
	"encoding/json"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MemberDeveloperGrantPO is one member's developer grant and its sending scope. The row exists
// only while the member holds the grant; owners and admins never need one.
type MemberDeveloperGrantPO struct {
	MembershipID   uint   `gorm:"primaryKey;autoIncrement:false"`
	OrganizationID uint   `gorm:"not null;index:idx_member_developer_grants_organization"`
	AllDomains     bool   `gorm:"not null;default:false"`
	ScopeDomains   string `gorm:"type:text;not null;default:'[]'"`
	GrantedBy      *uint
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

// TableName returns the stable developer grant table name.
func (MemberDeveloperGrantPO) TableName() string {
	return "member_developer_grants"
}

func (po *MemberDeveloperGrantPO) toDomain() *domain.DeveloperGrant {
	if po == nil {
		return nil
	}
	return developerGrantFromColumns(po.AllDomains, po.ScopeDomains)
}

// developerGrantFromColumns decodes a stored scope. An all-domains grant lists no domain.
func developerGrantFromColumns(allDomains bool, sendingDomains string) *domain.DeveloperGrant {
	grant := &domain.DeveloperGrant{AllDomains: allDomains, Domains: []string{}}
	if allDomains {
		return grant
	}
	var names []string
	if err := json.Unmarshal([]byte(sendingDomains), &names); err == nil {
		for _, name := range names {
			if name = domain.NormalizeDomainName(name); name != "" && !slices.Contains(grant.Domains, name) {
				grant.Domains = append(grant.Domains, name)
			}
		}
	}
	return grant
}

// normalizeDeveloperGrant cleans a requested scope: all domains lists none; otherwise at least one
// and at most MaxDeveloperGrantDomains names, lowercase, unique, and sorted.
func normalizeDeveloperGrant(grant domain.DeveloperGrant) (domain.DeveloperGrant, error) {
	if grant.AllDomains {
		if len(grant.Domains) > 0 {
			return domain.DeveloperGrant{}, domain.ErrInvalidInput
		}
		return domain.DeveloperGrant{AllDomains: true, Domains: []string{}}, nil
	}
	if len(grant.Domains) == 0 || len(grant.Domains) > domain.MaxDeveloperGrantDomains {
		return domain.DeveloperGrant{}, domain.ErrInvalidInput
	}
	names := make([]string, 0, len(grant.Domains))
	for _, name := range grant.Domains {
		name = domain.NormalizeDomainName(name)
		if name == "" || len(name) > 253 {
			return domain.DeveloperGrant{}, domain.ErrInvalidInput
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return domain.DeveloperGrant{Domains: names}, nil
}

// requireVerifiedOrganizationDomains refuses a domain that is not a verified domain of the
// organization in the mailbox starter's mail_domains table.
func requireVerifiedOrganizationDomains(tx *gorm.DB, organizationID uint, names []string) error {
	if len(names) == 0 {
		return nil
	}
	var verified []string
	if err := tx.Raw(`SELECT lower(name) FROM mail_domains WHERE organization_id = ? AND status = 'verified'`,
		organizationID).Scan(&verified).Error; err != nil {
		return err
	}
	for _, name := range names {
		if !slices.Contains(verified, name) {
			return domain.ErrDeveloperDomainNotAllowed
		}
	}
	return nil
}

// saveDeveloperGrant stores or replaces the grant of one membership.
func saveDeveloperGrant(tx *gorm.DB, organizationID, membershipID, grantedBy uint, grant domain.DeveloperGrant, now time.Time) (*MemberDeveloperGrantPO, error) {
	encoded, err := json.Marshal(grant.Domains)
	if err != nil {
		return nil, err
	}
	po := &MemberDeveloperGrantPO{
		MembershipID:   membershipID,
		OrganizationID: organizationID,
		AllDomains:     grant.AllDomains,
		ScopeDomains:   string(encoded),
		GrantedBy:      &grantedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "membership_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"all_domains", "scope_domains", "granted_by", "updated_at"}),
	}).Create(po).Error
	if err != nil {
		return nil, err
	}
	return po, nil
}
