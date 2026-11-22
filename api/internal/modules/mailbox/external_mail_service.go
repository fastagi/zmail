package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/metrics"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
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
	// Provider calls left by revocations are made, subscriptions of ended connections stop, and
	// notifications that arrived during a fetch make their connection due, before the claim picks
	// due connections.
	s.processProviderReleases(ctx, now)
	s.maintainExternalMailPush(ctx, now)
	connections, err := s.externalMail.ClaimExternalMailConnections(ctx, domain.ExternalMailClaim{
		Now: now, LeaseUntil: now.Add(externalMailSyncInterval), Limit: limit,
		PerProvider: s.settings.ExternalMail.Sync.FetchesPerPass(),
	})
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
	// Pruning is bounded and does not count as handled connections, so it never keeps the worker
	// from waiting between passes. The purge of revoked copies runs in the mail worker instead, so
	// it does not depend on any provider being enabled.
	s.pruneExternalMailSyncRuns(ctx, now)
	return len(connections), nil
}

// syncExternalConnection fetches one claimed connection and records the outcome with a sync run.
// A refused token moves the connection to needs_reconnect, which stops polling; any other fetch
// failure keeps it active with the cursor unchanged and backs off.
func (s *service) syncExternalConnection(ctx context.Context, connection *domain.ExternalMailConnection, now time.Time) error {
	result := domain.ExternalMailSyncResult{
		ConnectionID: connection.ID, MailboxID: connection.MailboxID, Cursor: connection.SyncCursor,
		Status: domain.ExternalMailStatusActive, NextSyncAt: now.Add(externalMailSyncInterval), Now: now,
		ConsecutiveFailures: connection.ConsecutiveFailures, ConsecutiveMore: connection.ConsecutiveMore,
	}
	budget := s.settings.ExternalMail.Sync
	spacing := budget.Spacing()
	timedOut, throttled := false, false
	startedAt, started := s.now().UTC(), time.Now()
	mailbox, err := s.repo.FindMailbox(ctx, connection.OrganizationID, connection.MailboxID)
	if errors.Is(err, ErrMailboxNotFound) {
		return s.dropConnectionWithoutMailbox(ctx, connection, result, startedAt)
	}
	if err != nil {
		return err
	}
	// The claim lease covers the wait in this pass; before the fetch it is moved past the fetch
	// deadline, so no other worker can claim the connection, and use its grant, while the fetch
	// may still run. A connection another worker took meanwhile is skipped.
	lease := maxTime(connection.NextSyncAt, s.now().UTC().Add(budget.Timeout()+externalMailLeaseMargin))
	held, err := s.externalMail.RenewExternalMailLease(ctx, connection.ID, connection.NextSyncAt, lease)
	if err != nil || !held {
		return err
	}
	connector, registered := s.externalConnectors.lookup(connection.Provider)
	// A notification received before the fetch starts is answered by it; one received later keeps
	// the connection pending for another sync.
	push := s.pushSource(connector)
	var subscription *domain.ExternalMailSubscription
	if push != nil {
		if subscription, err = s.pushStore.FindExternalMailSubscription(ctx, connection.ID); err != nil {
			return err
		}
	}
	trigger := domain.ExternalMailSyncTriggerPoll
	if subscription != nil && subscription.PendingAt != nil && !subscription.PendingAt.After(startedAt) {
		trigger = domain.ExternalMailSyncTriggerPush
		metrics.ObserveExternalMailPushDelay(string(connection.Provider), startedAt.Sub(*subscription.PendingAt))
	}
	var fetched ExternalMailFetch
	var token ExternalMailToken
	var tenant *domain.ExternalMailTenant
	if !registered {
		err = ErrExternalMailProviderUnavailable
	} else {
		// Each fetch has its own deadline, a tenant's token minting included, so a slow or hostile
		// provider cannot stall the pass.
		fetchCtx, cancel := context.WithTimeout(ctx, budget.Timeout())
		token, tenant, err = s.connectionToken(fetchCtx, connection, connector)
		if err == nil {
			fetched, err = connector.Source.Fetch(fetchCtx, ExternalMailFetchRequest{
				Token: token, Cursor: connection.SyncCursor,
				AccountAddress: connection.AccountAddress, BackfillDays: connection.BackfillDays, Now: now,
				NotBefore: timeOrZero(connection.ReceivedNotBefore),
			})
		}
		timedOut = errors.Is(fetchCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancel()
		if err != nil && ctx.Err() != nil {
			// The worker is stopping: nothing is recorded, and the lease hands the connection
			// back one interval later.
			return ctx.Err()
		}
		if timedOut {
			// Whatever the source answered after its deadline is not trusted: a timeout is a
			// fetch failure and backs off.
			fetched, err = ExternalMailFetch{RefreshToken: fetched.RefreshToken}, errExternalMailFetchTimeout
		}
	}
	latency := time.Since(started)
	var upkeep pushUpkeep
	if push != nil && err == nil && !fetched.More && subscriptionDue(subscription, now) {
		// The subscription is started or renewed under the lease, moved past the call's deadline,
		// with the grant the fetch used; a grant the provider rotates on the way is saved below.
		renewed, renewErr := s.externalMail.RenewExternalMailLease(ctx, connection.ID, lease,
			maxTime(lease, s.now().UTC().Add(externalMailPushCallTimeout+externalMailLeaseMargin)))
		if renewErr != nil {
			return renewErr
		}
		if renewed {
			pushToken := token
			if fetched.RefreshToken != "" {
				pushToken.RefreshToken = fetched.RefreshToken
			}
			var rotated string
			upkeep, rotated = s.upkeepPush(ctx, push, connection, subscription, pushToken, now)
			if rotated != "" {
				fetched.RefreshToken = rotated
			}
		}
	} else {
		upkeep = pushUpkeep{after: subscription}
	}
	if push == nil {
		upkeep = pushUpkeep{}
	}
	outcome := domain.ExternalMailSyncOK
	if errors.Is(err, ErrExternalMailCursorExpired) && connection.SyncCursor == "" {
		// An empty cursor cannot expire: a source that says so would restart forever.
		err = errExternalMailCursorLoop
	}
	if fetched.RefreshToken != "" && fetched.RefreshToken != token.RefreshToken {
		// A provider that rotates grants has already spent the old one, so the new one is kept
		// even when the rest of the fetch failed.
		sealed, sealErr := s.sealRefreshToken(fetched.RefreshToken)
		if sealErr != nil {
			return sealErr
		}
		result.EncryptedRefreshToken = sealed
	}
	switch {
	case err == nil:
		result.Cursor, result.SyncedAt, result.ConsecutiveFailures = fetched.Cursor, &now, 0
		result.Messages = s.externalCopies(connection, fetched.Messages, now)
		result.ConsecutiveMore = 0
		if !fetched.More {
			// A live subscription announces new mail, so the poll is only the safety net.
			result.NextSyncAt = s.pushPollAt(upkeep, now)
		}
		if fetched.More {
			// Another page is ready: fetch it at once, until the connection has fetched the most
			// pages in a row allowed; then each further page waits the spacing, so one backfill
			// cannot monopolize the worker.
			result.ConsecutiveMore = min(connection.ConsecutiveMore+1, maxConsecutiveFailures)
			result.NextSyncAt = now
			if result.ConsecutiveMore >= budget.ImmediatePages() {
				result.NextSyncAt, throttled = now.Add(spacing), true
			}
		}
	case errors.Is(err, ErrExternalMailCursorExpired):
		// Start over from an empty cursor at once, in the next pass; copies already stored are
		// not repeated. The restart is a new backfill, so its pages in a row count from zero.
		outcome = domain.ExternalMailSyncCursorExpired
		result.Cursor, result.NextSyncAt, result.ConsecutiveFailures, result.ConsecutiveMore = "", now, 0, 0
		slog.InfoContext(ctx, "external_mail.cursor_expired",
			slog.Uint64("connection_id", uint64(connection.ID)), slog.String("provider", string(connection.Provider)))
	case errors.Is(err, ErrExternalMailAuth):
		outcome = domain.ExternalMailSyncAuthFailed
		result.Status, result.LastErrorCode = domain.ExternalMailStatusNeedsReconnect, domain.ExternalMailErrorAuthFailed
	case errors.Is(err, ErrExternalMailProviderUnavailable):
		// The connection keeps its grant and cursor and resumes when the connector is enabled again.
		outcome = domain.ExternalMailSyncProviderUnavailable
		result.LastErrorCode = domain.ExternalMailErrorProviderUnavailable
	default:
		outcome = domain.ExternalMailSyncFetchFailed
		result.LastErrorCode = domain.ExternalMailErrorFetchFailed
		result.ConsecutiveFailures = min(connection.ConsecutiveFailures+1, maxConsecutiveFailures)
		result.NextSyncAt = now.Add(externalMailBackoff(result.ConsecutiveFailures, externalMailJitter))
	}
	result.Run = &domain.ExternalMailSyncRun{
		ConnectionID: connection.ID, StartedAt: startedAt, FinishedAt: s.now().UTC(), Outcome: outcome,
		Fetched: len(fetched.Messages), More: err == nil && fetched.More, ErrorCode: result.LastErrorCode,
		TimedOut: timedOut, Throttled: throttled, Trigger: trigger,
	}
	// Only an incremental poll that read a whole round can catch push missing mail: a backfill, a
	// restart after an expired cursor, and pages in the middle of a round read mail that was never
	// to be announced. The check itself runs on the copies the save inserts, so a message the
	// provider returns again (Graph delta reports read or flagged messages too) never counts.
	checkMissed := push != nil && trigger == domain.ExternalMailSyncTriggerPoll && outcome == domain.ExternalMailSyncOK &&
		!fetched.More && connection.SyncCursor != "" && connection.ConsecutiveMore == 0
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
		degraded := checkMissed && pushMissedMail(subscription, stored, startedAt, s.settings.ExternalMail.Push.Quiet())
		if err = s.savePushUpkeep(tx, connection, applied, result.LastErrorCode == "", upkeep, degraded, trigger, startedAt); err != nil {
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
	if !applied && push != nil && upkeep.subscribed != nil {
		// The connection was revoked while the sync subscribed: its revocation found no live
		// subscription to stop, so this one is stopped here.
		s.unsubscribeOrphan(ctx, push, connection, *upkeep.subscribed)
	}
	if tenant != nil && result.Run.Outcome == domain.ExternalMailSyncAuthFailed {
		s.checkTenantAfterRefusal(ctx, connector, tenant, connection.ID, budget.Timeout())
	}
	metrics.RecordExternalMailSync(string(connection.Provider), string(result.Run.Outcome), latency)
	attributes := []any{
		slog.Uint64("connection_id", uint64(connection.ID)), slog.Uint64("mailbox_id", uint64(connection.MailboxID)),
		slog.String("provider", string(connection.Provider)), slog.String("status", string(result.Status)),
		slog.String("outcome", string(result.Run.Outcome)), slog.Int64("latency_ms", latency.Milliseconds()),
		slog.Int("page_size", result.Run.Fetched), slog.Bool("more", result.Run.More),
		slog.Bool("timed_out", timedOut), slog.Bool("throttled", throttled), slog.String("trigger", string(trigger)),
		slog.Int("fetched", len(result.Messages)), slog.Int("stored", len(stored)), slog.Bool("applied", applied),
	}
	if result.LastErrorCode != "" && result.Run.Outcome != domain.ExternalMailSyncDropped {
		attributes = append(attributes, slog.Int("consecutive_failures", result.ConsecutiveFailures),
			slog.Time("next_sync_at", result.NextSyncAt))
	}
	if result.LastErrorCode != "" {
		slog.WarnContext(ctx, "external_mail.sync_failed", append(attributes, slog.String("error_code", result.LastErrorCode))...)
		return nil
	}
	slog.InfoContext(ctx, "external_mail.synced", attributes...)
	return nil
}

// timeOrZero is the time, or the zero time for nil.
func timeOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
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
		if connection.ReceivedNotBefore != nil && (external.ReceivedAt.IsZero() || receivedAt.Before(*connection.ReceivedNotBefore)) {
			// Mail received outside this holder's tenure is never stored, whatever the connector
			// returned; one without a receive time cannot be placed, so it is dropped too. Dropped
			// copies show as fetched but not stored in the sync run.
			continue
		}
		if external.ReceivedAt.IsZero() {
			receivedAt = now
		}
		copies = append(copies, &domain.MailMessage{
			MailboxID: connection.MailboxID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
			FromAddress: truncateText(external.FromAddress, 320), FromName: truncateText(external.FromName, 255),
			To: external.To, Cc: external.Cc, Subject: truncateRunes(external.Subject, domain.MaxMailSubjectLength),
			TextBody: external.TextBody, HTMLBody: external.HTMLBody,
			MessageID: truncateText(external.RFCMessageID, 998), RFCMessageID: truncateText(external.RFCMessageID, 998),
			InReplyTo: truncateText(external.InReplyTo, 998), References: external.References,
			Placement: domain.MailPlacementDefault, Source: connection.Provider.Source(), ExternalID: external.ExternalID,
			CreatedAt: receivedAt,
		})
	}
	return copies
}

