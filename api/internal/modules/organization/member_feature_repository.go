package organization

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// UpdateMemberFeatures changes the named feature grants of one member. Only owners and admins
// may, and only for the member role: owners and admins always have every feature.
func (r *repository) UpdateMemberFeatures(
	ctx context.Context,
	organizationID, userID, memberID uint,
	patch domain.MemberFeaturePatch,
	now time.Time,
) (*domain.OrganizationMembershipFeatureChange, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	if organizationID == 0 || userID == 0 || memberID == 0 || patch.IsEmpty() || now.IsZero() {
		return nil, domain.ErrInvalidInput
	}

	var change *domain.OrganizationMembershipFeatureChange
	err = db.Transaction(func(tx *gorm.DB) error {
		actor, findErr := findMembershipForUserUpdate(tx, organizationID, userID)
		if findErr != nil {
			return findErr
		}
		if !domain.OrganizationRole(actor.Role).CanManageOrganization() {
			return domain.ErrPermissionDenied
		}

		target, findErr := findMembershipByIDUpdate(tx, organizationID, memberID)
		if findErr != nil {
			return findErr
		}
		if target.User.ID == 0 {
			return domain.ErrOrganizationMemberNotFound
		}
		if domain.OrganizationRole(target.Role) != domain.OrganizationRoleMember {
			return domain.ErrOrganizationMemberFeaturesFixed
		}

		before := target.features()
		beforeGrant := target.DeveloperGrant.toDomain()
		after := patch.Apply(before)
		if updateErr := updateDeveloperGrant(tx, organizationID, actor.UserID, target, patch, after.Developer, now); updateErr != nil {
			return updateErr
		}
		// The developer flag is the grant row, never a column.
		columnsBefore, columnsAfter := before, after
		columnsBefore.Developer, columnsAfter.Developer = false, false
		if columnsAfter != columnsBefore {
			columns := memberFeatureColumns(after)
			columns["updated_at"] = now
			result := tx.Model(&OrganizationMembershipPO{}).
				Where("id = ? AND organization_id = ? AND role = ?", target.ID, organizationID, target.Role).
				Updates(columns)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return domain.ErrConflict
			}
			target.CanUseBroadcasts, target.CanUseContacts = after.Broadcasts, after.Contacts
			target.CanUseTemplates, target.CanUseAPIKeys = after.Templates, after.APIKeys
			target.CanUseMailClients = after.MailClients
		}
		if after != before || patch.DeveloperGrant != nil {
			target.UpdatedAt = now
		}
		change = &domain.OrganizationMembershipFeatureChange{
			Membership: target.toDomain(), Before: before, BeforeGrant: beforeGrant,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return change, nil
}

// updateDeveloperGrant applies the developer part of a patch to the target's grant row. A new grant
// needs a scope; a scope alone replaces an existing grant's; turning the feature off removes the
// row. Every listed domain must be a verified domain of the organization.
func updateDeveloperGrant(
	tx *gorm.DB,
	organizationID, actorUserID uint,
	target *OrganizationMembershipPO,
	patch domain.MemberFeaturePatch,
	granted bool,
	now time.Time,
) error {
	if patch.Developer == nil && patch.DeveloperGrant == nil {
		return nil
	}
	if !granted {
		if patch.DeveloperGrant != nil && patch.Developer == nil {
			return domain.ErrInvalidInput
		}
		if patch.Developer != nil && patch.DeveloperGrant != nil {
			return domain.ErrInvalidInput
		}
		if target.DeveloperGrant == nil {
			return nil
		}
		if err := tx.Where("membership_id = ?", target.ID).Delete(&MemberDeveloperGrantPO{}).Error; err != nil {
			return err
		}
		target.DeveloperGrant = nil
		return nil
	}
	if patch.DeveloperGrant == nil {
		if target.DeveloperGrant == nil {
			return domain.ErrInvalidInput
		}
		return nil
	}
	grant, err := normalizeDeveloperGrant(*patch.DeveloperGrant)
	if err != nil {
		return err
	}
	if err := requireVerifiedOrganizationDomains(tx, organizationID, grant.Domains); err != nil {
		return err
	}
	saved, err := saveDeveloperGrant(tx, organizationID, target.ID, actorUserID, grant, now)
	if err != nil {
		return err
	}
	if target.DeveloperGrant != nil {
		saved.CreatedAt = target.DeveloperGrant.CreatedAt
	}
	target.DeveloperGrant = saved
	return nil
}
