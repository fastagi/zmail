package organization

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/user"
)

func (r *repository) ListMembers(
	ctx context.Context,
	organizationID, userID uint,
	page, pageSize int,
) ([]*domain.OrganizationMembership, int64, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	if organizationID == 0 || userID == 0 || page < 1 || pageSize < 1 {
		return nil, 0, domain.ErrInvalidInput
	}

	var visible int64
	if err := db.Model(&OrganizationMembershipPO{}).
		Where("organization_id = ? AND user_id = ?", organizationID, userID).
		Count(&visible).Error; err != nil {
		return nil, 0, err
	}
	if visible == 0 {
		return nil, 0, domain.ErrOrganizationNotFound
	}

	query := db.Model(&OrganizationMembershipPO{}).
		Joins("JOIN users AS member_users ON member_users.id = organization_memberships.user_id AND member_users.deleted_at IS NULL").
		Where("organization_memberships.organization_id = ?", organizationID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var memberships []*OrganizationMembershipPO
	if err := query.
		Preload("User").
		Order("organization_memberships.user_id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&memberships).Error; err != nil {
		return nil, 0, err
	}

	items := make([]*domain.OrganizationMembership, len(memberships))
	for index, membership := range memberships {
		items[index] = membership.toDomain()
	}
	return items, total, nil
}

func (r *repository) ChangeMemberRole(
	ctx context.Context,
	organizationID, userID, memberID uint,
	role domain.OrganizationRole,
	now time.Time,
) (*domain.OrganizationMembershipRoleChange, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	if organizationID == 0 || userID == 0 || memberID == 0 || !role.CanBeInvited() || now.IsZero() {
		return nil, domain.ErrInvalidInput
	}

	var change *domain.OrganizationMembershipRoleChange
	err = db.Transaction(func(tx *gorm.DB) error {
		actor, findErr := findMembershipForUserUpdate(tx, organizationID, userID)
		if findErr != nil {
			return findErr
		}
		if !domain.OrganizationRole(actor.Role).CanChangeMemberRoles() {
			return domain.ErrPermissionDenied
		}

		target, findErr := findMembershipByIDUpdate(tx, organizationID, memberID)
		if findErr != nil {
			return findErr
		}
		if target.User.ID == 0 {
			return domain.ErrOrganizationMemberNotFound
		}
		before := domain.OrganizationRole(target.Role)
		if before == domain.OrganizationRoleOwner {
			return domain.ErrOrganizationOwnershipTransferRequired
		}
		if before == role {
			change = &domain.OrganizationMembershipRoleChange{
				Membership: target.toDomain(),
				BeforeRole: before,
			}
			return nil
		}

		result := tx.Model(&OrganizationMembershipPO{}).
			Where("id = ? AND organization_id = ? AND role = ?", target.ID, organizationID, target.Role).
			Updates(map[string]any{"role": role, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return domain.ErrConflict
		}
		target.Role = string(role)
		target.UpdatedAt = now
		change = &domain.OrganizationMembershipRoleChange{
			Membership: target.toDomain(),
			BeforeRole: before,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return change, nil
}

func (r *repository) RemoveMember(
	ctx context.Context,
	organizationID, userID, memberID uint,
) (*domain.OrganizationMembership, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	if organizationID == 0 || userID == 0 || memberID == 0 {
		return nil, domain.ErrInvalidInput
	}

	var removed *domain.OrganizationMembership
	err = db.Transaction(func(tx *gorm.DB) error {
		actor, findErr := findMembershipForUserUpdate(tx, organizationID, userID)
		if findErr != nil {
			return findErr
		}
		target, findErr := findMembershipByIDUpdate(tx, organizationID, memberID)
		if findErr != nil {
			return findErr
		}

		actorRole := domain.OrganizationRole(actor.Role)
		targetRole := domain.OrganizationRole(target.Role)
		if actor.ID == target.ID {
			if actorRole == domain.OrganizationRoleOwner {
				return domain.ErrOrganizationOwnershipTransferRequired
			}
		} else if !actorRole.CanRemoveMember(targetRole) {
			return domain.ErrPermissionDenied
		}

		result := tx.Where("id = ? AND organization_id = ? AND role = ?", target.ID, organizationID, target.Role).
			Delete(&OrganizationMembershipPO{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return domain.ErrOrganizationMemberNotFound
		}
		removed = target.toDomain()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

func (r *repository) TransferOwnership(
	ctx context.Context,
	organizationID, userID, newOwnerMemberID uint,
	now time.Time,
) (*domain.OrganizationOwnershipTransfer, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	if organizationID == 0 || userID == 0 || newOwnerMemberID == 0 || now.IsZero() {
		return nil, domain.ErrInvalidInput
	}

	var transfer *domain.OrganizationOwnershipTransfer
	err = db.Transaction(func(tx *gorm.DB) error {
		actor, findErr := findMembershipForUserUpdate(tx, organizationID, userID)
		if findErr != nil {
			return findErr
		}
		if domain.OrganizationRole(actor.Role) != domain.OrganizationRoleOwner {
			return domain.ErrPermissionDenied
		}

		target, findErr := findMembershipByIDUpdate(tx, organizationID, newOwnerMemberID)
		if findErr != nil {
			return findErr
		}
		targetRole := domain.OrganizationRole(target.Role)
		if actor.ID == target.ID || !targetRole.CanBeInvited() || target.User.ID == 0 {
			return domain.ErrOrganizationOwnershipTransferTargetInvalid
		}

		previousOwner := tx.Model(&OrganizationMembershipPO{}).
			Where("id = ? AND organization_id = ? AND role = ?", actor.ID, organizationID, domain.OrganizationRoleOwner).
			Updates(map[string]any{"role": domain.OrganizationRoleAdmin, "updated_at": now})
		if previousOwner.Error != nil {
			return previousOwner.Error
		}
		if previousOwner.RowsAffected != 1 {
			return domain.ErrPermissionDenied
		}

		newOwner := tx.Model(&OrganizationMembershipPO{}).
			Where("id = ? AND organization_id = ? AND role = ?", target.ID, organizationID, target.Role).
			Updates(map[string]any{"role": domain.OrganizationRoleOwner, "updated_at": now})
		if newOwner.Error != nil {
			return newOwner.Error
		}
		if newOwner.RowsAffected != 1 {
			return domain.ErrOrganizationOwnershipTransferTargetInvalid
		}

		actor.Role = string(domain.OrganizationRoleAdmin)
		actor.UpdatedAt = now
		target.Role = string(domain.OrganizationRoleOwner)
		target.UpdatedAt = now
		transfer = &domain.OrganizationOwnershipTransfer{
			PreviousOwner:      actor.toDomain(),
			NewOwner:           target.toDomain(),
			NewOwnerBeforeRole: targetRole,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return transfer, nil
}

func (r *repository) CountMembershipsForUser(ctx context.Context, userID uint) (int64, int64, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return 0, 0, err
	}
	if userID == 0 {
		return 0, 0, domain.ErrInvalidInput
	}

	var counts struct {
		Total int64
		Owned int64
	}
	err = db.Model(&OrganizationMembershipPO{}).
		Select(
			"COUNT(*) AS total, COALESCE(SUM(CASE WHEN role = ? THEN 1 ELSE 0 END), 0) AS owned",
			domain.OrganizationRoleOwner,
		).
		Where("user_id = ?", userID).
		Scan(&counts).Error
	return counts.Total, counts.Owned, err
}

func findMembershipForUserUpdate(tx *gorm.DB, organizationID, userID uint) (*OrganizationMembershipPO, error) {
	var membership OrganizationMembershipPO
	err := forUpdate(tx).
		Preload("User").
		Where("organization_id = ? AND user_id = ?", organizationID, userID).
		First(&membership).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrOrganizationNotFound
	}
	if err != nil {
		return nil, err
	}
	return &membership, nil
}

func findMembershipByIDUpdate(tx *gorm.DB, organizationID, memberID uint) (*OrganizationMembershipPO, error) {
	var membership OrganizationMembershipPO
	err := forUpdate(tx).
		Preload("User").
		Where("organization_id = ? AND id = ?", organizationID, memberID).
		First(&membership).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrOrganizationMemberNotFound
	}
	if err != nil {
		return nil, err
	}
	return &membership, nil
}

func findUndeletedUserForUpdate(tx *gorm.DB, userID uint) (*user.UserPO, error) {
	var po user.UserPO
	if err := forUpdate(tx).First(&po, userID).Error; err != nil {
		return nil, err
	}
	return &po, nil
}

func forUpdate(tx *gorm.DB) *gorm.DB {
	if tx == nil {
		return tx
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

// membershipLookupChunk bounds the values of one IN list, well below PostgreSQL's parameter limit.
const membershipLookupChunk = 1000

// FindMembershipsByEmails matches account emails of the organization's members only, in chunks.
// It is the one read that returns member emails; only server-side mailbox imports and exports use it.
func (r *repository) FindMembershipsByEmails(ctx context.Context, organizationID uint, emails []string) ([]*domain.OrganizationMembership, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(emails))
	lowered := make([]string, 0, len(emails))
	for _, email := range emails {
		email = strings.ToLower(strings.TrimSpace(email))
		if _, duplicate := seen[email]; email == "" || duplicate {
			continue
		}
		seen[email] = struct{}{}
		lowered = append(lowered, email)
	}
	found := make([]*domain.OrganizationMembership, 0, len(lowered))
	for start := 0; start < len(lowered); start += membershipLookupChunk {
		chunk := lowered[start:min(start+membershipLookupChunk, len(lowered))]
		var rows []*OrganizationMembershipPO
		if err := db.Model(&OrganizationMembershipPO{}).
			Joins("JOIN users AS member_users ON member_users.id = organization_memberships.user_id AND member_users.deleted_at IS NULL").
			Where("organization_memberships.organization_id = ? AND LOWER(member_users.email) IN ?", organizationID, chunk).
			Preload("User").
			Order("organization_memberships.user_id ASC").
			Find(&rows).Error; err != nil {
			return nil, err
		}
		found = append(found, withEmails(rows)...)
	}
	return found, nil
}

// ListMembershipsByUserIDs reads the organization's memberships of userIDs in chunks, with account
// emails; only server-side mailbox exports use it.
func (r *repository) ListMembershipsByUserIDs(ctx context.Context, organizationID uint, userIDs []uint) ([]*domain.OrganizationMembership, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[uint]struct{}, len(userIDs))
	ids := make([]uint, 0, len(userIDs))
	for _, id := range userIDs {
		if _, duplicate := seen[id]; id == 0 || duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	found := make([]*domain.OrganizationMembership, 0, len(ids))
	for start := 0; start < len(ids); start += membershipLookupChunk {
		chunk := ids[start:min(start+membershipLookupChunk, len(ids))]
		var rows []*OrganizationMembershipPO
		if err := db.Model(&OrganizationMembershipPO{}).
			Where("organization_id = ? AND user_id IN ?", organizationID, chunk).
			Preload("User").
			Order("user_id ASC").
			Find(&rows).Error; err != nil {
			return nil, err
		}
		found = append(found, withEmails(rows)...)
	}
	return found, nil
}

// ListManagerUserIDs returns the user ids of the organization's owners and admins, ascending.
func (r *repository) ListManagerUserIDs(ctx context.Context, organizationID uint) ([]uint, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	var ids []uint
	err = db.Model(&OrganizationMembershipPO{}).
		Where("organization_id = ? AND role IN ?", organizationID,
			[]domain.OrganizationRole{domain.OrganizationRoleOwner, domain.OrganizationRoleAdmin}).
		Order("user_id ASC").
		Pluck("user_id", &ids).Error
	return ids, err
}

// withEmails converts memberships and keeps each user's account email, which toDomain leaves out.
func withEmails(rows []*OrganizationMembershipPO) []*domain.OrganizationMembership {
	memberships := make([]*domain.OrganizationMembership, 0, len(rows))
	for _, row := range rows {
		membership := row.toDomain()
		if membership.User != nil {
			membership.User.Email = row.User.Email
		}
		memberships = append(memberships, membership)
	}
	return memberships
}
