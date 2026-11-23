package domain

import "testing"

func TestDeveloperGrantAllowsDomain(t *testing.T) {
	listed := DeveloperGrant{Domains: []string{"fast-ui.com"}}
	if !listed.AllowsDomain("FAST-UI.com.", false) {
		t.Fatal("a listed domain must match case-insensitively and without a trailing dot")
	}
	if listed.AllowsDomain("shu.ie", true) {
		t.Fatal("an unlisted domain must not match, verified or not")
	}
	if listed.AllowsDomain("", true) {
		t.Fatal("an empty domain never matches")
	}
	all := DeveloperGrant{AllDomains: true}
	if !all.AllowsDomain("shu.ie", true) {
		t.Fatal("all domains must allow a verified organization domain")
	}
	if all.AllowsDomain("zmail.example", false) {
		t.Fatal("all domains must not allow a domain that is not a verified organization domain")
	}
}

func TestAddressDomain(t *testing.T) {
	cases := map[string]string{
		"No-Reply@Fast-UI.com": "fast-ui.com",
		"a@b@shu.ie.":          "shu.ie",
		"no-at-sign":           "",
	}
	for address, want := range cases {
		if got := AddressDomain(address); got != want {
			t.Fatalf("AddressDomain(%q) = %q, want %q", address, got, want)
		}
	}
}

func TestMemberFeaturesDeveloperImpliesAPIKeys(t *testing.T) {
	features := MemberFeatures{Developer: true}
	if !features.Has(MemberFeatureAPIKeys) {
		t.Fatal("the developer grant must include API keys")
	}
	if !features.Has(MemberFeatureDeveloper) {
		t.Fatal("the developer grant must report itself")
	}
	if (MemberFeatures{APIKeys: true}).Has(MemberFeatureDeveloper) {
		t.Fatal("API keys alone must not grant developer")
	}
	if !MemberFeatureDeveloper.IsValid() {
		t.Fatal("developer must be a valid feature")
	}
	if !AllMemberFeatures().Developer {
		t.Fatal("owners and admins have the developer feature")
	}
	catalog := MemberFeatureCatalog()
	if catalog[len(catalog)-1] != MemberFeatureDeveloper {
		t.Fatal("developer is last in the catalog")
	}
}

func TestMemberFeaturePatchAppliesDeveloper(t *testing.T) {
	on := true
	patch := MemberFeaturePatch{Developer: &on}
	if patch.IsEmpty() {
		t.Fatal("a developer-only patch is not empty")
	}
	if !patch.Apply(MemberFeatures{}).Developer {
		t.Fatal("the patch must turn developer on")
	}
}

func TestOrganizationContextDeveloperScope(t *testing.T) {
	owner := OrganizationContext{Role: OrganizationRoleOwner}
	if _, unrestricted, ok := owner.DeveloperScope(); !ok || !unrestricted {
		t.Fatal("an owner is an unrestricted developer")
	}
	plain := OrganizationContext{Role: OrganizationRoleMember, Features: MemberFeatures{APIKeys: true}}
	if _, _, ok := plain.DeveloperScope(); ok {
		t.Fatal("a member without the grant is not a developer")
	}
	grant := DeveloperGrant{Domains: []string{"fast-ui.com"}}
	developer := OrganizationContext{
		Role: OrganizationRoleMember, Features: MemberFeatures{Developer: true}, DeveloperGrant: &grant,
	}
	scope, unrestricted, ok := developer.DeveloperScope()
	if !ok || unrestricted || !scope.AllowsDomain("fast-ui.com", true) {
		t.Fatal("a developer member is limited to the grant's domains")
	}
	missing := OrganizationContext{Role: OrganizationRoleMember, Features: MemberFeatures{Developer: true}}
	if _, _, ok := missing.DeveloperScope(); ok {
		t.Fatal("a developer flag without a stored grant grants nothing")
	}
}