// ListExternalMailConnections lists the viewer's own connections, then the shared connections
// they manage: every one for owners and admins, those of mailboxes they hold with manage
// otherwise. It answers ErrExternalMailConnectionNotFound while the instance enables no source.
func (s *service) ListExternalMailConnections(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.ExternalMailConnection, error) {
	if !s.externalMailAvailable() {
		return nil, ErrExternalMailConnectionNotFound
	}
	connections, err := s.externalMail.ListExternalMailConnections(ctx, viewer.OrganizationID, viewer.UserID)
	if err != nil {
		return nil, err
	}
	// The organization's mailboxes assigned to the viewer from a tenant follow their own ones.
	assigned, err := s.externalMail.ListTenantPersonalConnections(ctx, viewer.OrganizationID, viewer.UserID, maxListedTenantPersonalConnections)
	if err != nil {
		return nil, err
	}
	connections = append(connections, assigned...)
	manager := viewer.Role.CanManageOrganization()
	if manager || viewer.MembershipID != 0 {
		shared, err := s.externalMail.ListSharedExternalMailConnections(ctx, viewer.OrganizationID, viewer.MembershipID, manager,
			domain.MaxSharedExternalMailConnectionsCeiling)
		if err != nil {
			return nil, err
		}
		connections = append(connections, shared...)
	}
	// Each listed connection shows its push subscription where its sync health is shown.
	if err := s.attachPushState(ctx, connections); err != nil {
		return nil, err
	}
	return connections, nil
}

