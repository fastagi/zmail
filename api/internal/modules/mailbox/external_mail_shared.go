package mailbox

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// sharedBinding is a checked shared target: the display name (empty takes the account address) and
// the first members, each resolved to a current membership.
type sharedBinding struct {
	displayName string
	members     []resolvedMember
}

// checkSharing applies the rules of creating a shared mailbox to a shared binding: only owners and
// admins may bind a team account, and its members follow POST /v1/mailboxes (1 to 100 current
// members, each once, with view, send, or manage). It runs when a sign-in starts and again when it
// completes, since memberships may change in between.
func (s *service) checkSharing(ctx context.Context, viewer domain.OrganizationContext, sharing *domain.ExternalMailSharing) (*sharedBinding, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	members := make([]MailboxMemberInput, 0, len(sharing.Members))
	for _, member := range sharing.Members {
		members = append(members, MailboxMemberInput{UserID: member.UserID, Permission: member.Permission})
	}
	if err := validateMailboxAccess(CreateMailboxInput{Type: domain.MailboxTypeShared, Members: members}); err != nil {
		return nil, err
	}
	checked := &sharedBinding{}
	if strings.TrimSpace(sharing.DisplayName) != "" {
		var err error
		if checked.displayName, err = normalizeDisplayName(sharing.DisplayName); err != nil {
			return nil, err
		}
	}
	resolved, err := s.resolveMembers(ctx, viewer.OrganizationID, members)
	if err != nil {
		return nil, err
	}
	checked.members = resolved
	return checked, nil
}

// normalizedSharing is the stored form of a checked shared target.
func normalizedSharing(checked *sharedBinding) *domain.ExternalMailSharing {
	sharing := &domain.ExternalMailSharing{DisplayName: checked.displayName}
	for _, member := range checked.members {
		sharing.Members = append(sharing.Members, domain.ExternalMailSharingMember{UserID: member.userID, Permission: member.permission})
	}
	return sharing
}

