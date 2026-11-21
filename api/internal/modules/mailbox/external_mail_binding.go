package mailbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ExternalMailBindingService binds outside accounts. Every method answers as if the feature did
// not exist while the instance enables no connector.
type ExternalMailBindingService interface {
	// ListExternalMailConnectors lists the connectors members can bind with; empty when off.
	ListExternalMailConnectors(ctx context.Context) []ExternalMailConnector
	// StartExternalMailAuthorization begins a sign-in to bind a new account, or, with a
	// connection id, to restore one of the viewer's own connections.
	StartExternalMailAuthorization(ctx context.Context, viewer domain.OrganizationContext, input ExternalMailAuthorizationInput) (ExternalMailAuthorizationStart, error)
	// CompleteExternalMailAuthorization finishes the viewer's sign-in with the provider's code.
	// created is true when a new external mailbox and connection were made, false when an existing
	// connection was restored.
	CompleteExternalMailAuthorization(ctx context.Context, viewer domain.OrganizationContext, state, code string) (connection *domain.ExternalMailConnection, created bool, err error)
}

// ExternalMailAuthorizationInput starts a sign-in.
type ExternalMailAuthorizationInput struct {
	// Provider is the connector id; for a reconnect it must be the connection's or empty.
	Provider domain.ExternalMailProvider
	// ConnectionID restores that connection of the viewer; nil binds a new account.
	ConnectionID *uint
	// BackfillDays is how far back a new binding first reads; zero takes the default.
	BackfillDays int
}

// ExternalMailAuthorizationStart is where to send the member's browser.
type ExternalMailAuthorizationStart struct {
	AuthorizationURL string
	ExpiresAt        time.Time
}

var _ ExternalMailBindingService = (*service)(nil)

func (s *service) ListExternalMailConnectors(context.Context) []ExternalMailConnector {
	if !s.externalMailAvailable() {
		return nil
	}
	return s.externalConnectors.list()
}

func (s *service) StartExternalMailAuthorization(ctx context.Context, viewer domain.OrganizationContext, input ExternalMailAuthorizationInput) (ExternalMailAuthorizationStart, error) {
	if !s.externalMailAvailable() || viewer.UserID == 0 {
		return ExternalMailAuthorizationStart{}, ErrExternalMailProviderUnavailable
	}
	backfill := input.BackfillDays
	if backfill == 0 {
		backfill = domain.DefaultExternalMailBackfillDays
	}
	if backfill < 1 || backfill > domain.MaxExternalMailBackfillDays {
		return ExternalMailAuthorizationStart{}, ErrExternalMailAuthorizationInvalid
	}
	provider := input.Provider
	if input.ConnectionID != nil {
		connection, err := s.externalMail.FindExternalMailConnection(ctx, viewer.OrganizationID, *input.ConnectionID)
		if err != nil {
			return ExternalMailAuthorizationStart{}, err
		}
		// Only the member who bound an account signs in to it again.
		if connection.OwnerUserID == nil || *connection.OwnerUserID != viewer.UserID {
			return ExternalMailAuthorizationStart{}, ErrExternalMailConnectionNotFound
		}
		if provider != "" && provider != connection.Provider {
			return ExternalMailAuthorizationStart{}, ErrExternalMailAuthorizationInvalid
		}
		provider = connection.Provider
	}
	connector, registered := s.externalConnectors.lookup(provider)
	if !registered || !connector.Capabilities.Read {
		return ExternalMailAuthorizationStart{}, ErrExternalMailProviderUnavailable
	}

	state, err := randomToken()
	if err != nil {
		return ExternalMailAuthorizationStart{}, err
	}
	verifier, err := randomToken()
	if err != nil {
		return ExternalMailAuthorizationStart{}, err
	}
	sealedVerifier, err := s.encryptor.EncryptString(verifier)
	if err != nil {
		return ExternalMailAuthorizationStart{}, err
	}
	now := s.now().UTC()
	authorization := &domain.ExternalMailAuthorization{
		OrganizationID: viewer.OrganizationID, UserID: viewer.UserID, Provider: provider,
		StateHash: hashState(state), SealedVerifier: sealedVerifier, ConnectionID: input.ConnectionID,
		BackfillDays: backfill, ExpiresAt: now.Add(domain.ExternalMailAuthorizationTTL), CreatedAt: now,
	}
	if err := s.externalMail.CreateExternalMailAuthorization(ctx, authorization); err != nil {
		return ExternalMailAuthorizationStart{}, err
	}
	slog.InfoContext(ctx, "external_mail.authorization_started",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("user_id", uint64(viewer.UserID)),
		slog.String("provider", string(provider)), slog.Bool("reconnect", input.ConnectionID != nil))
	return ExternalMailAuthorizationStart{
		AuthorizationURL: connector.Source.AuthURL(ExternalMailAuthRequest{
			State: state, CodeChallenge: pkceChallenge(verifier), RedirectURI: s.settings.ExternalMail.RedirectURL,
		}),
		ExpiresAt: authorization.ExpiresAt,
	}, nil
}

