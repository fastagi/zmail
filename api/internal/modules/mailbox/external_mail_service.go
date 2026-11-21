package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// externalMailSyncInterval is how often one connection is polled; it is also the claim lease,
	// so a worker that dies mid-fetch hands the connection back one interval later.
	externalMailSyncInterval = 2 * time.Minute
	// maxExternalIDLength matches mail_messages.external_id.
	maxExternalIDLength = 255
)

var _ domain.ExternalMailSyncWorker = (*service)(nil)

// externalMailAvailable reports whether the instance enables a source and can seal its tokens.
func (s *service) externalMailAvailable() bool {
	return s.externalMail != nil && s.settings.ExternalMail.Enabled() && s.encryptor != nil
}

// sealRefreshToken encrypts a refresh token for storage.
func (s *service) sealRefreshToken(token string) (string, error) {
	if s.encryptor == nil {
		return "", ErrExternalMailUnavailable
	}
	return s.encryptor.EncryptString(token)
}

// SyncExternalMail polls up to limit due connections. A failure of one connection is recorded on
// it and does not stop the others; only a store failure ends the pass.
func (s *service) SyncExternalMail(ctx context.Context, limit int) (int, error) {
	if !s.externalMailAvailable() || limit <= 0 {
		return 0, nil
	}
	now := s.now().UTC()
	connections, err := s.externalMail.ClaimExternalMailConnections(ctx, now, now.Add(externalMailSyncInterval), limit)
	if err != nil {
		return 0, err
	}
	for _, connection := range connections {
		if err := ctx.Err(); err != nil {
			return len(connections), err
		}
		if err := s.syncExternalConnection(ctx, connection, now); err != nil {
			return len(connections), err
		}
	}
	return len(connections), nil
}

// syncExternalConnection fetches one claimed connection and records the outcome. A refused token
// moves the connection to needs_reconnect, which stops polling; any other fetch failure keeps it
// active for the next interval with the cursor unchanged.
func (s *service) syncExternalConnection(ctx context.Context, connection *domain.ExternalMailConnection, now time.Time) error {
	result := domain.ExternalMailSyncResult{
		ConnectionID: connection.ID, MailboxID: connection.MailboxID, Cursor: connection.SyncCursor,
		Status: domain.ExternalMailStatusActive, NextSyncAt: now.Add(externalMailSyncInterval), Now: now,
	}
	connector, registered := s.externalConnectors.lookup(connection.Provider)
	refreshToken, err := s.encryptor.DecryptString(connection.EncryptedRefreshToken)
	var fetched ExternalMailFetch
	switch {
	case !registered:
		err = ErrExternalMailProviderUnavailable
	case err == nil:
		fetched, err = connector.Source.Fetch(ctx, ExternalMailToken{RefreshToken: refreshToken}, connection.SyncCursor)
	default:
		// A token this instance cannot open is as unusable as one the provider refuses.
		err = fmt.Errorf("%w: sealed token unreadable", ErrExternalMailAuth)
	}
	switch {
	case err == nil:
		result.Cursor, result.SyncedAt = fetched.Cursor, &now
		result.Messages = s.externalCopies(connection, fetched.Messages, now)
	case errors.Is(err, ErrExternalMailAuth):
		result.Status, result.LastErrorCode = domain.ExternalMailStatusNeedsReconnect, domain.ExternalMailErrorAuthFailed
	case errors.Is(err, ErrExternalMailProviderUnavailable):
		// The connection keeps its grant and cursor and resumes when the connector is enabled again.
		result.LastErrorCode = domain.ExternalMailErrorProviderUnavailable
	default:
		result.LastErrorCode = domain.ExternalMailErrorFetchFailed
	}
	mailbox, err := s.repo.FindMailbox(ctx, connection.OrganizationID, connection.MailboxID)
	if errors.Is(err, ErrMailboxNotFound) {
		// A deleted external mailbox takes no more mail; its lease keeps it out of the next passes.
		slog.WarnContext(ctx, "external_mail.mailbox_missing",
			slog.Uint64("connection_id", uint64(connection.ID)), slog.Uint64("mailbox_id", uint64(connection.MailboxID)))
		return nil
	}
	if err != nil {
		return err
	}
	var stored []*domain.MailMessage
	applied := false
	// New copies go through the same post-store step as Zmail's own inbound mail, in the
	// transaction that stores them.
	saveErr := s.apply(ctx, func(tx context.Context) ([]MailNotification, error) {
		var notifications []MailNotification
		var err error
		if stored, applied, err = s.externalMail.SaveExternalMailSync(tx, result); err != nil {
			return nil, err
		}
		for _, message := range stored {
			announced, err := s.afterInboundStored(tx, mailbox, message)
			if err != nil {
				return nil, err
			}
			notifications = append(notifications, announced...)
		}
		return notifications, nil
	})
	if saveErr != nil {
		return saveErr
	}
	attributes := []any{
		slog.Uint64("connection_id", uint64(connection.ID)), slog.Uint64("mailbox_id", uint64(connection.MailboxID)),
		slog.String("provider", string(connection.Provider)), slog.String("status", string(result.Status)),
		slog.Int("fetched", len(result.Messages)), slog.Int("stored", len(stored)), slog.Bool("applied", applied),
	}
	if result.LastErrorCode != "" {
		slog.WarnContext(ctx, "external_mail.sync_failed", append(attributes, slog.String("error_code", result.LastErrorCode))...)
		return nil
	}
	slog.InfoContext(ctx, "external_mail.synced", attributes...)
	return nil
}

