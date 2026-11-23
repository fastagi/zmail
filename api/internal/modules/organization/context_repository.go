package organization

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

type organizationContextRow struct {
	OrganizationID    uint   `gorm:"column:organization_id"`
	OrganizationName  string `gorm:"column:organization_name"`
	OrganizationSlug  string `gorm:"column:organization_slug"`
	MembershipID      uint   `gorm:"column:membership_id"`
	UserID            uint   `gorm:"column:user_id"`
	Role              string `gorm:"column:role"`
	CanUseBroadcasts  bool   `gorm:"column:can_use_broadcasts"`
	CanUseContacts    bool   `gorm:"column:can_use_contacts"`
	CanUseTemplates   bool   `gorm:"column:can_use_templates"`
	CanUseAPIKeys     bool   `gorm:"column:can_use_api_keys"`
	CanUseMailClients bool   `gorm:"column:can_use_mail_clients"`
	// DeveloperGrantID is the membership id of the developer grant, or nil without one.
	DeveloperGrantID      *uint   `gorm:"column:developer_grant_id"`
	DeveloperAllDomains   *bool   `gorm:"column:developer_all_domains"`
	DeveloperSendingNames *string `gorm:"column:developer_sending_domains"`
}

func (r *repository) ResolveContext(
	ctx context.Context,
	organizationID, userID uint,
) (*domain.OrganizationContext, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	if organizationID == 0 || userID == 0 {
		return nil, domain.ErrInvalidInput
	}

	var row organizationContextRow
	err = db.
		Table("organization_memberships AS memberships").
		Select(`
			memberships.organization_id,
			organizations.name AS organization_name,
			organizations.slug AS organization_slug,
			memberships.id AS membership_id,
			memberships.user_id,
			memberships.role,
			memberships.can_use_broadcasts,
			memberships.can_use_contacts,
			memberships.can_use_templates,
			memberships.can_use_api_keys,
			memberships.can_use_mail_clients,
			grants.membership_id AS developer_grant_id,
			grants.all_domains AS developer_all_domains,
			grants.sending_domains AS developer_sending_domains
		`).
		Joins("JOIN organizations ON organizations.id = memberships.organization_id").
		Joins("LEFT JOIN member_developer_grants AS grants ON grants.membership_id = memberships.id").
		Where("memberships.organization_id = ? AND memberships.user_id = ?", organizationID, userID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrOrganizationNotFound
	}
	if err != nil {
		return nil, err
	}

	resolved := &domain.OrganizationContext{
		OrganizationID:   row.OrganizationID,
		OrganizationName: row.OrganizationName,
		OrganizationSlug: row.OrganizationSlug,
		MembershipID:     row.MembershipID,
		UserID:           row.UserID,
		Role:             domain.OrganizationRole(row.Role),
		Features: domain.MemberFeatures{
			Broadcasts:  row.CanUseBroadcasts,
			Contacts:    row.CanUseContacts,
			Templates:   row.CanUseTemplates,
			APIKeys:     row.CanUseAPIKeys,
			MailClients: row.CanUseMailClients,
			Developer:   row.DeveloperGrantID != nil,
		},
	}
	if row.DeveloperGrantID != nil {
		allDomains, names := false, "[]"
		if row.DeveloperAllDomains != nil {
			allDomains = *row.DeveloperAllDomains
		}
		if row.DeveloperSendingNames != nil {
			names = *row.DeveloperSendingNames
		}
		resolved.DeveloperGrant = developerGrantFromColumns(allDomains, names)
	}
	if !resolved.IsValid() {
		return nil, domain.ErrServiceUnavailable
	}
	return resolved, nil
}
