package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

const (
	// ruleExternalMailCredentials limits password bindings per member: each attempt signs in to an
	// outside server, so the endpoint must not become a password-guessing relay.
	ruleExternalMailCredentials = "mailbox_external_mail_credentials"
	// externalMailCredentialAttempts is how many bindings a member may try per window.
	externalMailCredentialAttempts = 10
	externalMailCredentialWindow   = 15 * time.Minute
)

// ErrExternalMailCredentialsRateLimited means a member tried too many password bindings.
var ErrExternalMailCredentialsRateLimited = errors.New("too many external mail credential attempts")

// ExternalMailCredentialsInput binds an account of a password connector. The password is used to
// sign in once, then sealed; it is never logged or returned.
type ExternalMailCredentialsInput struct {
	Provider       domain.ExternalMailProvider
	Host           string
	Port           int
	Username       string
	Password       string
	AccountAddress string
	// BackfillDays is how far back a new binding first reads; zero takes the default.
	BackfillDays int
}

func newExternalMailCredentialLimit(stores *ratelimit.Stores) ratelimit.Store {
	if stores == nil {
		stores = ratelimit.NewMemoryStores()
	}
	return stores.New(ratelimit.Rule{
		Name: ruleExternalMailCredentials, Max: externalMailCredentialAttempts, Window: externalMailCredentialWindow,
	})
}

// BindExternalMailCredentials verifies the credentials by signing in once, then binds the account
// with the same rules as a completed sign-in: a new account gets a private read-only mailbox, the
// viewer's own connection to it is restored, and another member's account is refused.
func (s *service) BindExternalMailCredentials(ctx context.Context, viewer domain.OrganizationContext, input ExternalMailCredentialsInput) (*domain.ExternalMailConnection, bool, error) {
	if !s.externalMailAvailable() || viewer.UserID == 0 {
		return nil, false, ErrExternalMailProviderUnavailable
	}
	connector, registered := s.externalConnectors.lookup(input.Provider)
	verifier, verifies := connector.Source.(ExternalMailPasswordVerifier)
	if !registered || !verifies || connector.AuthKind != ExternalMailAuthPassword || !connector.Capabilities.Read {
		return nil, false, ErrExternalMailProviderUnavailable
	}
	backfill := input.BackfillDays
	if backfill == 0 {
		backfill = domain.DefaultExternalMailBackfillDays
	}
	if backfill < 1 || backfill > domain.MaxExternalMailBackfillDays {
		return nil, false, ErrExternalMailAuthorizationInvalid
	}
	if s.externalCredentialLimit != nil {
		allowed, _, resetAt := s.externalCredentialLimit.Take(ctx, strconv.FormatUint(uint64(viewer.UserID), 10))
		if !allowed {
			return nil, false, &RetryAfterError{Err: ErrExternalMailCredentialsRateLimited, RetryAfter: time.Until(resetAt)}
		}
	}

	token, account, err := verifier.VerifyPassword(ctx, ExternalMailPasswordCredentials{
		Host: input.Host, Port: input.Port, Username: input.Username, Password: input.Password,
		AccountAddress: input.AccountAddress,
	})
	if err != nil {
		outcome := "unreachable"
		switch {
		case errors.Is(err, ErrExternalMailAuth):
			outcome, err = "refused", ErrExternalMailAuthorizationFailed
		case errors.Is(err, ErrExternalMailAuthorizationInvalid):
			outcome = "invalid"
		case ctx.Err() != nil:
			return nil, false, ctx.Err()
		default:
			// A server that cannot be reached, or does not speak the protocol, is one answer.
			err = ErrExternalMailServerUnreachable
		}
		slog.InfoContext(ctx, "external_mail.credentials_refused",
			slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("user_id", uint64(viewer.UserID)),
			slog.String("provider", string(input.Provider)), slog.String("outcome", outcome))
		return nil, false, err
	}
	return s.saveExternalBinding(ctx, viewer, connector, token, externalBinding{
		provider: input.Provider, address: account.Address, backfillDays: backfill,
	})
}