// RevokeExternalMailConnection unbinds an account: its owner may, and so may owners and admins for
// offboarding, which is recorded as a forced disconnect. A shared connection is unbound by whoever
// manages it. The provider grant is revoked on a
// best-effort basis, then the connection stops for good and its token is erased. Mail already
// synced stays in the connected mailbox until the retention ends or the owner purges it.
func (s *service) RevokeExternalMailConnection(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	if !s.externalMailAvailable() {
		return ErrExternalMailConnectionNotFound
	}
	connection, err := s.externalMail.FindExternalMailConnection(ctx, viewer.OrganizationID, id)
	if err != nil {
		return err
	}
	if connection.TenantID != nil {
		return s.unassignTenantConnection(ctx, viewer, connection)
	}
	if connection.Shared {
		// An organization's connection is unbound by whoever manages it, as a disconnect of the
		// team account rather than a forced one.
		if _, err = s.managedConnection(ctx, viewer, connection.ID); err != nil {
			return err
		}
		return s.disconnectShared(ctx, viewer, connection)
	}
	owner := connection.OwnerUserID != nil && viewer.UserID != 0 && *connection.OwnerUserID == viewer.UserID
	if !owner && !viewer.Role.CanManageOrganization() {
		return ErrExternalMailConnectionNotFound
	}
	if !owner {
		return s.forceDisconnect(ctx, viewer, connection)
	}
	return s.revokeConnection(ctx, connection, viewer.UserID, domain.MailboxAccessConnectionRevoked)
}

