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
		after := patch.Apply(before)
		if after != before {
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
			target.UpdatedAt = now
		}
		change = &domain.OrganizationMembershipFeatureChange{Membership: target.toDomain(), Before: before}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return change, nil
}
