package organization

import (
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// Resend quotas per invitation. They bound how often one address can be emailed by a manager.
const (
	invitationResendsPerMinute = 1
	invitationResendsPerDay    = 5

	ruleInvitationResendMinute = "organization_invitation_resend_minute"
	ruleInvitationResendDay    = "organization_invitation_resend_day"
)

// InvitationPolicy owns replaceable organization invitation timing policy.
type InvitationPolicy struct {
	TTL time.Duration
	// ResendCooldown and ResendDaily are per-invitation fixed-window quotas for resends.
	ResendCooldown ratelimit.Store
	ResendDaily    ratelimit.Store
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
	}
}