// forceDisconnect revokes another member's connection for an owner or admin, records it in the
// mailbox's access history, and, when it changed anything, in the organization audit log so other
// managers see who disconnected which connection. Neither carries the outside address.
func (s *service) forceDisconnect(ctx context.Context, viewer domain.OrganizationContext, connection *domain.ExternalMailConnection) error {
	live := connection.Status != domain.ExternalMailStatusRevoked
	if err := s.revokeConnection(ctx, connection, viewer.UserID, domain.MailboxAccessConnectionForceRevoked); err != nil {
		return err
	}
	if live {
		metadata := map[string]any{"provider": string(connection.Provider), "reason": "forced_disconnect"}
		if connection.OwnerUserID != nil {
			metadata["owner_user_id"] = *connection.OwnerUserID
		}
		s.auditChange(ctx, auditstarter.Change{
			Action: "delete", Resource: "external_mail_connections", TargetType: "external_mail_connection",
			TargetID: strconv.FormatUint(uint64(connection.ID), 10), Result: domain.AuditResultSuccess,
			Metadata: metadata, AlsoVisibleTo: s.otherManagers(ctx, connection.OrganizationID, viewer.UserID),
		})
	}
	return nil
}

// otherManagers returns the organization's owners and admins other than actorUserID, whose audit
// history also receives a forced disconnect. A failed lookup is logged: the disconnect stands.
func (s *service) otherManagers(ctx context.Context, organizationID, actorUserID uint) []uint {
	if s.memberships == nil {
		return nil
	}
	managers, err := s.memberships.ListManagerUserIDs(ctx, organizationID)
	if err != nil {
		slog.WarnContext(ctx, "mailbox.external_mail.list_managers_failed", "organization_id", organizationID, "err", err)
		return nil
	}
	return slices.DeleteFunc(managers, func(userID uint) bool { return userID == actorUserID })
}

