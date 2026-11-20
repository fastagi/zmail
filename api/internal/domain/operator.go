package domain

import (
	"context"
	"time"
)

// OperatorScope is what one operator grant allows. Each grant has exactly one scope.
type OperatorScope string

const (
	// OperatorScopeFull allows every operator route.
	OperatorScopeFull OperatorScope = "full"
	// OperatorScopeMailMetrics allows only the read-only mail metrics and organization reads.
	OperatorScopeMailMetrics OperatorScope = "mail_metrics"
)

// Valid reports whether the scope is a known value.
func (s OperatorScope) Valid() bool {
	return s == OperatorScopeFull || s == OperatorScopeMailMetrics
}

// OperatorCapability names one group of operator routes.
type OperatorCapability string

const (
	// OperatorCapabilityManage covers every operator route, including account administration.
	OperatorCapabilityManage OperatorCapability = "operator:manage"
	// OperatorCapabilityMailMetrics covers the mail metrics, the outbound log, and organization reads.
	OperatorCapabilityMailMetrics OperatorCapability = "mail_metrics:read"
	// OperatorCapabilityMailContent covers reading an organization broadcast's sent body, with a
	// reason and an audit record. No scope includes it; a grant gets it only through MailContent.
	OperatorCapabilityMailContent OperatorCapability = "mail_content:read"
)

// Capabilities returns what the scope allows, in a stable order. An unknown scope allows nothing.
func (s OperatorScope) Capabilities() []OperatorCapability {
	switch s {
	case OperatorScopeFull:
		return []OperatorCapability{OperatorCapabilityManage, OperatorCapabilityMailMetrics}
	case OperatorScopeMailMetrics:
		return []OperatorCapability{OperatorCapabilityMailMetrics}
	default:
		return nil
	}
}

// Allows reports whether the scope includes the capability.
func (s OperatorScope) Allows(capability OperatorCapability) bool {
	for _, granted := range s.Capabilities() {
		if granted == capability {
			return true
		}
	}
	return false
}

// OperatorAccess is everything one grant allows: its scope plus the explicitly granted content
// add-on. Content is never implied by a scope, full included.
type OperatorAccess struct {
	Scope       OperatorScope
	MailContent bool
}

// Valid reports whether the scope is a known value.
func (a OperatorAccess) Valid() bool {
	return a.Scope.Valid()
}

// Capabilities returns what the grant allows, in a stable order. An unknown scope allows nothing,
// content included.
func (a OperatorAccess) Capabilities() []OperatorCapability {
	capabilities := a.Scope.Capabilities()
	if a.MailContent && len(capabilities) > 0 {
		capabilities = append(capabilities, OperatorCapabilityMailContent)
	}
	return capabilities
}

// Allows reports whether the grant includes the capability.
func (a OperatorAccess) Allows(capability OperatorCapability) bool {
	for _, granted := range a.Capabilities() {
		if granted == capability {
			return true
		}
	}
	return false
}

// OperatorGrant makes a user a platform operator. Operators run the deployment through the Admin
// Console; the grant is not an organization role or a permission key.
type OperatorGrant struct {
	UserID   uint
	Username string
	Email    string
	Scope    OperatorScope
	// MailContent adds OperatorCapabilityMailContent to the scope's capabilities.
	MailContent bool
	GrantedAt   time.Time
}

// Access returns what the grant allows.
func (g OperatorGrant) Access() OperatorAccess {
	return OperatorAccess{Scope: g.Scope, MailContent: g.MailContent}
}

// OperatorGrantChange says what a grant command did.
type OperatorGrantChange string

const (
	// OperatorGrantCreated: the account had no grant and now has one.
	OperatorGrantCreated OperatorGrantChange = "created"
	// OperatorGrantScopeChanged: the account's existing grant now has another scope or content flag.
	OperatorGrantScopeChanged OperatorGrantChange = "scope_changed"
	// OperatorGrantUnchanged: the account already had a grant with this scope and content flag.
	OperatorGrantUnchanged OperatorGrantChange = "unchanged"
)

// OperatorGrantStore is the CLI-facing seam for creating, removing, and listing operator grants.
type OperatorGrantStore interface {
	GrantOperator(ctx context.Context, email string, access OperatorAccess) (*OperatorGrant, OperatorGrantChange, error)
	RevokeOperator(ctx context.Context, email string) (userID uint, removed bool, err error)
	ListOperators(ctx context.Context) ([]OperatorGrant, error)
}
