package organization

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

func boolPointer(value bool) *bool { return &value }

func TestNewMembershipsStartWithContactsAndTemplatesOnly(t *testing.T) {
	db, _, _, memberships := createOrganizationMembershipFixture(t)

	var stored OrganizationMembershipPO
	require.NoError(t, db.First(&stored, memberships["member"].ID).Error)
	assert.Equal(t, domain.DefaultMemberFeatures(), stored.features())
}

func TestRepositoryResolvesStoredFeaturesWithTheContext(t *testing.T) {
	db, repo, organizationID, memberships := createOrganizationMembershipFixture(t)
	require.NoError(t, db.Model(&OrganizationMembershipPO{}).
		Where("id = ?", memberships["member"].ID).
		Updates(map[string]any{"can_use_broadcasts": true, "can_use_contacts": false}).Error)

	resolved, err := repo.ResolveContext(context.Background(), organizationID, memberships["member"].UserID)
	require.NoError(t, err)
	assert.Equal(t, domain.MemberFeatures{Broadcasts: true, Templates: true}, resolved.Features)
	assert.True(t, resolved.CanUse(domain.MemberFeatureBroadcasts))
	assert.False(t, resolved.CanUse(domain.MemberFeatureContacts))
	assert.False(t, resolved.CanUse(domain.MemberFeatureAPIKeys))
}

func TestManagersChangeOnlyTheFeaturesTheyName(t *testing.T) {
	for _, actor := range []string{"owner", "admin"} {
		t.Run(actor, func(t *testing.T) {
			_, repo, organizationID, memberships := createOrganizationMembershipFixture(t)
			now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

			change, err := repo.UpdateMemberFeatures(context.Background(), organizationID,
				memberships[actor].UserID, memberships["member"].ID,
				domain.MemberFeaturePatch{Broadcasts: boolPointer(true), Templates: boolPointer(false)}, now)
			require.NoError(t, err)
			assert.Equal(t, domain.DefaultMemberFeatures(), change.Before)
			assert.Equal(t, domain.MemberFeatures{Broadcasts: true, Contacts: true}, change.Membership.Features)
			assert.Equal(t, now, change.Membership.UpdatedAt)
			require.NotNil(t, change.Membership.User)

			resolved, err := repo.ResolveContext(context.Background(), organizationID, memberships["member"].UserID)
			require.NoError(t, err)
			assert.Equal(t, domain.MemberFeatures{Broadcasts: true, Contacts: true}, resolved.Features)
		})
	}
}

func TestOnlyManagersChangeFeaturesAndOnlyForMembers(t *testing.T) {
	db, repo, organizationID, memberships := createOrganizationMembershipFixture(t)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	grant := domain.MemberFeaturePatch{APIKeys: boolPointer(true)}

	peerID := createOrganizationTestUser(t, db, "member-peer")
	peer := &OrganizationMembershipPO{OrganizationID: organizationID, UserID: peerID, Role: string(domain.OrganizationRoleMember)}
	require.NoError(t, db.Omit(clause.Associations).Create(peer).Error)

	_, err := repo.UpdateMemberFeatures(context.Background(), organizationID,
		memberships["member"].UserID, peer.ID, grant, now)
	require.ErrorIs(t, err, domain.ErrPermissionDenied)
	_, err = repo.UpdateMemberFeatures(context.Background(), organizationID,
		memberships["member"].UserID, memberships["member"].ID, grant, now)
	require.ErrorIs(t, err, domain.ErrPermissionDenied)

	for _, target := range []string{"owner", "admin"} {
		_, err = repo.UpdateMemberFeatures(context.Background(), organizationID,
			memberships["owner"].UserID, memberships[target].ID, grant, now)
		require.ErrorIs(t, err, domain.ErrOrganizationMemberFeaturesFixed, target)
	}

	_, err = repo.UpdateMemberFeatures(context.Background(), organizationID,
		memberships["owner"].UserID, 999999, grant, now)
	require.ErrorIs(t, err, domain.ErrOrganizationMemberNotFound)

	_, err = repo.UpdateMemberFeatures(context.Background(), organizationID,
		999999, memberships["member"].ID, grant, now)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)

	_, err = repo.UpdateMemberFeatures(context.Background(), organizationID,
		memberships["owner"].UserID, memberships["member"].ID, domain.MemberFeaturePatch{}, now)
	require.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestListedMembersCarryTheirFeatures(t *testing.T) {
	db, repo, organizationID, memberships := createOrganizationMembershipFixture(t)
	require.NoError(t, db.Model(&OrganizationMembershipPO{}).
		Where("id = ?", memberships["member"].ID).
		Update("can_use_api_keys", true).Error)

	items, _, err := repo.ListMembers(context.Background(), organizationID, memberships["member"].UserID, 1, 10)
	require.NoError(t, err)
	byID := map[uint]*domain.OrganizationMembership{}
	for _, item := range items {
		byID[item.ID] = item
	}
	assert.Equal(t, domain.MemberFeatures{Contacts: true, Templates: true, APIKeys: true},
		byID[memberships["member"].ID].Features)
}

func TestOrganizationViewsReportEffectiveFeatures(t *testing.T) {
	member := &domain.OrganizationMembership{
		ID: 3, UserID: 4, Role: domain.OrganizationRoleMember,
		Features:     domain.MemberFeatures{Contacts: true},
		Organization: &domain.Organization{ID: 1, Name: "Acme", Slug: "acme"},
	}
	assert.Equal(t, MemberFeaturesResponse{Contacts: true}, toResponse(member).Features)
	assert.Equal(t, MemberFeaturesResponse{Contacts: true}, toMemberResponse(member).Features)

	member.Role = domain.OrganizationRoleAdmin
	all := MemberFeaturesResponse{Broadcasts: true, Contacts: true, Templates: true, APIKeys: true}
	assert.Equal(t, all, toResponse(member).Features)
	assert.Equal(t, all, toContextResponse(domain.OrganizationContext{Role: domain.OrganizationRoleOwner}).Features)
}