// revokeConnection hands a live connection's grant back to its provider, then marks it revoked and
// records the event on its mailbox in one transaction. A connection already revoked changes nothing
// and records nothing.
func (s *service) revokeConnection(ctx context.Context, connection *domain.ExternalMailConnection, actorUserID uint, event domain.MailboxAccessEventType) error {
	if connection.Status == domain.ExternalMailStatusRevoked {
		return nil
	}
	// The provider is called only once the revocation committed: it stops the push subscription,
	// then takes the grant back. What is left to do is recorded in the revocation's transaction, so a
	// process that stops after the commit leaves it to the worker; the row is stopped either way.
	release := s.planProviderRelease(ctx, connection)
	err := s.repo.InTransaction(ctx, func(tx context.Context) error {
		if err := s.externalMail.RevokeExternalMailConnection(tx, connection.OrganizationID, connection.ID, s.now().UTC()); err != nil {
			return err
		}
		if err := s.recordProviderRelease(tx, release); err != nil {
			return err
		}
		return s.afterConnectionRevoked(tx, connection, actorUserID, event, nil)
	})
	if err != nil {
		return err
	}
	if release != nil {
		held := *connection
		releaseAfterCommit(ctx, func(ctx context.Context) { s.performProviderRelease(ctx, &held, release) })
	}
	slog.InfoContext(ctx, "external_mail.revoked",
		slog.Uint64("connection_id", uint64(connection.ID)), slog.String("provider", string(connection.Provider)),
		slog.String("event", string(event)))
	return nil
}

// afterConnectionRevoked ends what a revoked connection's mailbox fed, in the transaction that revoked
// it, and records the event with extra detail.
func (s *service) afterConnectionRevoked(tx context.Context, connection *domain.ExternalMailConnection, actorUserID uint, event domain.MailboxAccessEventType, extra map[string]any) error {
	// A revoked account's copies never reach the model again, whoever revoked it.
	if s.triage != nil {
		if err := s.triage.StopMailboxTriage(tx, connection.MailboxID); err != nil {
			return err
		}
	}
	// Nor an agent: the mailbox's grants end and its drafts go with the connection.
	reason := domain.MailAgentRevokedConnectionRevoked
	if event == domain.MailboxAccessConnectionRevokedLeave {
		reason = domain.MailAgentRevokedOwnerLeft
	}
	if err := s.endAgentMailbox(tx, domain.MailAgentGrantRevocation{
		OrganizationID: connection.OrganizationID, MailboxID: connection.MailboxID,
		Reason: reason, RevokedBy: actorUserID,
	}); err != nil {
		return err
	}
	// Its notifications are ignored from now on.
	if err := s.stopPushRow(tx, connection.ID); err != nil {
		return err
	}
	return s.recordConnectionEventWith(tx, connection, event, actorUserID, extra)
}

