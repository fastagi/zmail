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

	assert.Equal(t, MemberFeatures{Contacts: true, Templates: true, MailClients: true}, defaults)
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
		MemberFeatureMailClients, MemberFeatureDeveloper,
	}, MemberFeatureCatalog())
	for _, feature := range MemberFeatureCatalog() {
		assert.True(t, feature.IsValid())
	}
	assert.False(t, MemberFeature("permissions").IsValid())
}

func TestMemberFeatureCatalogIncludesMailClients(t *testing.T) {
	assert.True(t, MemberFeatureMailClients.IsValid())
	assert.Equal(t, MemberFeature("mail_clients"), MemberFeatureMailClients)
	assert.True(t, AllMemberFeatures().Has(MemberFeatureMailClients))
	assert.True(t, DefaultMemberFeatures().Has(MemberFeatureMailClients), "reading one's own mail sends nothing at scale")
	assert.False(t, MemberFeatures{Contacts: true}.Has(MemberFeatureMailClients))

	withdrawn := MemberFeaturePatch{MailClients: new(false)}
	assert.False(t, withdrawn.IsEmpty())
	assert.False(t, withdrawn.Apply(AllMemberFeatures()).MailClients)
	assert.True(t, withdrawn.Apply(AllMemberFeatures()).Contacts)
}