func (s *service) CompleteExternalMailAuthorization(ctx context.Context, viewer domain.OrganizationContext, state, code string) (*domain.ExternalMailConnection, bool, error) {
	if !s.externalMailAvailable() || viewer.UserID == 0 {
		return nil, false, ErrExternalMailAuthorizationNotFound
	}
	state, code = strings.TrimSpace(state), strings.TrimSpace(code)
	if state == "" || code == "" {
		return nil, false, ErrExternalMailAuthorizationNotFound
	}
	// Taking the sign-in deletes it, so a state works once even when the exchange then fails.
	authorization, err := s.externalMail.TakeExternalMailAuthorization(ctx, hashState(state), s.now().UTC())
	if err != nil {
		return nil, false, err
	}
	if authorization.OrganizationID != viewer.OrganizationID || authorization.UserID != viewer.UserID {
		return nil, false, ErrExternalMailAuthorizationNotFound
	}
	connector, registered := s.externalConnectors.lookup(authorization.Provider)
	if !registered {
		return nil, false, ErrExternalMailProviderUnavailable
	}
	verifier, err := s.encryptor.DecryptString(authorization.SealedVerifier)
	if err != nil {
		return nil, false, ErrExternalMailAuthorizationNotFound
	}
	token, account, err := connector.Source.Exchange(ctx, ExternalMailCodeExchange{
		Code: code, CodeVerifier: verifier, RedirectURI: s.settings.ExternalMail.RedirectURL,
	})
	if err != nil {
		if errors.Is(err, ErrExternalMailAuth) {
			return nil, false, ErrExternalMailAuthorizationFailed
		}
		return nil, false, err
	}
	address := strings.ToLower(strings.TrimSpace(account.Address))
	if address == "" || len(address) > 320 || token.RefreshToken == "" {
		return nil, false, ErrExternalMailAuthorizationFailed
	}
	sealed, err := s.sealRefreshToken(token.RefreshToken)
	if err != nil {
		return nil, false, err
	}

	var connection *domain.ExternalMailConnection
	created := false
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		existing, findErr := s.externalMail.FindExternalMailConnectionByAccount(tx, viewer.OrganizationID, authorization.Provider, address)
		switch {
		case errors.Is(findErr, ErrExternalMailConnectionNotFound):
			if authorization.ConnectionID != nil {
				return ErrExternalMailAccountMismatch
			}
			connection, findErr = s.bindExternalAccount(tx, viewer, authorization, address, sealed)
			created = findErr == nil
			return findErr
		case findErr != nil:
			return findErr
		}
		if existing.OwnerUserID == nil || *existing.OwnerUserID != viewer.UserID {
			return ErrExternalMailAccountTaken
		}
		if authorization.ConnectionID != nil && *authorization.ConnectionID != existing.ID {
			return ErrExternalMailAccountMismatch
		}
		connection, findErr = s.externalMail.RebindExternalMailConnection(tx, domain.ExternalMailRebind{
			ConnectionID: existing.ID, EncryptedRefreshToken: sealed,
			ResetCursor: existing.Status == domain.ExternalMailStatusRevoked, Now: s.now().UTC(),
		})
		return findErr
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		// Another sign-in bound the same account in the meantime.
		err = ErrExternalMailAccountTaken
	}
	if err != nil {
		if errors.Is(err, ErrExternalMailAccountMismatch) || errors.Is(err, ErrExternalMailAccountTaken) ||
			errors.Is(err, ErrExternalMailConnectionLimit) {
			// The grant is not kept, so it is handed back to the provider.
			if revokeErr := connector.Source.Revoke(ctx, token); revokeErr != nil {
				slog.WarnContext(ctx, "external_mail.provider_revoke_failed", slog.String("provider", string(authorization.Provider)))
			}
		}
		return nil, false, err
	}
	event := "external_mail.reconnected"
	if created {
		event = "external_mail.bound"
	}
	slog.InfoContext(ctx, event,
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("user_id", uint64(viewer.UserID)),
		slog.Uint64("connection_id", uint64(connection.ID)), slog.String("provider", string(connection.Provider)))
	return connection, created, nil
}

// bindExternalAccount creates the private, read-only external mailbox of a new account and its
// connection, due for its first sync at once.
func (s *service) bindExternalAccount(tx context.Context, viewer domain.OrganizationContext, authorization *domain.ExternalMailAuthorization, address, sealed string) (*domain.ExternalMailConnection, error) {
	owned, err := s.externalMail.ListExternalMailConnections(tx, viewer.OrganizationID, viewer.UserID)
	if err != nil {
		return nil, err
	}
	if len(owned) >= domain.MaxExternalMailConnectionsPerUser {
		return nil, ErrExternalMailConnectionLimit
	}
	owner := viewer.UserID
	mailbox := &domain.Mailbox{
		OrganizationID: viewer.OrganizationID, CreatedBy: viewer.UserID, Address: address, DisplayName: address,
		Type: domain.MailboxTypeExternal, OwnerUserID: &owner,
	}
	// External mailboxes are a member's own accounts; the per-user limit bounds them, not the
	// organization's mailbox limit.
	if err := s.repo.CreateMailbox(tx, mailbox, domain.MailboxesPerOrganizationCeiling); err != nil {
		return nil, err
	}
	connection := &domain.ExternalMailConnection{
		OrganizationID: viewer.OrganizationID, MailboxID: mailbox.ID, Provider: authorization.Provider,
		AccountAddress: address, OwnerUserID: &owner, EncryptedRefreshToken: sealed,
		BackfillDays: authorization.BackfillDays, NextSyncAt: s.now().UTC(),
	}
	if err := s.externalMail.CreateExternalMailConnection(tx, connection); err != nil {
		return nil, err
	}
	return connection, nil
}

// randomToken is 32 random bytes, base64url without padding: a state or a PKCE verifier.
func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// hashState is the stored form of a state.
func hashState(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}
