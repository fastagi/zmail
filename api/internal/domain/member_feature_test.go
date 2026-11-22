package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemberFeaturesGrantOnlyWhatIsStored(t *testing.T) {
	features := MemberFeatures{Contacts: true, Templates: true}

	assert.True(t, features.Has(MemberFeatureContacts))
	assert.True(t, features.Has(MemberFeatureTemplates))
	assert.False(t, features.Has(MemberFeatureBroadcasts))
	assert.False(t, features.Has(MemberFeatureAPIKeys))
	assert.False(t, features.Has(MemberFeature("unknown")))
}

func TestNewMemberDefaultsLeaveBroadcastsAndAPIKeysToAnAdmin(t *testing.T) {
	defaults := DefaultMemberFeatures()

	assert.Equal(t, MemberFeatures{Contacts: true, Templates: true}, defaults)
}

func TestManagersAlwaysHaveEveryFeature(t *testing.T) {
	for _, role := range []OrganizationRole{OrganizationRoleOwner, OrganizationRoleAdmin} {
		assert.Equal(t, AllMemberFeatures(), role.EffectiveFeatures(MemberFeatures{}), role)
	}
	stored := MemberFeatures{Broadcasts: true}
	assert.Equal(t, stored, OrganizationRoleMember.EffectiveFeatures(stored))
}

func TestOrganizationContextCanUseFollowsRoleAndGrants(t *testing.T) {
	member := OrganizationContext{Role: OrganizationRoleMember, Features: MemberFeatures{Contacts: true}}
	assert.True(t, member.CanUse(MemberFeatureContacts))
	assert.False(t, member.CanUse(MemberFeatureBroadcasts))

	admin := OrganizationContext{Role: OrganizationRoleAdmin}
	for _, feature := range MemberFeatureCatalog() {
		assert.True(t, admin.CanUse(feature), feature)
	}
}

func TestMemberFeatureCatalogIsStable(t *testing.T) {
	assert.Equal(t, []MemberFeature{
		MemberFeatureBroadcasts, MemberFeatureContacts, MemberFeatureTemplates, MemberFeatureAPIKeys,
	}, MemberFeatureCatalog())
	for _, feature := range MemberFeatureCatalog() {
		assert.True(t, feature.IsValid())
	}
	assert.False(t, MemberFeature("permissions").IsValid())
}