// mayManageConnection reports whether the viewer reconnects, unbinds, and reads the sync history of
// a connection: a member's own account is theirs alone; a shared connection is managed by whoever
// manages the members of its mailbox, owners and admins included. A shared connection whose mailbox
// was deleted is managed by nobody.
func (s *service) mayManageConnection(ctx context.Context, viewer domain.OrganizationContext, connection *domain.ExternalMailConnection) (bool, error) {
	if !connection.Shared {
		return connection.OwnerUserID != nil && viewer.UserID != 0 && *connection.OwnerUserID == viewer.UserID, nil
	}
	mailbox, err := s.repo.FindMailbox(ctx, viewer.OrganizationID, connection.MailboxID)
	if errors.Is(err, ErrMailboxNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	access, err := s.accessFor(ctx, viewer, mailbox)
	if err != nil {
		return false, err
	}
	return access.ManageMembers, nil
}

// managedConnection returns a connection of the organization the viewer may manage; any other is
// answered not found.
func (s *service) managedConnection(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.ExternalMailConnection, error) {
	connection, err := s.externalMail.FindExternalMailConnection(ctx, viewer.OrganizationID, id)
	if err != nil {
		return nil, err
	}
	may, err := s.mayManageConnection(ctx, viewer, connection)
	if err != nil {
		return nil, err
	}
	if !may {
		return nil, ErrExternalMailConnectionNotFound
	}
	return connection, nil
}

// checkSharedConnectionLimit refuses one more live shared connection once the organization holds
// its limit. Revoked ones do not count. It runs under the organization's shared binding lock.
func (s *service) checkSharedConnectionLimit(tx context.Context, organizationID uint) error {
	live, err := s.externalMail.CountLiveSharedExternalMailConnections(tx, organizationID)
	if err != nil {
		return err
	}
	if live >= s.settings.ExternalMail.SharedConnectionLimit() {
		return ErrExternalMailConnectionLimit
	}
	return nil
}

// bindSharedAccount creates the shared, read-only connected mailbox of a team account with its first
// members and access events, as a shared mailbox is created, and the organization's connection to
// it, due for its first sync at once. Like every connected mailbox it is outside the organization's
// mailbox limit; the organization's shared connection limit bounds it instead.
func (s *service) bindSharedAccount(tx context.Context, viewer domain.OrganizationContext, binding externalBinding, sealed string) (*domain.ExternalMailConnection, error) {
	if err := s.checkSharedConnectionLimit(tx, viewer.OrganizationID); err != nil {
		return nil, err
	}
	displayName := binding.sharing.displayName
	if displayName == "" {
		displayName = binding.address
	}
	mailbox := &domain.Mailbox{
		OrganizationID: viewer.OrganizationID, CreatedBy: viewer.UserID, Address: binding.address, DisplayName: displayName,
		Type: domain.MailboxTypeShared, Origin: domain.MailboxOriginConnected,
	}
	if err := s.repo.CreateMailbox(tx, mailbox, 0); err != nil {
		return nil, err
	}
	if err := s.recordAccess(tx, mailbox, domain.MailboxAccessCreated, viewer.UserID, 0, map[string]any{
		"type": string(mailbox.Type), "members": len(binding.sharing.members),
	}); err != nil {
		return nil, err
	}
	for _, member := range binding.sharing.members {
		if _, err := s.setMember(tx, viewer, mailbox, member); err != nil {
			return nil, err
		}
	}
	binder := viewer.UserID
	connection := &domain.ExternalMailConnection{
		OrganizationID: viewer.OrganizationID, MailboxID: mailbox.ID, Provider: binding.provider,
		AccountAddress: binding.address, Shared: true, BoundByUserID: &binder, EncryptedRefreshToken: sealed,
		BackfillDays: binding.backfillDays, NextSyncAt: s.now().UTC(),
	}
	if err := s.externalMail.CreateExternalMailConnection(tx, connection); err != nil {
		return nil, err
	}
	if err := s.recordConnectionEvent(tx, connection, domain.MailboxAccessConnectionBound, viewer.UserID); err != nil {
		return nil, err
	}
	return connection, nil
}

// saveOverShared handles a fresh grant for an account that is already a shared connection of the
// organization. A reconnect of that connection by someone who manages it restores it; a reconnect of
// another connection signed in to the wrong account. A new binding, personal or shared, takes the
// account over only once the connection is revoked and its mailbox deleted; until then the mailbox
// is restored by a reconnect, never captured.
func (s *service) saveOverShared(tx context.Context, viewer domain.OrganizationContext, connector ExternalMailConnector, token ExternalMailToken, binding externalBinding, existing *domain.ExternalMailConnection, sealed string) (*domain.ExternalMailConnection, bool, error) {
	if binding.connectionID != nil {
		if *binding.connectionID != existing.ID {
			return nil, false, ErrExternalMailAccountMismatch
		}
		may, err := s.mayManageConnection(tx, viewer, existing)
		if err != nil {
			return nil, false, err
		}
		if !may {
			return nil, false, ErrExternalMailConnectionNotFound
		}
		if existing.Status == domain.ExternalMailStatusRevoked {
			if err := s.checkSharedConnectionLimit(tx, viewer.OrganizationID); err != nil {
				return nil, false, err
			}
		}
		connection, err := s.externalMail.RebindExternalMailConnection(tx, domain.ExternalMailRebind{
			ConnectionID: existing.ID, EncryptedRefreshToken: sealed,
			ResetCursor: existing.Status == domain.ExternalMailStatusRevoked || !s.sameExternalMailbox(connector, existing, token),
			Now:         s.now().UTC(),
		})
		if err != nil {
			return nil, false, err
		}
		return connection, false, s.recordConnectionEvent(tx, connection, domain.MailboxAccessConnectionReconnected, viewer.UserID)
	}
	if existing.Status != domain.ExternalMailStatusRevoked {
		return nil, false, ErrExternalMailAccountTaken
	}
	if _, err := s.repo.FindMailbox(tx, existing.OrganizationID, existing.MailboxID); err == nil {
		return nil, false, ErrExternalMailAccountTaken
	} else if !errors.Is(err, ErrMailboxNotFound) {
		return nil, false, err
	}
	if err := s.purgeForRebind(tx, viewer, existing); err != nil {
		return nil, false, err
	}
	connection, err := s.bindAccount(tx, viewer, binding, sealed)
	return connection, err == nil, err
}

// bindAccount binds a new account as the binding asks: shared into a new shared connected mailbox,
// otherwise as the viewer's own.
func (s *service) bindAccount(tx context.Context, viewer domain.OrganizationContext, binding externalBinding, sealed string) (*domain.ExternalMailConnection, error) {
	if binding.sharing != nil {
		return s.bindSharedAccount(tx, viewer, binding, sealed)
	}
	return s.bindExternalAccount(tx, viewer, binding, sealed)
}

// disconnectShared revokes an organization's connection for someone who manages it, records
// connection_revoked on its mailbox, and, when it changed anything, writes the organization audit
// log, so every owner and admin sees who disconnected the team account. Neither carries the address.
func (s *service) disconnectShared(ctx context.Context, viewer domain.OrganizationContext, connection *domain.ExternalMailConnection) error {
	live := connection.Status != domain.ExternalMailStatusRevoked
	if err := s.revokeConnection(ctx, connection, viewer.UserID, domain.MailboxAccessConnectionRevoked); err != nil {
		return err
	}
	if live {
		s.auditChange(ctx, auditstarter.Change{
			Action: "delete", Resource: "external_mail_connections", TargetType: "external_mail_connection",
			TargetID: strconv.FormatUint(uint64(connection.ID), 10), Result: domain.AuditResultSuccess,
			Metadata: map[string]any{
				"provider": string(connection.Provider), "reason": "shared_disconnect", "mailbox_id": connection.MailboxID,
			},
		})
	}
	return nil
}
