package organization

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

func TestFeatureRequestCarriesTheDeveloperScope(t *testing.T) {
	var req UpdateOrganizationMemberFeaturesRequest
	require.NoError(t, json.Unmarshal([]byte(`{"developer":true,"developer_domains":["fast-ui.com"]}`), &req))
	patch := req.patch()
	require.NotNil(t, patch.Developer)
	assert.True(t, *patch.Developer)
	require.NotNil(t, patch.DeveloperGrant)
	assert.Equal(t, domain.DeveloperGrant{Domains: []string{"fast-ui.com"}}, *patch.DeveloperGrant)
	assert.Empty(t, req.validationErrors())

	var all UpdateOrganizationMemberFeaturesRequest
	require.NoError(t, json.Unmarshal([]byte(`{"developer_all_domains":true}`), &all))
	assert.Equal(t, domain.DeveloperGrant{AllDomains: true}, *all.patch().DeveloperGrant)

	var off UpdateOrganizationMemberFeaturesRequest
	require.NoError(t, json.Unmarshal([]byte(`{"developer":false}`), &off))
	assert.Nil(t, off.patch().DeveloperGrant)
}

func TestMemberResponseReportsDeveloperAndScope(t *testing.T) {
	member := toMemberResponse(&domain.OrganizationMembership{
		ID: 3, Role: domain.OrganizationRoleMember,
		Features:       domain.MemberFeatures{Developer: true},
		DeveloperGrant: &domain.DeveloperGrant{Domains: []string{"fast-ui.com"}},
	})
	encoded, err := json.Marshal(member)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"developer":true`)
	assert.Contains(t, string(encoded), `"api_keys":true`, "developer includes API keys")
	assert.Contains(t, string(encoded), `"developer_scope":{"all_domains":false,"domains":["fast-ui.com"]}`)

	admin, err := json.Marshal(toMemberResponse(&domain.OrganizationMembership{ID: 4, Role: domain.OrganizationRoleAdmin}))
	require.NoError(t, err)
	assert.Contains(t, string(admin), `"developer":true`)
	assert.Contains(t, string(admin), `"developer_scope":null`, "managers have no scope: they are unrestricted")

	context, err := json.Marshal(toContextResponse(domain.OrganizationContext{
		Role: domain.OrganizationRoleMember, Features: domain.MemberFeatures{Developer: true},
		DeveloperGrant: &domain.DeveloperGrant{AllDomains: true, Domains: []string{}},
	}))
	require.NoError(t, err)
	assert.Contains(t, string(context), `"developer_scope":{"all_domains":true,"domains":[]}`)
}

func TestGrantingDeveloperAuditsOnlyTheDeveloperChangeAndScope(t *testing.T) {
	repo := &fakeRepository{featureFn: func(patch domain.MemberFeaturePatch) (*domain.OrganizationMembershipFeatureChange, error) {
		return &domain.OrganizationMembershipFeatureChange{
			Membership: &domain.OrganizationMembership{
				ID: 9, UserID: 5, Role: domain.OrganizationRoleMember,
				Features:       domain.MemberFeatures{Developer: true},
				DeveloperGrant: patch.DeveloperGrant,
			},
			Before: domain.MemberFeatures{},
		}, nil
	}}
	svc := newOrganizationService(repo)
	var audited []auditstarter.Change
	svc.recordAudit = func(_ context.Context, change auditstarter.Change) { audited = append(audited, change) }

	on := true
	_, err := svc.UpdateMemberFeatures(context.Background(), 1, 2, 9, &UpdateOrganizationMemberFeaturesRequest{
		Developer: &on, DeveloperDomains: []string{"fast-ui.com"},
	})
	require.NoError(t, err)
	require.Len(t, audited, 1)
	assert.Equal(t, domain.AuditValueChange{Before: false, After: true}, audited[0].Changes["developer"])
	_, apiKeys := audited[0].Changes["api_keys"]
	assert.False(t, apiKeys, "the implied API key grant is not a stored change")
	scope := audited[0].Changes["developer_scope"]
	assert.Nil(t, scope.Before)
	assert.Equal(t, map[string]any{"all_domains": false, "domains": []string{"fast-ui.com"}}, scope.After)
}
