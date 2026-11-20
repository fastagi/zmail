package organization

import (
	"context"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.PlatformOrganizationDirectory = (*repository)(nil)

// PlatformMemberTotals counts organizations and the distinct users who belong to any of them.
func (r *repository) PlatformMemberTotals(ctx context.Context) (domain.PlatformMemberTotals, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return domain.PlatformMemberTotals{}, err
	}
	var totals domain.PlatformMemberTotals
	if err := db.Model(&OrganizationPO{}).Count(&totals.Organizations).Error; err != nil {
		return domain.PlatformMemberTotals{}, err
	}
	var members struct {
		Members         int64
		DisabledMembers int64
	}
	if err := operatorMemberScope(db).
		Select(`COUNT(DISTINCT organization_memberships.user_id) AS members,
			COUNT(DISTINCT organization_memberships.user_id) FILTER (WHERE member_users.status <> 1) AS disabled_members`).
		Scan(&members).Error; err != nil {
		return domain.PlatformMemberTotals{}, err
	}
	totals.Members = members.Members
	totals.DisabledMembers = members.DisabledMembers
	return totals, nil
}

// PlatformOrganizations returns every organization whose name contains query, ordered by ID, with
// its member count. Memberships are counted on their own, never joined to other aggregates.
func (r *repository) PlatformOrganizations(ctx context.Context, query string) ([]domain.PlatformOrganizationRow, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	scope := db.Model(&OrganizationPO{})
	if query = strings.TrimSpace(query); query != "" {
		pattern := "%" + escapeOperatorLike(strings.ToLower(query)) + "%"
		scope = scope.Where(`LOWER(name) LIKE ? ESCAPE '\'`, pattern)
	}
	var organizations []OrganizationPO
	if findErr := scope.Select("id", "name").Order("id ASC").Find(&organizations).Error; findErr != nil {
		return nil, findErr
	}
	ids := make([]uint, len(organizations))
	for index := range organizations {
		ids[index] = organizations[index].ID
	}
	counts, err := operatorMemberCounts(db, ids)
	if err != nil {
		return nil, err
	}
	rows := make([]domain.PlatformOrganizationRow, len(organizations))
	for index := range organizations {
		rows[index] = domain.PlatformOrganizationRow{
			ID:      organizations[index].ID,
			Name:    organizations[index].Name,
			Members: counts[organizations[index].ID],
		}
	}
	return rows, nil
}

// PlatformOrganizationNames returns the names of the given organizations.
func (r *repository) PlatformOrganizationNames(ctx context.Context, ids []uint) (map[uint]string, error) {
	names := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, err
	}
	var organizations []OrganizationPO
	if err := db.Select("id", "name").Where("id IN ?", ids).Find(&organizations).Error; err != nil {
		return nil, err
	}
	for index := range organizations {
		names[organizations[index].ID] = organizations[index].Name
	}
	return names, nil
}
