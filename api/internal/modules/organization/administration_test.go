package organization

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.OrganizationAdministrator = (*service)(nil)

func addOrganizationTestMember(t *testing.T, db *gorm.DB, organizationID, userID uint, role domain.OrganizationRole, joined time.Time) {
	t.Helper()
	require.NoError(t, db.Omit(clause.Associations).Create(&OrganizationMembershipPO{
		OrganizationID: organizationID, UserID: userID, Role: string(role), CreatedAt: joined, UpdatedAt: joined,
	}).Error)
}

type administrationFixture struct {
	db           *gorm.DB
	repo         *repository
	organization *domain.Organization
	owner        uint
	admin        uint
	member       uint
	outsider     uint
}

func newAdministrationFixture(t *testing.T) *administrationFixture {
	t.Helper()
	db := newOrganizationRepositoryTestDB(t)
	repo := NewRepository(db)
	f := &administrationFixture{db: db, repo: repo}
	f.owner = createOrganizationTestUser(t, db, "owner")
	f.admin = createOrganizationTestUser(t, db, "admin")
	f.member = createOrganizationTestUser(t, db, "member")
	f.outsider = createOrganizationTestUser(t, db, "outsider")
	f.organization = &domain.Organization{Name: "Acme", Slug: "acme", CreatedBy: f.owner}
	require.NoError(t, repo.CreateWithOwner(context.Background(), f.organization,
		&domain.OrganizationMembership{UserID: f.owner, Role: domain.OrganizationRoleOwner}))
	joined := time.Now().UTC().Add(time.Hour)
	addOrganizationTestMember(t, db, f.organization.ID, f.member, domain.OrganizationRoleMember, joined)
	addOrganizationTestMember(t, db, f.organization.ID, f.admin, domain.OrganizationRoleAdmin, joined.Add(time.Minute))
	return f
}

func TestRepositoryFindOrganizationManagersListsOwnersFirstWithEmails(t *testing.T) {
	f := newAdministrationFixture(t)

	organization, managers, err := f.repo.findOrganizationManagers(context.Background(), f.organization.ID)
	require.NoError(t, err)
	assert.Equal(t, "Acme", organization.Name)
	assert.Equal(t, "acme", organization.Slug)
	require.Len(t, managers, 2)
	assert.Equal(t, f.owner, managers[0].UserID)
	assert.Equal(t, domain.OrganizationRoleOwner, managers[0].Role)
	assert.Equal(t, "owner@example.com", managers[0].User.Email)
	assert.Equal(t, f.admin, managers[1].UserID)
	assert.Equal(t, domain.OrganizationRoleAdmin, managers[1].Role)
	assert.Equal(t, "admin@example.com", managers[1].User.Email)

	_, _, err = f.repo.findOrganizationManagers(context.Background(), f.organization.ID+100)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
}

func TestRepositoryFindMembershipReturnsNilForOutsiders(t *testing.T) {
	f := newAdministrationFixture(t)

	membership, err := f.repo.findMembership(context.Background(), f.organization.ID, f.member)
	require.NoError(t, err)
	require.NotNil(t, membership)
	assert.Equal(t, domain.OrganizationRoleMember, membership.Role)

	membership, err = f.repo.findMembership(context.Background(), f.organization.ID, f.outsider)
	require.NoError(t, err)
	assert.Nil(t, membership)
}

