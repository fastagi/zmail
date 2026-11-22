package organization

import (
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// Resend quotas. They bound how often one address, and one organization's invitations in total,
// can be emailed by managers.
const (
	invitationResendsPerMinute = 1
	invitationResendsPerDay    = 5
	organizationResendsPerHour = 30

	ruleInvitationResendMinute   = "organization_invitation_resend_minute"
	ruleInvitationResendDay      = "organization_invitation_resend_day"
	ruleOrganizationResendHourly = "organization_invitation_resend_organization_hour"
)

// InvitationPolicy owns replaceable organization invitation timing policy.
type InvitationPolicy struct {
	TTL time.Duration
	// ResendCooldown and ResendDaily are per-invitation quotas; ResendOrganization is shared by
	// every invitation of one organization.
	ResendCooldown     ratelimit.Store
	ResendDaily        ratelimit.Store
	ResendOrganization ratelimit.Store
}

// NewInvitationPolicy builds policy from the validated startup snapshot. The resend quotas use the
// shared rate-limit backend, so every replica enforces one budget when it is configured.
func NewInvitationPolicy(cfg *config.Config, stores *ratelimit.Stores) InvitationPolicy {
	ttl := config.DefaultOrganizationInvitationTTL
	if cfg != nil && cfg.Organization.InvitationTTL > 0 {
		ttl = cfg.Organization.InvitationTTL
	}
	if stores == nil {
		stores = ratelimit.NewMemoryStores()
	}
	return InvitationPolicy{
		TTL: ttl,
		ResendCooldown: stores.New(ratelimit.Rule{
			Name: ruleInvitationResendMinute, Max: invitationResendsPerMinute, Window: time.Minute,
		}),
		ResendDaily: stores.New(ratelimit.Rule{
			Name: ruleInvitationResendDay, Max: invitationResendsPerDay, Window: 24 * time.Hour,
		}),
		ResendOrganization: stores.New(ratelimit.Rule{
			Name: ruleOrganizationResendHourly, Max: organizationResendsPerHour, Window: time.Hour,
		}),
	}
}
