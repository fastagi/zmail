package mailbox

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

func TestDomainSetupErrorCodeNamesWhatTheOwnerCanDo(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no failure", nil, ""},
		{"forbidden token", &mailtransport.DomainError{Operation: "enable email routing", Status: 403, Code: 10000}, DomainErrorPermissionDenied},
		{"unauthorized token", &mailtransport.DomainError{Operation: "create record", Status: 401}, DomainErrorPermissionDenied},
		{"cloudflare auth code on 400", &mailtransport.DomainError{Operation: "create record", Status: 400, Code: 9109}, DomainErrorPermissionDenied},
		{"cloudflare outage", &mailtransport.DomainError{Operation: "enable email sending", Status: 502}, DomainErrorProviderUnavailable},
		{"rate limited", &mailtransport.DomainError{Operation: "create record", Status: 429}, DomainErrorProviderUnavailable},
		{"timeout", fmt.Errorf("call: %w", context.DeadlineExceeded), DomainErrorProviderUnavailable},
		{"routing refused", &mailtransport.DomainError{Operation: "enable email routing", Status: 400, Code: 2007}, DomainErrorRoutingRefused},
		{"sending refused", &mailtransport.DomainError{Operation: "enable email sending", Status: 400}, DomainErrorSendingRefused},
		{"sending records unreadable", &mailtransport.DomainError{Operation: "sending records", Status: 400}, DomainErrorSendingRefused},
		{"dns record refused", fmt.Errorf("wrapped: %w", &mailtransport.DomainError{Operation: "create record", Status: 400, Code: 81057}), DomainErrorDNSRecordRefused},
		{"routing rule refused", &mailtransport.DomainError{Operation: "create routing rule", Status: 400}, DomainErrorRoutingRuleRefused},
		{"other provider refusal", &mailtransport.DomainError{Operation: "list records", Status: 400}, DomainErrorProviderRefused},
		{"zone gone", mailtransport.ErrZoneNotFound, DomainErrorZoneNotFound},
		{"anything else", errors.New("database unavailable"), DomainErrorSetupFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, domainSetupErrorCode(tc.err))
		})
	}
}
