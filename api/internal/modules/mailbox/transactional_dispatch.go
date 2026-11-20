package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
	"github.com/fastagi/zmail/api/internal/infra/metrics"
)

const (
	// failureSenderUnverified: the template's sender domain lost its verification while queued.
	failureSenderUnverified = "send.sender_unverified"
	// failurePayloadUnavailable: the queued payload could not be decrypted.
	failurePayloadUnavailable = "send.payload_unavailable"
	// providerSuppressedCode is the provider's refusal of a recipient on its own suppression list.
	providerSuppressedCode = "transport.suppressed"
	// simulatedDetail marks every event of a simulated or test-mode message.
	simulatedDetail = "simulated"

	bounceTypePermanent = "permanent"
	bounceTypeTemporary = "temporary"
)

// transactionalRetryDelays are the waits before the 2nd, 3rd, and 4th provider call.
var transactionalRetryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// transactionalRetryDelay is the wait after a temporary provider failure of attempt number
// attempts; false once the message has used its retries.
func transactionalRetryDelay(attempts int) (time.Duration, bool) {
	if attempts < 1 || attempts > len(transactionalRetryDelays) {
		return 0, false
	}
	return transactionalRetryDelays[attempts-1], true
}

// DispatchTransactionalMessage sends one queued message right after it was stored. It leases the
// message like the worker does, so a message is dispatched by one of them only.
func (s *service) DispatchTransactionalMessage(ctx context.Context, id uint) error {
	if s.transactional == nil {
		return nil
	}
	message, err := s.transactional.FindTransactionalByID(ctx, id)
	if errors.Is(err, ErrTransactionalMessageNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if message.Status != domain.TransactionalStatusQueued {
		return nil
	}
	_, err = s.dispatchTransactionalOrganization(ctx, message.OrganizationID, []uint{id}, s.now().UTC(), 1)
	return err
}

// DispatchTransactional runs one worker pass over transactional mail: it fails messages queued
// past TransactionalQueueTimeout and messages whose provider call outlived its lease, then sends
// due messages organization by organization, never more than limit in all. It keeps draining
// stored messages when the feature has been switched off.
func (s *service) DispatchTransactional(ctx context.Context, limit int) (int, error) {
	if s.transactional == nil || limit <= 0 {
		return 0, nil
	}
	now := s.now().UTC()
	processed := 0
	for _, step := range []struct {
		fail  func(ctx context.Context, now time.Time, limit int) ([]*domain.TransactionalMessage, error)
		event string
	}{
		{s.transactional.ExpireTransactional, "transactional_message.expired"},
		{s.transactional.FailUnknownTransactional, "transactional_message.unknown_outcome"},
	} {
		failed, err := s.failTransactionalBatch(ctx, step.fail, now, limit-processed, step.event)
		processed += failed
		if err != nil || processed >= limit {
			return processed, err
		}
	}
	organizations, err := s.transactional.TransactionalDueOrganizations(ctx, now, limit-processed)
	if err != nil {
		return processed, err
	}
	for _, organizationID := range organizations {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if processed >= limit {
			break
		}
		sent, err := s.dispatchTransactionalOrganization(ctx, organizationID, nil, now, limit-processed)
		processed += sent
		if err != nil {
			return processed, err
		}
	}
	s.reportTransactionalQueue(ctx)
	return processed, nil
}

// PurgeExpiredTransactional deletes up to limit messages created TransactionalRetention before now,
// oldest first; their events go with them.
func (s *service) PurgeExpiredTransactional(ctx context.Context, now time.Time, limit int) (int, error) {
	if s.transactional == nil || limit <= 0 {
		return 0, nil
	}
	purged, err := s.transactional.PurgeTransactional(ctx, now.Add(-domain.TransactionalRetention), limit)
	if purged > 0 {
		slog.InfoContext(ctx, "transactional_message.purged", slog.Int("count", purged))
	}
	return purged, err
}

// failTransactionalBatch fails a batch of queued messages and publishes email.failed for each in
// the same transaction.
func (s *service) failTransactionalBatch(
	ctx context.Context,
	fail func(ctx context.Context, now time.Time, limit int) ([]*domain.TransactionalMessage, error),
	now time.Time,
	limit int,
	event string,
) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	var failed []*domain.TransactionalMessage
	err := s.applyTransactional(ctx, func(tx context.Context) ([]TransactionalNotification, error) {
		var err error
		if failed, err = fail(tx, now, limit); err != nil {
			return nil, err
		}
		notifications := make([]TransactionalNotification, 0, len(failed))
		for _, message := range failed {
			notifications = append(notifications, TransactionalNotification{
				Type: WebhookEmailFailed, Message: message, Reason: message.FailureCode, OccurredAt: now,
			})
		}
		return notifications, nil
	})
	if err != nil {
		return 0, err
	}
	for _, message := range failed {
		logTransactional(ctx, event, message, slog.String("failure_code", message.FailureCode))
		metrics.RecordTransactionalMessage(string(message.Mode), string(message.Status))
	}
	return len(failed), nil
}

