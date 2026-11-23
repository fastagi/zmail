package webhook

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/organization"
)

// ownedEndpointCondition limits deliveries to the endpoints one user created.
const ownedEndpointCondition = "endpoint_id IN (SELECT id FROM webhook_endpoints WHERE organization_id = ? AND created_by = ?)"

type endpointOwnerKey struct{}

// withEndpointOwner limits every endpoint, delivery, and attempt the repository reads or changes
// to those of endpoints created by userID. Owners and admins act without it.
func withEndpointOwner(ctx context.Context, userID uint) context.Context {
	return context.WithValue(ctx, endpointOwnerKey{}, userID)
}

func endpointOwnerFromContext(ctx context.Context) uint {
	if ctx == nil {
		return 0
	}
	owner, _ := ctx.Value(endpointOwnerKey{}).(uint)
	return owner
}

type endpointCreator struct {
	UserID       uint    `gorm:"column:user_id"`
	Role         string  `gorm:"column:role"`
	GrantID      *uint   `gorm:"column:grant_id"`
	AllDomains   *bool   `gorm:"column:all_domains"`
	ScopeDomains *string `gorm:"column:scope_domains"`
}

// endpointsInAudience keeps the endpoints whose creator may receive the event now. Endpoints of
// current owners and admins receive everything; an endpoint created by anyone else receives only
// programmatic mail whose sender domain its creator's developer grant covers, and nothing once
// the creator has no grant or has left the organization.
func endpointsInAudience(tx *gorm.DB, mutation publicationMutation, endpoints []EndpointPO) ([]EndpointPO, error) {
	if len(endpoints) == 0 {
		return endpoints, nil
	}
	creatorIDs := make([]uint, 0, len(endpoints))
	for index := range endpoints {
		creatorIDs = append(creatorIDs, endpoints[index].CreatedBy)
	}
	var creators []endpointCreator
	if err := tx.Table("organization_memberships AS memberships").
		Select(`memberships.user_id, memberships.role, grants.membership_id AS grant_id,
			grants.all_domains, grants.scope_domains`).
		Joins("LEFT JOIN member_developer_grants AS grants ON grants.membership_id = memberships.id").
		Where("memberships.organization_id = ? AND memberships.user_id IN ?", mutation.OrganizationID, creatorIDs).
		Scan(&creators).Error; err != nil {
		return nil, fmt.Errorf("find webhook endpoint creators: %w", err)
	}
	byUser := make(map[uint]endpointCreator, len(creators))
	for _, creator := range creators {
		byUser[creator.UserID] = creator
	}
	kept := make([]EndpointPO, 0, len(endpoints))
	for index := range endpoints {
		creator, member := byUser[endpoints[index].CreatedBy]
		if member && creatorReceives(creator, mutation.Origin) {
			kept = append(kept, endpoints[index])
		}
	}
	return kept, nil
}

func creatorReceives(creator endpointCreator, origin *domain.WebhookEventOrigin) bool {
	if domain.OrganizationRole(creator.Role).CanManageOrganization() {
		return true
	}
	if creator.GrantID == nil || origin == nil || !origin.Programmatic {
		return false
	}
	allDomains, names := false, "[]"
	if creator.AllDomains != nil {
		allDomains = *creator.AllDomains
	}
	if creator.ScopeDomains != nil {
		names = *creator.ScopeDomains
	}
	return organization.DecodeDeveloperGrant(allDomains, names).AllowsDomain(origin.SenderDomain, origin.SenderDomainVerified)
}