func TestRepositoryGrantAdminCreatesRaisesOrKeeps(t *testing.T) {
	f := newAdministrationFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	created, err := f.repo.grantAdmin(ctx, f.organization.ID, f.outsider, now)
	require.NoError(t, err)
	assert.True(t, created.Created())
	assert.Equal(t, domain.OrganizationRoleAdmin, created.Membership.Role)
	assert.NotZero(t, created.Membership.ID)
	require.NotNil(t, created.Membership.Organization)
	assert.Equal(t, "Acme", created.Membership.Organization.Name)

	raised, err := f.repo.grantAdmin(ctx, f.organization.ID, f.member, now)
	require.NoError(t, err)
	assert.Equal(t, domain.OrganizationRoleMember, raised.BeforeRole)
	assert.Equal(t, domain.OrganizationRoleAdmin, raised.Membership.Role)
	assert.True(t, raised.Changed())

	for _, userID := range []uint{f.admin, f.owner, f.outsider} {
		kept, keepErr := f.repo.grantAdmin(ctx, f.organization.ID, userID, now)
		require.NoError(t, keepErr)
		assert.False(t, kept.Changed(), "user %d", userID)
	}
	owner, err := f.repo.findMembership(ctx, f.organization.ID, f.owner)
	require.NoError(t, err)
	assert.Equal(t, domain.OrganizationRoleOwner, owner.Role, "an owner is never lowered")

	var memberships int64
	require.NoError(t, f.db.Model(&OrganizationMembershipPO{}).Where("organization_id = ?", f.organization.ID).Count(&memberships).Error)
	assert.Equal(t, int64(4), memberships)

	_, err = f.repo.grantAdmin(ctx, f.organization.ID+100, f.outsider, now)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	_, err = f.repo.grantAdmin(ctx, f.organization.ID, f.outsider+100, now)
	require.ErrorIs(t, err, domain.ErrUserNotFound)
}

// fakeAdministration is the administration store of service tests.
type fakeAdministration struct {
	grant *domain.OrganizationAdminGrant
	err   error
}

func (f *fakeAdministration) findOrganizationManagers(context.Context, uint) (*domain.Organization, []*domain.OrganizationMembership, error) {
	return nil, nil, f.err
}

func (f *fakeAdministration) findMembership(context.Context, uint, uint) (*domain.OrganizationMembership, error) {
	return nil, f.err
}

func (f *fakeAdministration) grantAdmin(context.Context, uint, uint, time.Time) (*domain.OrganizationAdminGrant, error) {
	return f.grant, f.err
}

func TestGrantAdminAnnouncesOnlyACreatedMembership(t *testing.T) {
	now := time.Date(2026, time.November, 20, 10, 0, 0, 0, time.UTC)
	publisher := &recordingPublisher{}
	svc := NewService(&fakeRepository{}, nil, nil, InvitationPolicy{TTL: time.Hour}, publisher)
	svc.now = func() time.Time { return now }
	store := &fakeAdministration{grant: &domain.OrganizationAdminGrant{
		Membership: &domain.OrganizationMembership{ID: 9, OrganizationID: 4, UserID: 12, Role: domain.OrganizationRoleAdmin},
	}}
	svc.administration = store

	grant, err := svc.GrantAdmin(context.Background(), 4, 12)
	require.NoError(t, err)
	assert.True(t, grant.Created())
	require.Len(t, publisher.published, 1)
	joined, ok := publisher.published[0].(domain.OrganizationMemberJoinedEvent)
	require.True(t, ok)
	assert.Equal(t, uint(4), joined.OrganizationID)
	assert.Equal(t, uint(12), joined.UserID)
	assert.Equal(t, uint(9), joined.MembershipID)

	store.grant = &domain.OrganizationAdminGrant{
		Membership: &domain.OrganizationMembership{ID: 9, OrganizationID: 4, UserID: 12, Role: domain.OrganizationRoleAdmin},
		BeforeRole: domain.OrganizationRoleMember,
	}
	_, err = svc.GrantAdmin(context.Background(), 4, 12)
	require.NoError(t, err)
	assert.Len(t, publisher.published, 1, "a raised member already joined")

	_, err = svc.GrantAdmin(context.Background(), 0, 12)
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	svc.administration = nil
	_, err = svc.GrantAdmin(context.Background(), 4, 12)
	require.ErrorIs(t, err, domain.ErrServiceUnavailable)
}
