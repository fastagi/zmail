package domainsetup

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// Stable codes for the last failed setup or check of a mail domain. Clients translate them into
// what went wrong and what the owner can do next; last_error keeps the provider's own words.
const (
	DomainErrorPermissionDenied    = "domain.permission_denied"
	DomainErrorProviderUnavailable = "domain.provider_unavailable"
	DomainErrorRoutingRefused      = "domain.routing_refused"
	DomainErrorSendingRefused      = "domain.sending_refused"
	DomainErrorDNSRecordRefused    = "domain.dns_record_refused"
	DomainErrorRoutingRuleRefused  = "domain.routing_rule_refused"
	DomainErrorProviderRefused     = "domain.provider_refused"
	DomainErrorZoneNotFound        = "domain.zone_not_found"
	DomainErrorSetupFailed         = "domain.setup_failed"
)

// cloudflareAuthCodes are Cloudflare error codes for a token that may not do the call.
var cloudflareAuthCodes = map[int]bool{9109: true, 10000: true, 10001: true}

// ErrorCode classifies a setup or check failure; nil has no code.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, mailtransport.ErrZoneNotFound) {
		return DomainErrorZoneNotFound
	}
	var failure *mailtransport.DomainError
	if errors.As(err, &failure) {
		switch {
		case failure.Status == http.StatusUnauthorized || failure.Status == http.StatusForbidden ||
			cloudflareAuthCodes[failure.Code]:
			return DomainErrorPermissionDenied
		case failure.Status == http.StatusTooManyRequests || failure.Status >= http.StatusInternalServerError:
			return DomainErrorProviderUnavailable
		}
		switch failure.Operation {
		case "enable email routing":
			return DomainErrorRoutingRefused
		case "enable email sending", "sending records", "list sending domains":
			return DomainErrorSendingRefused
		case "create record":
			return DomainErrorDNSRecordRefused
		case "create routing rule":
			return DomainErrorRoutingRuleRefused
		default:
			return DomainErrorProviderRefused
		}
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) {
		return DomainErrorProviderUnavailable
	}
	return DomainErrorSetupFailed
}
