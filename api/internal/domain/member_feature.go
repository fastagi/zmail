package domain

// MemberFeature names one console capability an organization grants to each member separately.
// Owners and admins always have every feature; a grant only matters for the member role.
type MemberFeature string

const (
	// MemberFeatureBroadcasts covers drafting, sending, and stopping marketing broadcasts.
	MemberFeatureBroadcasts MemberFeature = "broadcasts"
	// MemberFeatureContacts covers the organization's contacts and contact lists.
	MemberFeatureContacts MemberFeature = "contacts"
	// MemberFeatureTemplates covers creating and editing mail templates.
	MemberFeatureTemplates MemberFeature = "templates"
	// MemberFeatureAPIKeys covers using the member's API keys in the organization.
	MemberFeatureAPIKeys MemberFeature = "api_keys"
	// MemberFeatureMailClients covers creating mail client passwords and reading mail in mail apps.
	MemberFeatureMailClients MemberFeature = "mail_clients"
)

// MemberFeatureCatalog returns every feature in its stable display order.
func MemberFeatureCatalog() []MemberFeature {
	return []MemberFeature{
		MemberFeatureBroadcasts, MemberFeatureContacts, MemberFeatureTemplates, MemberFeatureAPIKeys,
		MemberFeatureMailClients,
	}
}

// IsValid reports whether the feature belongs to the catalog.
func (f MemberFeature) IsValid() bool {
	switch f {
	case MemberFeatureBroadcasts, MemberFeatureContacts, MemberFeatureTemplates, MemberFeatureAPIKeys,
		MemberFeatureMailClients:
		return true
	default:
		return false
	}
}

// MemberFeatures is the set of features stored on one membership.
type MemberFeatures struct {
	Broadcasts  bool
	Contacts    bool
	Templates   bool
	APIKeys     bool
	MailClients bool
}

// DefaultMemberFeatures is what a new member gets: everyday contacts, templates, and mail apps,
// while broadcasts and API keys, which send mail at scale, wait for an owner or admin to grant
// them.
func DefaultMemberFeatures() MemberFeatures {
	return MemberFeatures{Contacts: true, Templates: true, MailClients: true}
}

// AllMemberFeatures grants everything; it is what owners and admins effectively have.
func AllMemberFeatures() MemberFeatures {
	return MemberFeatures{Broadcasts: true, Contacts: true, Templates: true, APIKeys: true, MailClients: true}
}

// Has reports whether the set grants the feature. An unknown feature is never granted.
func (f MemberFeatures) Has(feature MemberFeature) bool {
	switch feature {
	case MemberFeatureBroadcasts:
		return f.Broadcasts
	case MemberFeatureContacts:
		return f.Contacts
	case MemberFeatureTemplates:
		return f.Templates
	case MemberFeatureAPIKeys:
		return f.APIKeys
	case MemberFeatureMailClients:
		return f.MailClients
	default:
		return false
	}
}

// EffectiveFeatures returns what a membership with this role and stored grants may use.
func (r OrganizationRole) EffectiveFeatures(stored MemberFeatures) MemberFeatures {
	if r.CanManageOrganization() {
		return AllMemberFeatures()
	}
	return stored
}

// MemberFeaturePatch changes the named features and leaves the others as they are.
type MemberFeaturePatch struct {
	Broadcasts  *bool
	Contacts    *bool
	Templates   *bool
	APIKeys     *bool
	MailClients *bool
}

// IsEmpty reports whether the patch names no feature.
func (p MemberFeaturePatch) IsEmpty() bool {
	return p.Broadcasts == nil && p.Contacts == nil && p.Templates == nil && p.APIKeys == nil && p.MailClients == nil
}

// Apply returns the features with the patch applied.
func (p MemberFeaturePatch) Apply(features MemberFeatures) MemberFeatures {
	if p.Broadcasts != nil {
		features.Broadcasts = *p.Broadcasts
	}
	if p.Contacts != nil {
		features.Contacts = *p.Contacts
	}
	if p.Templates != nil {
		features.Templates = *p.Templates
	}
	if p.APIKeys != nil {
		features.APIKeys = *p.APIKeys
	}
	if p.MailClients != nil {
		features.MailClients = *p.MailClients
	}
	return features
}

// OrganizationMembershipFeatureChange captures a persisted feature change for auditing.
type OrganizationMembershipFeatureChange struct {
	Membership *OrganizationMembership
	Before     MemberFeatures
}
