package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailRolePO is an organization's mail role; names are unique per organization ignoring case.
type MailRolePO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;uniqueIndex:idx_mail_roles_name,priority:1"`
	Name           string `gorm:"size:64;not null;uniqueIndex:idx_mail_roles_name,priority:2,expression:lower(name)"`
	Color          string `gorm:"size:16;not null;default:'gray'"`
	Description    string `gorm:"size:280;not null;default:''"`
	OwnerUserID    *uint
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailRolePO) TableName() string {
	return "mail_roles"
}

func (po *MailRolePO) toDomain() *domain.MailRole {
	return &domain.MailRole{
		ID: po.ID, OrganizationID: po.OrganizationID, Name: po.Name, Color: domain.MailLabelColor(po.Color),
		Description: po.Description, OwnerUserID: po.OwnerUserID, CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt,
	}
}

// The service takes the role seam from its repository by type assertion; this keeps the real
// repository from silently losing it.
var _ domain.MailRoleRepository = (*repository)(nil)

// ListMailRoles lists the organization's roles by name ignoring case, then id. An organization
// has at most domain.MaxMailRolesPerOrganization of them.
func (r *repository) ListMailRoles(ctx context.Context, organizationID uint) ([]*domain.MailRole, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailRolePO
	err := r.conn(ctx).Where("organization_id = ?", organizationID).
		Order("lower(name) ASC, id ASC").Limit(domain.MaxMailRolesPerOrganization).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	roles := make([]*domain.MailRole, 0, len(rows))
	for i := range rows {
		roles = append(roles, rows[i].toDomain())
	}
	return roles, nil
}

// FindMailRole returns the organization's role; a role of another organization is not found.
func (r *repository) FindMailRole(ctx context.Context, organizationID, id uint) (*domain.MailRole, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return findMailRole(r.conn(ctx), organizationID, id)
}

func findMailRole(db *gorm.DB, organizationID, id uint) (*domain.MailRole, error) {
	var po MailRolePO
	if err := db.Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailRoleNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

// CreateMailRole stores a role while holding a per-organization advisory lock, so two concurrent
// creates cannot both pass the limit. A name taken ignoring case is refused by the unique index.
func (r *repository) CreateMailRole(ctx context.Context, role *domain.MailRole, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mail_roles'), ?)`, int32(role.OrganizationID)).Error; err != nil { //nolint:gosec // lock key only needs to be stable per organization
			return err
		}
		var owned int64
		if err := tx.Model(&MailRolePO{}).Where("organization_id = ?", role.OrganizationID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return ErrMailRoleLimitReached
		}
		po := &MailRolePO{
			OrganizationID: role.OrganizationID, Name: role.Name, Color: string(role.Color),
			Description: role.Description, OwnerUserID: role.OwnerUserID,
		}
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrMailRoleNameTaken
			}
			return err
		}
		*role = *po.toDomain()
		return nil
	})
}

// UpdateMailRole replaces the role's name, color, description, and owner; the last writer wins.
// Renaming a role to its own name in another case keeps it, since the unique index compares the
// row with itself. The stored role is read back into role.
func (r *repository) UpdateMailRole(ctx context.Context, role *domain.MailRole) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailRolePO{}).
		Where("id = ? AND organization_id = ?", role.ID, role.OrganizationID).
		Updates(map[string]any{
			"name": role.Name, "color": string(role.Color), "description": role.Description,
			"owner_user_id": role.OwnerUserID, "updated_at": gorm.Expr("now()"),
		})
	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			return ErrMailRoleNameTaken
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailRoleNotFound
	}
	stored, err := findMailRole(r.conn(ctx), role.OrganizationID, role.ID)
	if err != nil {
		return err
	}
	*role = *stored
	return nil
}

// DeleteMailRole deletes the role and leaves its mailboxes ungrouped. The schema's ON DELETE SET
// NULL ungroups them too; doing it here as well keeps the result where the foreign key is absent.
// Locking the role first makes a concurrent assignment wait and then find the role gone.
func (r *repository) DeleteMailRole(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		found, err := lockMailRole(tx, organizationID, id, "UPDATE")
		if err != nil {
			return err
		}
		if !found {
			return ErrMailRoleNotFound
		}
		if err := tx.Exec(`UPDATE mailboxes SET role_id = NULL WHERE role_id = ?`, id).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM mail_roles WHERE id = ? AND organization_id = ?`, id, organizationID).Error
	})
}

// AssignMailboxRole puts a live mailbox of the organization under one of the organization's roles,
// or ungroups it when roleID is nil. Assigning the role a mailbox already has succeeds again. The
// role is held FOR KEY SHARE until the update commits, so a concurrent delete either finishes first
// (the role is then not found) or waits for the assignment and ungroups the mailbox itself. A
// missing mailbox answers before a missing role.
func (r *repository) AssignMailboxRole(ctx context.Context, organizationID, mailboxID uint, roleID *uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		roleFound := true
		if roleID != nil {
			var err error
			if roleFound, err = lockMailRole(tx, organizationID, *roleID, "KEY SHARE"); err != nil {
				return err
			}
		}
		// Point role_id only at a role the check above found, so a role of another organization
		// never reaches the column even though the foreign key would accept it.
		assigned := roleID
		if !roleFound {
			assigned = nil
		}
		result := tx.Exec(`UPDATE mailboxes SET role_id = ?, updated_at = now()
			WHERE id = ? AND organization_id = ? AND deleted_at IS NULL`, assigned, mailboxID, organizationID)
		if result.Error != nil {
			if isForeignKeyViolation(result.Error) {
				return ErrMailRoleNotFound
			}
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMailboxNotFound
		}
		if !roleFound {
			// Roll the update back: the mailbox exists, the role does not.
			return ErrMailRoleNotFound
		}
		return nil
	})
}

// lockMailRole locks the organization's role with the given row lock strength and reports whether
// it exists.
func lockMailRole(tx *gorm.DB, organizationID, id uint, strength string) (bool, error) {
	var found []uint
	err := tx.Model(&MailRolePO{}).Where("id = ? AND organization_id = ?", id, organizationID).
		Clauses(clause.Locking{Strength: strength}).Pluck("id", &found).Error
	return len(found) > 0, err
}

// ClearMailRoleOwner removes the user as owner of every role of the organization, for a member
// who left it.
func (r *repository) ClearMailRoleOwner(ctx context.Context, organizationID, userID uint) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Exec(`UPDATE mail_roles SET owner_user_id = NULL, updated_at = now()
		WHERE organization_id = ? AND owner_user_id = ?`, organizationID, userID)
	return result.RowsAffected, result.Error
}