// deleteConnectedMailbox revokes the mailbox's connection, then soft-deletes the mailbox, in one
// transaction. A mailbox without a connection is deleted as it is.
func (s *service) deleteConnectedMailbox(ctx context.Context, viewer domain.OrganizationContext, mailbox *domain.Mailbox) error {
	// The revocation's provider calls wait until the deletion has committed.
	ctx, releases := withDeferredReleases(ctx)
	err := s.repo.InTransaction(ctx, func(tx context.Context) error {
		if s.externalMail != nil {
			connection, err := s.externalMail.FindExternalMailConnectionByMailbox(tx, mailbox.OrganizationID, mailbox.ID)
			switch {
			case errors.Is(err, ErrExternalMailConnectionNotFound):
			case err != nil:
				return err
			default:
				if err := s.revokeConnection(tx, connection, viewer.UserID, domain.MailboxAccessConnectionRevoked); err != nil {
					return err
				}
			}
		}
		return s.repo.DeleteMailbox(tx, mailbox.OrganizationID, mailbox.ID)
	})
	if err != nil {
		return err
	}
	releases.run(ctx)
	return nil
}

// recordConnectionEvent writes a connection event to its mailbox's access history. The detail names
// the connection and its provider only, never the outside account's address.
func (s *service) recordConnectionEvent(ctx context.Context, connection *domain.ExternalMailConnection, event domain.MailboxAccessEventType, actorUserID uint) error {
	return s.recordConnectionEventWith(ctx, connection, event, actorUserID, nil)
}

// recordConnectionEventWith is recordConnectionEvent with extra detail, such as a reason. A tenant
// connection's events also name the tenant.
func (s *service) recordConnectionEventWith(ctx context.Context, connection *domain.ExternalMailConnection, event domain.MailboxAccessEventType, actorUserID uint, extra map[string]any) error {
	var subject uint
	if connection.OwnerUserID != nil {
		subject = *connection.OwnerUserID
	}
	detail := map[string]any{
		"connection_id": connection.ID,
		"provider":      string(connection.Provider),
	}
	if connection.TenantID != nil {
		detail["tenant_id"] = *connection.TenantID
	}
	for key, value := range extra {
		detail[key] = value
	}
	mailbox := &domain.Mailbox{ID: connection.MailboxID, OrganizationID: connection.OrganizationID}
	return s.recordAccess(ctx, mailbox, event, actorUserID, subject, detail)
}

// handBackExternalGrant revokes a connection's grant at its provider on a best-effort basis: a
// provider that is down or no longer enabled leaves the grant to expire there, and the token is
// erased here either way.
func (s *service) handBackExternalGrant(ctx context.Context, connection *domain.ExternalMailConnection) {
	if err := s.returnExternalGrant(ctx, connection); err != nil {
		// Logged by returnExternalGrant; the grant lapses at the provider.
		return
	}
}

// returnExternalGrant revokes the connection's sealed grant at its provider. It answers an error
// only when trying again may help: no grant, a grant this instance cannot open, or a connector the
// instance no longer runs end it.
func (s *service) returnExternalGrant(ctx context.Context, connection *domain.ExternalMailConnection) error {
	if connection.EncryptedRefreshToken == "" || s.encryptor == nil {
		return nil
	}
	token, err := s.encryptor.DecryptString(connection.EncryptedRefreshToken)
	if err != nil {
		return nil
	}
	connector, registered := s.externalConnectors.lookup(connection.Provider)
	if !registered {
		slog.WarnContext(ctx, "external_mail.provider_revoke_skipped",
			slog.Uint64("connection_id", uint64(connection.ID)), slog.String("provider", string(connection.Provider)))
		return nil
	}
	if err := connector.Source.Revoke(ctx, ExternalMailToken{RefreshToken: token}); err != nil {
		slog.WarnContext(ctx, "external_mail.provider_revoke_failed",
			slog.Uint64("connection_id", uint64(connection.ID)), slog.String("provider", string(connection.Provider)))
		return err
	}
	return nil
}