// reportTransactionalQueue publishes the age of the oldest live message still waiting to be sent.
func (s *service) reportTransactionalQueue(ctx context.Context) {
	oldest, err := s.transactional.OldestQueuedTransactional(ctx)
	if err != nil {
		return
	}
	age := 0.0
	if oldest != nil {
		age = max(s.now().Sub(*oldest).Seconds(), 0)
	}
	metrics.SetTransactionalQueueOldest(age)
}

// dispatchTransactionalOrganization leases up to limit due messages of one organization (only the
// given ones when ids is set) and dispatches each. Live, real messages lease only while the
// transactional lane has room.
//
// A worker pass passes the now it failed unknown outcomes with, so a lease that runs out between
// that check and this lease is left for the next pass's check instead of being leased again.
func (s *service) dispatchTransactionalOrganization(ctx context.Context, organizationID uint, ids []uint, now time.Time, limit int) (int, error) {
	leased, err := s.transactional.LeaseTransactional(ctx, organizationID, ids, now, now.Add(domain.TransactionalLease),
		now.Add(-sendLimitWindow), s.settings.laneLimit(domain.SendLaneTransactional), limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, message := range leased {
		// A message leased but not dispatched is taken again once its lease runs out.
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := s.dispatchLeased(ctx, message); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

// dispatchLeased re-checks a leased message and sends or simulates it (spec §12).
func (s *service) dispatchLeased(ctx context.Context, message *domain.TransactionalMessage) error {
	if message.LeaseUntil == nil {
		return nil
	}
	lease := *message.LeaseUntil
	if !message.Simulated {
		suppressed, err := s.transactional.IsSuppressed(ctx, message.OrganizationID, message.ToAddress)
		if err != nil {
			return err
		}
		if suppressed {
			return s.finishTransactional(ctx, message, lease, domain.TransactionalOutcome{Status: domain.TransactionalStatusSuppressed},
				nil, "suppressed")
		}
	}
	if err := s.requireTemplateSender(ctx, message.OrganizationID, message.FromAddress); err != nil {
		if !errors.Is(err, ErrMailDomainNotVerified) {
			return err
		}
		return s.finishTransactional(ctx, message, lease,
			domain.TransactionalOutcome{Status: domain.TransactionalStatusFailed, FailureCode: failureSenderUnverified}, nil, "")
	}
	payload, ok := s.decryptTransactional(message)
	if !ok {
		logTransactional(ctx, "transactional_payload.decrypt_failed", message)
		return s.finishTransactional(ctx, message, lease,
			domain.TransactionalOutcome{Status: domain.TransactionalStatusFailed, FailureCode: failurePayloadUnavailable}, nil, "")
	}
	if message.Simulated || message.Mode == domain.APIKeyModeTest {
		return s.finishSimulated(ctx, message, lease)
	}
	return s.sendTransactionalAttempt(ctx, message, lease, payload)
}

// decryptTransactional opens the queued payload; false when it is missing or does not open.
func (s *service) decryptTransactional(message *domain.TransactionalMessage) (transactionalPayload, bool) {
	var payload transactionalPayload
	if s.encryptor == nil || len(message.PayloadCiphertext) == 0 {
		return payload, false
	}
	plain, err := s.encryptor.Decrypt(message.PayloadCiphertext)
	if err != nil {
		return payload, false
	}
	if err := json.Unmarshal(plain, &payload); err != nil {
		return payload, false
	}
	return payload, true
}

// simulatedStep is one event of a simulated outcome and the webhook it publishes.
type simulatedStep struct {
	event      domain.MailEventType
	webhook    string
	bounceType string
}

// simulatedOutcomes are the simulator table of spec §4.2 by outcome name.
var simulatedOutcomes = map[string][]simulatedStep{
	"delivered": {
		{domain.MailEventAccepted, WebhookEmailSent, ""},
		{domain.MailEventDelivered, WebhookEmailDelivered, ""},
	},
	"bounced": {
		{domain.MailEventAccepted, WebhookEmailSent, ""},
		{domain.MailEventBounced, WebhookEmailBounced, bounceTypePermanent},
	},
	"soft-bounced": {
		{domain.MailEventAccepted, WebhookEmailSent, ""},
		{domain.MailEventDeferred, WebhookEmailBounced, bounceTypeTemporary},
	},
	"complained": {
		{domain.MailEventAccepted, WebhookEmailSent, ""},
		{domain.MailEventDelivered, WebhookEmailDelivered, ""},
		{domain.MailEventComplained, WebhookEmailComplained, ""},
	},
	"suppressed": {
		{domain.MailEventRejected, WebhookEmailSuppressed, ""},
	},
}

// finishSimulated ends a test-mode or simulated message as its simulator address says, without
// calling the provider and without touching the suppression list. A test key sending to a normal
// address simulates delivered.
func (s *service) finishSimulated(ctx context.Context, message *domain.TransactionalMessage, lease time.Time) error {
	name, _, _ := simulatorOutcome(message.ToAddress, s.settings.simulatorDomain())
	steps, known := simulatedOutcomes[name]
	if !known {
		name, steps = "delivered", simulatedOutcomes["delivered"]
	}
	now := s.now().UTC()
	outcome := domain.TransactionalOutcome{Status: domain.TransactionalStatusSent, SentAt: &now}
	if name == "suppressed" {
		outcome = domain.TransactionalOutcome{Status: domain.TransactionalStatusSuppressed}
	}
	events := make([]domain.MailEvent, 0, len(steps))
	notifications := make([]TransactionalNotification, 0, len(steps))
	for i, step := range steps {
		events = append(events, domain.MailEvent{Type: step.event, Detail: simulatedDetail, OccurredAt: now})
		reason := simulatedDetail
		switch step.webhook {
		case WebhookEmailSent:
			reason = ""
		case WebhookEmailSuppressed:
			reason = "suppressed"
		}
		notifications = append(notifications, TransactionalNotification{
			Type: step.webhook, BounceType: step.bounceType, Reason: reason,
			Key: "sim:" + strconv.Itoa(i), OccurredAt: now,
		})
	}
	return s.recordTransactional(ctx, message, lease, outcome, events, notifications)
}

// sendTransactionalAttempt starts an attempt, calls the provider outside any transaction, and
// records what it answered. Only a temporary failure the provider answered with an HTTP status is
// retried; any failure that leaves the outcome unknown fails for good (D33).
func (s *service) sendTransactionalAttempt(ctx context.Context, message *domain.TransactionalMessage, lease time.Time, payload transactionalPayload) error {
	attemptedAt := s.now().UTC()
	started, err := s.transactional.StartTransactionalAttempt(ctx, message.ID, lease, attemptedAt)
	if err != nil || !started {
		return err
	}
	message.Attempts++
	metrics.RecordSendLaneAttempt(string(domain.SendLaneTransactional))
	callStarted := time.Now()
	result, sendErr := s.transport.Send(ctx, mailtransport.Message{
		FromAddress: message.FromAddress,
		FromName:    message.FromName,
		To:          []string{message.ToAddress},
		Subject:     payload.Subject,
		TextBody:    payload.Text,
		HTMLBody:    payload.HTML,
		MessageID:   message.RFCMessageID,
	})
	logTransactional(ctx, "transactional_message.dispatched", message,
		slog.Int("attempts", message.Attempts), slog.Int64("duration_ms", time.Since(callStarted).Milliseconds()))

	// Record the outcome even when the caller has gone away: a sent message must not look queued.
	recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryRecordTimeout)
	defer cancel()
	now := s.now().UTC()
	if sendErr == nil {
		metrics.ObserveTransactionalDispatch(now.Sub(message.CreatedAt))
		events := sendEvents(result, true, now)
		return s.recordTransactional(recordContext, message, lease, domain.TransactionalOutcome{
			Status: domain.TransactionalStatusSent, ProviderMessageID: result.MessageID, SentAt: &now,
		}, events, providerNotifications(events, now))
	}
	var transportErr *mailtransport.Error
	isTransportErr := errors.As(sendErr, &transportErr)
	switch {
	case isTransportErr && transportErr.Code == providerSuppressedCode:
		// The provider's own list; Zmail's suppression list is not written (D35).
		events := sendEvents(result, false, now)
		return s.recordTransactional(recordContext, message, lease,
			domain.TransactionalOutcome{Status: domain.TransactionalStatusSuppressed, ProviderMessageID: result.MessageID},
			events, []TransactionalNotification{{Type: WebhookEmailSuppressed, Reason: "suppressed", OccurredAt: now}})
	case isTransportErr && !transportErr.Permanent && transportErr.Responded:
		if delay, retry := transactionalRetryDelay(message.Attempts); retry {
			scheduled, err := s.transactional.ScheduleTransactionalRetry(recordContext, message.ID, lease, now.Add(delay), transportErr.Code)
			if err != nil {
				return err
			}
			if !scheduled {
				logTransactionalLeaseLost(ctx, message)
				return nil
			}
			logTransactional(ctx, "transactional_message.retry_scheduled", message,
				slog.Int("attempts", message.Attempts), slog.String("failure_code", transportErr.Code))
			return nil
		}
		return s.failTransactional(recordContext, message, lease, transportErr.Code, result, now)
	case isTransportErr && transportErr.Permanent:
		return s.failTransactional(recordContext, message, lease, transportErr.Code, result, now)
	case errors.Is(sendErr, mailtransport.ErrNotConfigured):
		// No transport: the provider was never called, so the outcome is known, not unknown.
		return s.failTransactional(recordContext, message, lease, domain.TransactionalFailureNotConfigured, result, now)
	default:
		// No HTTP status came back: the provider may have accepted it, so it is never resent.
		logTransactional(ctx, "transactional_message.unknown_outcome", message, slog.Int("attempts", message.Attempts))
		return s.failTransactional(recordContext, message, lease, domain.TransactionalFailureUnknownOutcome, result, now)
	}
}

// failTransactional records a provider failure with what the provider said about the recipient.
func (s *service) failTransactional(ctx context.Context, message *domain.TransactionalMessage, lease time.Time, code string, result mailtransport.Result, now time.Time) error {
	events := sendEvents(result, false, now)
	return s.recordTransactional(ctx, message, lease,
		domain.TransactionalOutcome{Status: domain.TransactionalStatusFailed, FailureCode: code, ProviderMessageID: result.MessageID},
		events, []TransactionalNotification{{Type: WebhookEmailFailed, Reason: code, OccurredAt: now}})
}

// providerNotifications are the webhooks of an accepted message: email.sent, then one per
// recipient event the provider reported during the call.
func providerNotifications(events []domain.MailEvent, at time.Time) []TransactionalNotification {
	notifications := []TransactionalNotification{{Type: WebhookEmailSent, OccurredAt: at}}
	for i, event := range events {
		notification := TransactionalNotification{Reason: event.Detail, Key: "send:" + strconv.Itoa(i), OccurredAt: at}
		switch event.Type {
		case domain.MailEventDelivered:
			notification.Type = WebhookEmailDelivered
		case domain.MailEventBounced:
			notification.Type, notification.BounceType = WebhookEmailBounced, bounceTypePermanent
		case domain.MailEventRejected:
			notification.Type = WebhookEmailSuppressed
		default:
			continue
		}
		notifications = append(notifications, notification)
	}
	return notifications
}

// finishTransactional ends a message before any provider call and publishes its webhook.
func (s *service) finishTransactional(
	ctx context.Context,
	message *domain.TransactionalMessage,
	lease time.Time,
	outcome domain.TransactionalOutcome,
	events []domain.MailEvent,
	reason string,
) error {
	webhook := WebhookEmailFailed
	if outcome.Status == domain.TransactionalStatusSuppressed {
		webhook = WebhookEmailSuppressed
	} else {
		reason = outcome.FailureCode
	}
	return s.recordTransactional(ctx, message, lease, outcome, events, []TransactionalNotification{{
		Type: webhook, Reason: reason, OccurredAt: s.now().UTC(),
	}})
}

// recordTransactional stores the final outcome, its events, and its webhooks in one transaction,
// only while the caller still holds the lease. The ciphertext is wiped with the outcome.
func (s *service) recordTransactional(
	ctx context.Context,
	message *domain.TransactionalMessage,
	lease time.Time,
	outcome domain.TransactionalOutcome,
	events []domain.MailEvent,
	notifications []TransactionalNotification,
) error {
	finished := false
	err := s.applyTransactional(ctx, func(tx context.Context) ([]TransactionalNotification, error) {
		var err error
		if finished, err = s.transactional.FinishTransactional(tx, message.ID, lease, outcome); err != nil || !finished {
			return nil, err
		}
		final := *message
		final.Status, final.FailureCode = outcome.Status, outcome.FailureCode
		final.PayloadCiphertext, final.LeaseUntil, final.QueuedReason, final.NextAttemptAt = nil, nil, nil, nil
		if len(events) > 0 {
			if _, final.LastEvent, err = s.transactional.AddTransactionalEvents(tx, message.ID, events); err != nil {
				return nil, err
			}
		}
		published := make([]TransactionalNotification, 0, len(notifications))
		for _, notification := range notifications {
			notification.Message = &final
			published = append(published, notification)
		}
		return published, nil
	})
	if err != nil {
		return err
	}
	if !finished {
		// The lease ran out before the outcome was recorded; whoever holds the message now decides.
		logTransactionalLeaseLost(ctx, message)
		return nil
	}
	message.Status = outcome.Status
	event := "transactional_message." + string(outcome.Status)
	if outcome.FailureCode != "" {
		logTransactional(ctx, event, message, slog.String("failure_code", outcome.FailureCode))
	} else {
		logTransactional(ctx, event, message)
	}
	metrics.RecordTransactionalMessage(string(message.Mode), string(outcome.Status))
	return nil
}

// logTransactionalLeaseLost reports a dispatcher that lost its lease before recording what it did.
func logTransactionalLeaseLost(ctx context.Context, message *domain.TransactionalMessage) {
	slog.WarnContext(ctx, "transactional_message.lease_lost",
		slog.Uint64("organization_id", uint64(message.OrganizationID)),
		slog.Uint64("message_id", uint64(message.ID)))
}

// logTransactional writes one structured event with the message's identifiers only: never an
// address, a subject, a variable, or the payload.
func logTransactional(ctx context.Context, event string, message *domain.TransactionalMessage, extra ...slog.Attr) {
	var templateID uint64
	if message.TemplateID != nil {
		templateID = uint64(*message.TemplateID)
	}
	attributes := []any{
		slog.Uint64("organization_id", uint64(message.OrganizationID)),
		slog.Uint64("message_id", uint64(message.ID)),
		slog.Uint64("template_id", templateID),
		slog.String("mode", string(message.Mode)),
		slog.String("status", string(message.Status)),
	}
	for _, attribute := range extra {
		attributes = append(attributes, attribute)
	}
	slog.InfoContext(ctx, event, attributes...)
}
