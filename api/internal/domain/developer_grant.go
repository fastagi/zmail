package domain

import (
	"slices"
	"strings"
)

// MaxDeveloperGrantDomains bounds the sending domains one developer grant may list.
const MaxDeveloperGrantDomains = 50

// DeveloperGrant is the sending scope of a member's developer grant: every verified domain of the
// organization, followed as domains are verified later, or an explicit list. The platform's shared
// sending domain is never in scope.
type DeveloperGrant struct {
	AllDomains bool
	// Domains are lowercase names; empty when AllDomains is set.
	Domains []string
}

// AllowsDomain reports whether the scope covers a sending domain. verifiedOrganizationDomain says
// whether the domain is currently a verified domain of the organization; an all-domains scope
// needs it, an explicit list matches by name.
func (g DeveloperGrant) AllowsDomain(name string, verifiedOrganizationDomain bool) bool {
	name = NormalizeDomainName(name)
	if name == "" {
		return false
	}
	if g.AllDomains {
		return verifiedOrganizationDomain
	}
	return slices.Contains(g.Domains, name)
}

// NormalizeDomainName lowercases, trims spaces, and drops a trailing dot.
func NormalizeDomainName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// AddressDomain is the normalized domain of an address, or empty when it has no "@".
func AddressDomain(address string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return ""
	}
	return NormalizeDomainName(address[at+1:])
}
