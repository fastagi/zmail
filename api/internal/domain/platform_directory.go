package domain

import "context"

// PlatformMemberTotals counts organizations and their members across the whole platform.
type PlatformMemberTotals struct {
	// Organizations counts every existing organization.
	Organizations int64
	// Members counts distinct users with a membership in any organization, excluding deleted
	// accounts and including disabled ones.
	Members int64
	// DisabledMembers counts how many of Members are disabled accounts.
	DisabledMembers int64
}

// PlatformOrganizationRow is one organization with its own membership count (deleted accounts
// excluded).
type PlatformOrganizationRow struct {
	ID      uint
	Name    string
	Members int64
}

// PlatformOrganizationDirectory is the organization starter's read model for platform-wide
// operator views. It aggregates memberships only; callers join other aggregates by organization ID.
type PlatformOrganizationDirectory interface {
	PlatformMemberTotals(ctx context.Context) (PlatformMemberTotals, error)
	// PlatformOrganizations returns every organization whose name contains query (case-insensitive,
	// literal), ordered by ascending ID. An empty query matches all.
	PlatformOrganizations(ctx context.Context, query string) ([]PlatformOrganizationRow, error)
	// PlatformOrganizationNames returns the names of the given organizations; unknown IDs are absent.
	PlatformOrganizationNames(ctx context.Context, ids []uint) (map[uint]string, error)
}
