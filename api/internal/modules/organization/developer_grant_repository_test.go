package organization

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// createMailDomainsForGrantTest stands in for the mailbox starter's mail_domains table, which a
// developer grant's domains are checked against.
func createMailDomainsForGrantTest(t *testing.T, db *gorm.DB, organizationID uint, verified map[string]bool) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS mail_domains (
		id bigserial PRIMARY KEY, organization_id bigint NOT NULL, name varchar(253) NOT NULL,
		status varchar(16) NOT NULL)`).Error)
	for name, ok := range verified {
		status := "pending"
		if ok {
			status = "verified"
		}
		require.NoError(t, db.Exec(`INSERT INTO mail_domains (organization_id, name, status) VALUES (?, ?, ?)`,
			organizationID, name, status).Error)
	}
}

func developerPatch(grant domain.DeveloperGrant) domain.MemberFeaturePatch {
	return domain.MemberFeaturePatch{Developer: boolPointer(true), DeveloperGrant: &grant}
}

func TestManagerGrantsDeveloperWithinVerifiedDomains(t *testing.T) {
	db, repo, organizationID, memberships := createOrganizationMembershipFixture(t)
	createMailDomainsForGrantTest(t, db, organizationID, map[string]bool{"fast-ui.com": true, "shu.ie": true})
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

	change, err := repo.UpdateMemberFeatures(context.Background(), organizationID, memberships["admin"].UserID,
		memberships["member"].ID, developerPatch(domain.DeveloperGrant{Domains: []string{"FAST-UI.com"}}), now)
	require.NoError(t, err)
	assert.Nil(t, change.BeforeGrant)
	assert.True(t, change.Membership.Features.Developer)
	require.NotNil(t, change.Membership.DeveloperGrant)
	assert.Equal(t, []string{"fast-ui.com"}, change.Membership.DeveloperGrant.Domains)

	resolved, err := repo.ResolveContext(context.Background(), organizationID, memberships["member"].UserID)
	require.NoError(t, err)
	assert.True(t, resolved.Features.Developer)
	assert.True(t, resolved.CanUse(domain.MemberFeatureAPIKeys), "developer includes API keys")
	require.NotNil(t, resolved.DeveloperGrant)
	assert.Equal(t, domain.DeveloperGrant{Domains: []string{"fast-ui.com"}}, *resolved.DeveloperGrant)

	members, _, err := repo.ListMembers(context.Background(), organizationID, memberships["owner"].UserID, 1, 20)
	require.NoError(t, err)
	var listed *domain.OrganizationMembership
	for _, member := range members {
		if member.ID == memberships["member"].ID {
			listed = member
		}
	}
	require.NotNil(t, listed)
	require.NotNil(t, listed.DeveloperGrant, "the member list reports the scope")

	// Narrowing to all domains replaces the scope; turning it off removes the row.
	change, err = repo.UpdateMemberFeatures(context.Background(), organizationID, memberships["owner"].UserID,
		memberships["member"].ID, domain.MemberFeaturePatch{DeveloperGrant: &domain.DeveloperGrant{AllDomains: true}}, now)
	require.NoError(t, err)
	require.NotNil(t, change.BeforeGrant)
	assert.Equal(t, domain.DeveloperGrant{AllDomains: true, Domains: []string{}}, *change.Membership.DeveloperGrant)

	_, err = repo.UpdateMemberFeatures(context.Background(), organizationID, memberships["owner"].UserID,
		memberships["member"].ID, domain.MemberFeaturePatch{Developer: boolPointer(false)}, now)
	require.NoError(t, err)
	resolved, err = repo.ResolveContext(context.Background(), organizationID, memberships["member"].UserID)
	require.NoError(t, err)
	assert.False(t, resolved.Features.Developer)
	assert.Nil(t, resolved.DeveloperGrant)
	assert.False(t, resolved.CanUse(domain.MemberFeatureAPIKeys), "API keys were never granted separately")
}

func TestDeveloperGrantRefusesDomainsOutsideTheOrganization(t *testing.T) {
	db, repo, organizationID, memberships := createOrganizationMembershipFixture(t)
	createMailDomainsForGrantTest(t, db, organizationID, map[string]bool{"fast-ui.com": true, "pending.example": false})
	createMailDomainsForGrantTest(t, db, organizationID+1000, map[string]bool{"other.example": true})
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

	for _, name := range []string{"pending.example", "other.example", "zmail.example"} {
		_, err := repo.UpdateMemberFeatures(context.Background(), organizationID, memberships["owner"].UserID,
			memberships["member"].ID, developerPatch(domain.DeveloperGrant{Domains: []string{"fast-ui.com", name}}), now)
		require.ErrorIs(t, err, domain.ErrDeveloperDomainNotAllowed, name)
	}
	for _, patch := range []domain.MemberFeaturePatch{
		{Developer: boolPointer(true)}, // a new grant needs a scope
		developerPatch(domain.DeveloperGrant{}),
		{DeveloperGrant: &domain.DeveloperGrant{AllDomains: true}}, // no grant to rescope
	} {
		_, err := repo.UpdateMemberFeatures(context.Background(), organizationID, memberships["owner"].UserID,
			memberships["member"].ID, patch, now)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	}
	_, err := repo.UpdateMemberFeatures(context.Background(), organizationID, memberships["owner"].UserID,
		memberships["admin"].ID, developerPatch(domain.DeveloperGrant{AllDomains: true}), now)
	require.ErrorIs(t, err, domain.ErrOrganizationMemberFeaturesFixed)

	var count int64
	require.NoError(t, db.Model(&MemberDeveloperGrantPO{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRemovingAMemberDropsTheDeveloperGrant(t *testing.T) {
	db, repo, organizationID, memberships := createOrganizationMembershipFixture(t)
	createMailDomainsForGrantTest(t, db, organizationID, map[string]bool{"fast-ui.com": true})
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	_, err := repo.UpdateMemberFeatures(context.Background(), organizationID, memberships["owner"].UserID,
		memberships["member"].ID, developerPatch(domain.DeveloperGrant{AllDomains: true}), now)
	require.NoError(t, err)

	_, err = repo.RemoveMember(context.Background(), organizationID, memberships["owner"].UserID, memberships["member"].ID)
	require.NoError(t, err)
	var count int64
	require.NoError(t, db.Model(&MemberDeveloperGrantPO{}).Count(&count).Error)
	assert.Zero(t, count)
}

// TestRoleChangeWithdrawsTheDeveloperGrant: a promoted member needs no grant, and demoting them
// later must not quietly bring back the old one.
func TestRoleChangeWithdrawsTheDeveloperGrant(t *testing.T) {
	db, repo, organizationID, memberships := createOrganizationMembershipFixture(t)
	createMailDomainsForGrantTest(t, db, organizationID, map[string]bool{"fast-ui.com": true})
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	owner, member := memberships["owner"].UserID, memberships["member"]

	_, err := repo.UpdateMemberFeatures(ctx, organizationID, owner, member.ID,
		developerPatch(domain.DeveloperGrant{Domains: []string{"fast-ui.com"}}), now)
	require.NoError(t, err)

	_, err = repo.ChangeMemberRole(ctx, organizationID, owner, member.ID, domain.OrganizationRoleAdmin, now)
	require.NoError(t, err)
	change, err := repo.ChangeMemberRole(ctx, organizationID, owner, member.ID, domain.OrganizationRoleMember, now)
	require.NoError(t, err)
	assert.Nil(t, change.Membership.DeveloperGrant)
	assert.False(t, change.Membership.Features.Developer)

	resolved, err := repo.ResolveContext(ctx, organizationID, member.UserID)
	require.NoError(t, err)
	assert.Nil(t, resolved.DeveloperGrant)
	_, _, developer := resolved.DeveloperScope()
	assert.False(t, developer, "the old grant must not come back")
}