// externalCopies turns fetched messages into inbound copies for the connection's mailbox. A
// message without a usable external id cannot be deduplicated and is skipped.
func (s *service) externalCopies(connection *domain.ExternalMailConnection, fetched []ExternalMessage, now time.Time) []*domain.MailMessage {
	copies := make([]*domain.MailMessage, 0, len(fetched))
	for _, external := range fetched {
		if external.ExternalID == "" || len(external.ExternalID) > maxExternalIDLength {
			continue
		}
		receivedAt := external.ReceivedAt.UTC()
		if external.ReceivedAt.IsZero() {
			receivedAt = now
		}
		copies = append(copies, &domain.MailMessage{
			MailboxID: connection.MailboxID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
			FromAddress: truncateText(external.FromAddress, 320), FromName: truncateText(external.FromName, 255),
			To: external.To, Cc: external.Cc, Subject: truncateText(external.Subject, 998),
			TextBody: external.TextBody, HTMLBody: external.HTMLBody,
			MessageID: truncateText(external.RFCMessageID, 998), RFCMessageID: truncateText(external.RFCMessageID, 998),
			InReplyTo: truncateText(external.InReplyTo, 998), References: external.References,
			Placement: domain.MailPlacementDefault, Source: connection.Provider.Source(), ExternalID: external.ExternalID,
			CreatedAt: receivedAt,
		})
	}
	return copies
}

// ListExternalMailConnections lists the viewer's own connections. It answers
// ErrExternalMailConnectionNotFound while the instance enables no source.
func (s *service) ListExternalMailConnections(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.ExternalMailConnection, error) {
	if !s.externalMailAvailable() {
		return nil, ErrExternalMailConnectionNotFound
	}
	return s.externalMail.ListExternalMailConnections(ctx, viewer.OrganizationID, viewer.UserID)
}

// RevokeExternalMailConnection unbinds an account: its owner may, and so may owners and admins for
// offboarding. The provider grant is revoked on a best-effort basis, then the connection stops for
// good and its token is erased. Mail already synced stays in the external mailbox.
func (s *service) RevokeExternalMailConnection(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	if !s.externalMailAvailable() {
		return ErrExternalMailConnectionNotFound
	}
	connection, err := s.externalMail.FindExternalMailConnection(ctx, viewer.OrganizationID, id)
	if err != nil {
		return err
	}
	owner := connection.OwnerUserID != nil && viewer.UserID != 0 && *connection.OwnerUserID == viewer.UserID
	if !owner && !viewer.Role.CanManageOrganization() {
		return ErrExternalMailConnectionNotFound
	}
	if connection.Status == domain.ExternalMailStatusRevoked {
		return nil
	}
	if connection.EncryptedRefreshToken != "" {
		if token, openErr := s.encryptor.DecryptString(connection.EncryptedRefreshToken); openErr == nil {
			connector, registered := s.externalConnectors.lookup(connection.Provider)
			if !registered {
				slog.WarnContext(ctx, "external_mail.provider_revoke_skipped",
					slog.Uint64("connection_id", uint64(connection.ID)), slog.String("provider", string(connection.Provider)))
			} else if revokeErr := connector.Source.Revoke(ctx, ExternalMailToken{RefreshToken: token}); revokeErr != nil {
				slog.WarnContext(ctx, "external_mail.provider_revoke_failed",
					slog.Uint64("connection_id", uint64(connection.ID)), slog.String("provider", string(connection.Provider)))
			}
		}
	}
	if err := s.externalMail.RevokeExternalMailConnection(ctx, viewer.OrganizationID, id, s.now().UTC()); err != nil {
		return err
	}
	slog.InfoContext(ctx, "external_mail.revoked",
		slog.Uint64("connection_id", uint64(connection.ID)), slog.String("provider", string(connection.Provider)))
	return nil
}
