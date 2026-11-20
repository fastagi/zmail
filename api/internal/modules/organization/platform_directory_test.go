package organization

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/user"
)

// Spec acceptance row 1: two organizations with two and three members, one person in both, count
// 2 organizations, 2 and 3 members, and 4 distinct people. Deleted accounts never count; disabled
// ones count and are reported apart.
func TestPlatformDirectoryCountsDistinctMembers(t *testing.T) {
	db := newOrganizationRepositoryTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()
	alice := createOrganizationTestUser(t, db, "alice")
	bob := createOrganizationTestUser(t, db, "bob")
	carol := createOrganizationTestUser(t, db, "carol")
	dave := createOrganizationTestUser(t, db, "dave")
	gone := createOrganizationTestUser(t, db, "gone")

	north := &domain.Organization{Name: "North 100%", Slug: "north", CreatedBy: alice}
	require.NoError(t, repo.CreateWithOwner(ctx, north, &domain.OrganizationMembership{UserID: alice, Role: domain.OrganizationRoleOwner}))
	south := &domain.Organization{Name: "South", Slug: "south", CreatedBy: carol}
	require.NoError(t, repo.CreateWithOwner(ctx, south, &domain.OrganizationMembership{UserID: carol, Role: domain.OrganizationRoleOwner}))
	require.NoError(t, db.Create(&OrganizationPO{Name: "Empty", Slug: "empty", CreatedBy: alice}).Error)
	addMember := func(organizationID, userID uint) {
		require.NoError(t, db.Create(&OrganizationMembershipPO{
			OrganizationID: organizationID, UserID: userID, Role: string(domain.OrganizationRoleMember),
		}).Error)
	}
	addMember(north.ID, bob)  // north: alice, bob
	addMember(south.ID, bob)  // south: carol, bob, dave (bob in both)
	addMember(south.ID, dave) //
	addMember(south.ID, gone) // deleted below: never counted
	require.NoError(t, db.Model(&user.UserPO{}).Where("id = ?", gone).Update("deleted_at", time.Now()).Error)
	require.NoError(t, db.Model(&user.UserPO{}).Where("id = ?", dave).Update("status", 0).Error)

	totals, err := repo.PlatformMemberTotals(ctx)
	require.NoError(t, err)
	assert.Equal(t, domain.PlatformMemberTotals{Organizations: 3, Members: 4, DisabledMembers: 1}, totals)

	rows, err := repo.PlatformOrganizations(ctx, "")
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, domain.PlatformOrganizationRow{ID: north.ID, Name: "North 100%", Members: 2}, rows[0])
	assert.Equal(t, domain.PlatformOrganizationRow{ID: south.ID, Name: "South", Members: 3}, rows[1])
	assert.Equal(t, "Empty", rows[2].Name)
	assert.Zero(t, rows[2].Members, "an organization without members is still listed")

	matched, err := repo.PlatformOrganizations(ctx, "100%")
	require.NoError(t, err)
	require.Len(t, matched, 1, "wildcards in the query are literal")
	assert.Equal(t, north.ID, matched[0].ID)
	none, err := repo.PlatformOrganizations(ctx, "north_")
	require.NoError(t, err)
	assert.Empty(t, none)

	names, err := repo.PlatformOrganizationNames(ctx, []uint{south.ID, 999999})
	require.NoError(t, err)
	assert.Equal(t, map[uint]string{south.ID: "South"}, names)
}
