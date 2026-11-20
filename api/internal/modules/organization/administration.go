package organization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// administrationStore is the persistence of the operator administration seam; the repository
// implements it.
type administrationStore interface {
	findOrganizationManagers(ctx context.Context, organizationID uint) (*domain.Organization, []*domain.OrganizationMembership, error)
	findMembership(ctx context.Context, organizationID, userID uint) (*domain.OrganizationMembership, error)
	grantAdmin(ctx context.Context, organizationID, userID uint, now time.Time) (*domain.OrganizationAdminGrant, error)
}

var (
	_ domain.OrganizationAdministrator = (*service)(nil)
	_ administrationStore              = (*repository)(nil)
)

// FindOrganizationManagers returns the organization with its owners and admins for operator tooling.
func (s *service) FindOrganizationManagers(ctx context.Context, organizationID uint) (*domain.Organization, []*domain.OrganizationMembership, error) {
	if organizationID == 0 {
		return nil, nil, domain.ErrInvalidInput
	}
	if s.administration == nil {
		return nil, nil, domain.ErrServiceUnavailable
	}
	return s.administration.findOrganizationManagers(ctx, organizationID)
}

// FindMembership returns the user's membership, or nil when the user is not a member.
func (s *service) FindMembership(ctx context.Context, organizationID, userID uint) (*domain.OrganizationMembership, error) {
	if organizationID == 0 || userID == 0 {
		return nil, domain.ErrInvalidInput
	}
	if s.administration == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return s.administration.findMembership(ctx, organizationID, userID)
}

// GrantAdmin makes the user an admin of the organization and announces a created membership as
// joined after it commits. Auditing is the caller's: operator tooling has no request audit entry.
func (s *service) GrantAdmin(ctx context.Context, organizationID, userID uint) (*domain.OrganizationAdminGrant, error) {
	if organizationID == 0 || userID == 0 {
		return nil, domain.ErrInvalidInput
	}
	if s.administration == nil {
		return nil, domain.ErrServiceUnavailable
	}
	grant, err := s.administration.grantAdmin(ctx, organizationID, userID, s.now())
	if err != nil {
		return nil, fmt.Errorf("grant organization admin: %w", err)
	}
	if grant.Created() {
		membership := grant.Membership
		s.publish(ctx, domain.NewOrganizationMemberJoinedEvent(membership.OrganizationID, membership.UserID, membership.ID, s.now()))
	}
	return grant, nil
}

func (r *repository) findOrganizationManagers(ctx context.Context, organizationID uint) (*domain.Organization, []*domain.OrganizationMembership, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	var organization OrganizationPO
	if findErr := db.First(&organization, organizationID).Error; findErr != nil {
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			return nil, nil, domain.ErrOrganizationNotFound
		}
		return nil, nil, findErr
	}
	var rows []OrganizationMembershipPO
	if findErr := operatorMemberScope(db).
		Preload("User").
		Where("organization_memberships.organization_id = ? AND organization_memberships.role IN ?",
			organizationID, []string{string(domain.OrganizationRoleOwner), string(domain.OrganizationRoleAdmin)}).
		Order(clause.Expr{SQL: "CASE WHEN organization_memberships.role = ? THEN 0 ELSE 1 END, organization_memberships.created_at, organization_memberships.id",
			Vars: []any{string(domain.OrganizationRoleOwner)}}).
		Find(&rows).Error; findErr != nil {
		return nil, nil, findErr
	}
	managers := make([]*domain.OrganizationMembership, 0, len(rows))
	for index := range rows {
		managers = append(managers, membershipWithEmail(&rows[index]))
	}
	return organization.toDomain(), managers, nil
}

func (r *repository) findMembership(ctx context.Context, organizationID, userID uint) (*domain.OrganizationMembership, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	var row OrganizationMembershipPO
	findErr := db.Where("organization_id = ? AND user_id = ?", organizationID, userID).Take(&row).Error
	if errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if findErr != nil {
		return nil, findErr
	}
	return row.toDomain(), nil
}

// grantAdmin locks the organization and the account, then creates an admin membership or raises a
// member, in one transaction. An admin or owner is returned unchanged.
func (r *repository) grantAdmin(ctx context.Context, organizationID, userID uint, now time.Time) (*domain.OrganizationAdminGrant, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	var grant *domain.OrganizationAdminGrant
	err = db.Transaction(func(tx *gorm.DB) error {
		var organization OrganizationPO
		if findErr := forUpdate(tx).First(&organization, organizationID).Error; findErr != nil {
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				return domain.ErrOrganizationNotFound
			}
			return findErr
		}
		if _, userErr := findUndeletedUserForUpdate(tx, userID); userErr != nil {
			if errors.Is(userErr, gorm.ErrRecordNotFound) {
				return domain.ErrUserNotFound
			}
			return userErr
		}
		var membership OrganizationMembershipPO
		findErr := forUpdate(tx).Where("organization_id = ? AND user_id = ?", organizationID, userID).Take(&membership).Error
		switch {
		case errors.Is(findErr, gorm.ErrRecordNotFound):
			membership = OrganizationMembershipPO{
				OrganizationID: organizationID, UserID: userID, Role: string(domain.OrganizationRoleAdmin),
				CreatedAt: now, UpdatedAt: now,
			}
			if createErr := tx.Omit(clause.Associations).Create(&membership).Error; createErr != nil {
				if isUniqueViolation(createErr) {
					return domain.ErrOrganizationMemberAlreadyExists
				}
				return createErr
			}
			membership.Organization = organization
			grant = &domain.OrganizationAdminGrant{Membership: membership.toDomain()}
			return nil
		case findErr != nil:
			return findErr
		}
		before := domain.OrganizationRole(membership.Role)
		if before == domain.OrganizationRoleMember {
			if updateErr := tx.Model(&OrganizationMembershipPO{}).
				Where("id = ? AND role = ?", membership.ID, membership.Role).
				Updates(map[string]any{"role": string(domain.OrganizationRoleAdmin), "updated_at": now}).Error; updateErr != nil {
				return updateErr
			}
			membership.Role, membership.UpdatedAt = string(domain.OrganizationRoleAdmin), now
		}
		membership.Organization = organization
		grant = &domain.OrganizationAdminGrant{Membership: membership.toDomain(), BeforeRole: before}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return grant, nil
}

// membershipWithEmail converts a membership whose User was preloaded, keeping the account email
// that toDomain leaves out.
func membershipWithEmail(row *OrganizationMembershipPO) *domain.OrganizationMembership {
	membership := row.toDomain()
	if membership.User != nil {
		membership.User.Email = row.User.Email
	}
	return membership
}
