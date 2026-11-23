package domain

import "context"

// OrganizationContext is a membership verified for one request.
type OrganizationContext struct {
	OrganizationID   uint
	OrganizationName string
	OrganizationSlug string
	MembershipID     uint
	UserID           uint
	Role             OrganizationRole
	// Features are the grants stored on the membership; owners and admins have every feature
	// regardless, so read them through CanUse.
	Features MemberFeatures
	// DeveloperGrant is the member's developer grant and its sending scope; nil without one.
	DeveloperGrant *DeveloperGrant
}

// CanUse reports whether this membership may use the feature.
func (c OrganizationContext) CanUse(feature MemberFeature) bool {
	return c.Role.EffectiveFeatures(c.Features).Has(feature)
}

// DeveloperScope reports whether this membership may use developer capabilities and with what
// sending scope. Owners and admins are unrestricted; a member needs the grant and its stored scope.
func (c OrganizationContext) DeveloperScope() (grant DeveloperGrant, unrestricted bool, ok bool) {
	if c.Role.CanManageOrganization() {
		return DeveloperGrant{}, true, true
	}
	if !c.Features.Developer || c.DeveloperGrant == nil {
		return DeveloperGrant{}, false, false
	}
	return *c.DeveloperGrant, false, true
}

// IsValid reports whether the resolved context contains a complete membership identity.
func (c OrganizationContext) IsValid() bool {
	return c.OrganizationID > 0 &&
		c.OrganizationName != "" &&
		c.OrganizationSlug != "" &&
		c.MembershipID > 0 &&
		c.UserID > 0 &&
		c.Role.IsValid()
}

type organizationContextKey struct{}

// WithOrganizationContext binds one verified organization membership to a request context.
func WithOrganizationContext(ctx context.Context, value OrganizationContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, organizationContextKey{}, value)
}

// OrganizationContextFromContext returns the verified organization membership for this request.
func OrganizationContextFromContext(ctx context.Context) (OrganizationContext, bool) {
	if ctx == nil {
		return OrganizationContext{}, false
	}
	value, ok := ctx.Value(organizationContextKey{}).(OrganizationContext)
	return value, ok && value.IsValid()
}
