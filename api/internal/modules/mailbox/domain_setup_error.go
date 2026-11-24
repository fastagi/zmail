package mailbox

import "github.com/fastagi/zmail/api/internal/modules/mailbox/domainsetup"

// Stable codes for the last failed setup or check of a mail domain. Clients translate them into
// what went wrong and what the owner can do next; last_error keeps the provider's own words.
const (
	DomainErrorPermissionDenied    = domainsetup.DomainErrorPermissionDenied
	DomainErrorProviderUnavailable = domainsetup.DomainErrorProviderUnavailable
	DomainErrorRoutingRefused      = domainsetup.DomainErrorRoutingRefused
	DomainErrorSendingRefused      = domainsetup.DomainErrorSendingRefused
	DomainErrorDNSRecordRefused    = domainsetup.DomainErrorDNSRecordRefused
	DomainErrorRoutingRuleRefused  = domainsetup.DomainErrorRoutingRuleRefused
	DomainErrorProviderRefused     = domainsetup.DomainErrorProviderRefused
	DomainErrorZoneNotFound        = domainsetup.DomainErrorZoneNotFound
	DomainErrorSetupFailed         = domainsetup.DomainErrorSetupFailed
)

func domainSetupErrorCode(err error) string {
	return domainsetup.ErrorCode(err)
}
