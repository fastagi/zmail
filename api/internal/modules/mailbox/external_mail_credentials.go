package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
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
	// ruleExternalMailCredentialsOrganization bounds the attempts of all members of one
	// organization, so the distinct refusals cannot drive a scan of outside hosts.
	ruleExternalMailCredentialsOrganization = "mailbox_external_mail_credentials_org"
	externalMailOrganizationAttempts        = 30
	externalMailOrganizationWindow          = time.Hour
)

// ErrExternalMailCredentialsRateLimited means a member tried too many password bindings.
var ErrExternalMailCredentialsRateLimited = errors.New("too many external mail credential attempts")

// ExternalMailCredentialsInput binds an account of a password connector. The password is used to
// sign in once, then sealed; it is never logged or returned. The account address is derived by
// the connector from what the server accepted, never taken from the member.
type ExternalMailCredentialsInput struct {
	Provider domain.ExternalMailProvider
	Host     string
	Port     int
	Username string
	Password string
	// SMTPHost and SMTPPort optionally name the outgoing server; see ExternalMailPasswordCredentials.
	SMTPHost string
	SMTPPort int
	// BackfillDays is how far back a new binding first reads; zero takes the default.
	BackfillDays int
	// ConnectionID restricts the binding to restoring that connection: the viewer's own, or a
	// shared one they manage. Nil binds a new account or restores the viewer's own by address.
	ConnectionID *uint
	// Sharing binds a team account into a new shared connected mailbox, as for a sign-in.
	Sharing *domain.ExternalMailSharing
}

// externalCredentialLimits bound password bindings per member and per organization.
type externalCredentialLimits struct {
	member       ratelimit.Store
	organization ratelimit.Store
}

func newExternalMailCredentialLimits(stores *ratelimit.Stores) externalCredentialLimits {
	if stores == nil {
		stores = ratelimit.NewMemoryStores()
	}
	return externalCredentialLimits{
		member: stores.New(ratelimit.Rule{
			Name: ruleExternalMailCredentials, Max: externalMailCredentialAttempts, Window: externalMailCredentialWindow,
		}),
		organization: stores.New(ratelimit.Rule{
			Name: ruleExternalMailCredentialsOrganization, Max: externalMailOrganizationAttempts,
			Window: externalMailOrganizationWindow,
		}),
	}
}

// take consumes one attempt of the member and then of the organization; a member already over
// their own limit does not use up the organization's.
func (l externalCredentialLimits) take(ctx context.Context, viewer domain.OrganizationContext) error {
	for _, limit := range []struct {
		store  ratelimit.Store
		bucket string
	}{
		{l.member, "user:" + strconv.FormatUint(uint64(viewer.UserID), 10)},
		{l.organization, "organization:" + strconv.FormatUint(uint64(viewer.OrganizationID), 10)},
	} {
		if limit.store == nil {
			continue
		}
		if allowed, _, resetAt := limit.store.Take(ctx, limit.bucket); !allowed {
			return &RetryAfterError{Err: ErrExternalMailCredentialsRateLimited, RetryAfter: time.Until(resetAt)}
		}
	}
	return nil
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
	if input.Sharing != nil && input.ConnectionID != nil {
		return nil, false, ErrExternalMailAuthorizationInvalid
	}
	// Who may bind what is settled before an attempt is counted or the server is contacted.
	binding := externalBinding{provider: input.Provider, backfillDays: backfill, connectionID: input.ConnectionID}
	if input.Sharing != nil {
		var err error
		if binding.sharing, err = s.checkSharing(ctx, viewer, input.Sharing); err != nil {
			return nil, false, err
		}
	}
	if input.ConnectionID != nil {
		target, err := s.managedConnection(ctx, viewer, *input.ConnectionID)
		if err != nil {
			return nil, false, err
		}
		if target.Provider != input.Provider {
			return nil, false, ErrExternalMailAuthorizationInvalid
		}
		binding.reconnectShared = target.Shared
	}
	if err := s.externalCredentialLimits.take(ctx, viewer); err != nil {
		return nil, false, err
	}

	token, account, err := verifier.VerifyPassword(ctx, ExternalMailPasswordCredentials{
		Host: input.Host, Port: input.Port, Username: input.Username, Password: input.Password,
		SMTPHost: input.SMTPHost, SMTPPort: input.SMTPPort,
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
	binding.address = account.Address
	connection, created, err := s.saveExternalBinding(ctx, viewer, connector, token, binding)
	if err != nil {
		return nil, false, err
	}
	sends := connector.Capabilities.Send && strings.TrimSpace(input.SMTPHost) != ""
	if err := s.externalMail.SetConnectedMailboxSend(ctx, connection.OrganizationID, connection.MailboxID, sends); err != nil {
		return nil, false, err
	}
	return connection, created, nil
}
