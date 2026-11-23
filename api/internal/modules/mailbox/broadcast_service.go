package mailbox

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	maxBroadcastNameLength    = 100
	maxUnsubscribeLabelLength = 50
	defaultUnsubscribeLabel   = "Unsubscribe"
	maxBroadcastSchedule      = 365 * 24 * time.Hour
	broadcastLease            = 5 * time.Minute
	unsubscribeVariable       = "unsubscribe_url"
	stopReasonSendLimit       = "send_limit_reached"
	stopReasonTestWave        = "test_wave_complete"
	stopReasonBounceRate      = "bounce_rate_high"
	stopReasonComplaintRate   = "complaint_rate_high"
	stopReasonOwner           = "paused_by_owner"
	stopReasonAllFailed       = "all_recipients_failed"
	maxTestPercent            = 50
	defaultTestWaitMinutes    = 30
	minTestWaitMinutes        = 5
	maxTestWaitMinutes        = 24 * 60
	// The health check needs this many finished recipients before a rate means anything.
	minHealthSample = 20
	// A broadcast pauses at 5% bounces or 0.3% spam complaints, Gmail's limit for bulk senders.
	maxBouncePerMille            = 50
	maxComplaintPerMille         = 3
	maxActiveBroadcasts          = 100
	recipientSkippedUnsubscribed = "contact_not_subscribed"
	recipientSkippedDeleted      = "contact_deleted"
	recipientSkippedSuppressed   = "contact_suppressed" // on the organization's suppression list
	recipientFailedVariables     = "template.variables_missing"
	recipientFailedMailbox       = "mailbox_not_found"
	recipientFailedMessage       = "message_invalid"
	unsubscribeReasonLink        = "unsubscribe_link"
)

// BroadcastInput is the editable content of a draft broadcast.
type BroadcastInput struct {
	Name             string
	MailboxID        uint
	TemplateID       uint
	ListID           uint
	UnsubscribeLabel string
	// TestPercent sends to this share of the list first; 0 sends to everyone at once.
	TestPercent int
	// TestWaitMinutes is the wait after the test wave; 0 means the default of 30 minutes.
	TestWaitMinutes int
	// Variables are template values shared by every recipient.
	Variables map[string]string
}

// broadcastContactVariables are filled from each contact and cannot be set on a broadcast.
var broadcastContactVariables = []string{"email", "first_name", "last_name", unsubscribeVariable}

// BroadcastsEnabled reports whether broadcasts can run: unsubscribe links need the signing
// secret and the public Web origin.
func (s *service) BroadcastsEnabled() bool {
	return s.settings.InboundSecret != "" && s.settings.PublicURL != ""
}

func (s *service) ListBroadcasts(ctx context.Context, viewer domain.OrganizationContext, page, perPage int) ([]*domain.MailBroadcast, int64, error) {
	readable, err := s.readableMailboxIDs(ctx, viewer)
	if err != nil {
		return nil, 0, err
	}
	return s.broadcasts.ListBroadcasts(ctx, viewer.OrganizationID, readable, page, perPage)
}

// authorizeBroadcast loads a broadcast and checks the viewer's access to its sender mailbox. A
// viewer without any access to that mailbox, or a mailbox that is gone, finds no broadcast; some
// access but not the needed one is forbidden.
func (s *service) authorizeBroadcast(
	ctx context.Context,
	viewer domain.OrganizationContext,
	id uint,
	need func(domain.MailboxAccess) bool,
) (*domain.MailBroadcast, error) {
	broadcast, err := s.broadcasts.FindBroadcast(ctx, viewer.OrganizationID, id)
	if err != nil {
		return nil, err
	}
	mailbox, err := s.repo.FindMailbox(ctx, viewer.OrganizationID, broadcast.MailboxID)
	if errors.Is(err, ErrMailboxNotFound) {
		return nil, ErrMailBroadcastNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, viewer, mailbox, need, ErrMailBroadcastNotFound); err != nil {
		return nil, err
	}
	// A broadcast created before the mailbox's seal boundary is sealed with its mail.
	if sealedFor(mailbox, broadcast.CreatedAt) {
		return nil, ErrMailBroadcastNotFound
	}
	return broadcast, nil
}

func (s *service) GetBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, error) {
	return s.authorizeBroadcast(ctx, viewer, id, canView)
}

// CreateBroadcast needs Send on the sender mailbox.
func (s *service) CreateBroadcast(ctx context.Context, viewer domain.OrganizationContext, input BroadcastInput) (*domain.MailBroadcast, error) {
	broadcast, err := s.buildBroadcast(ctx, viewer, input)
	if err != nil {
		return nil, err
	}
	broadcast.CreatedBy = viewer.UserID
	if err := s.broadcasts.CreateBroadcast(ctx, broadcast); err != nil {
		return nil, err
	}
	return broadcast, nil
}

// UpdateBroadcast needs Send on the stored sender mailbox and on the new one.
func (s *service) UpdateBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint, input BroadcastInput) (*domain.MailBroadcast, error) {
	if _, err := s.authorizeBroadcast(ctx, viewer, id, canSend); err != nil {
		return nil, err
	}
	broadcast, err := s.buildBroadcast(ctx, viewer, input)
	if err != nil {
		return nil, err
	}
	broadcast.ID = id
	if err := s.broadcasts.UpdateDraft(ctx, broadcast); err != nil {
		return nil, err
	}
	return broadcast, nil
}

// DeleteBroadcast needs Send on the stored sender mailbox.
func (s *service) DeleteBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	if _, err := s.authorizeBroadcast(ctx, viewer, id, canSend); err != nil {
		return err
	}
	return s.broadcasts.DeleteBroadcast(ctx, viewer.OrganizationID, id)
}

// buildBroadcast validates the fields, confirms the template and list belong to the organization,
// and requires Send on the sender mailbox.
func (s *service) buildBroadcast(ctx context.Context, viewer domain.OrganizationContext, input BroadcastInput) (*domain.MailBroadcast, error) {
	organizationID := viewer.OrganizationID
	name := strings.Join(strings.Fields(cleanText(input.Name)), " ")
	if name == "" || utf8.RuneCountInString(name) > maxBroadcastNameLength || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("%w: name", ErrMailBroadcastInvalid)
	}
	label := strings.Join(strings.Fields(cleanText(input.UnsubscribeLabel)), " ")
	if utf8.RuneCountInString(label) > maxUnsubscribeLabelLength || strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("%w: unsubscribe_label", ErrMailBroadcastInvalid)
	}
	if input.TestPercent < 0 || input.TestPercent > maxTestPercent {
		return nil, fmt.Errorf("%w: test_percent must be between 0 and %d", ErrMailBroadcastInvalid, maxTestPercent)
	}
	wait := input.TestWaitMinutes
	if wait == 0 {
		wait = defaultTestWaitMinutes
	}
	if wait < minTestWaitMinutes || wait > maxTestWaitMinutes {
		return nil, fmt.Errorf("%w: test_wait_minutes must be between %d and %d", ErrMailBroadcastInvalid, minTestWaitMinutes, maxTestWaitMinutes)
	}
	variables, ok := cleanTemplateValues(input.Variables)
	if !ok {
		return nil, fmt.Errorf("%w: variables", ErrMailBroadcastInvalid)
	}
	for _, name := range broadcastContactVariables {
		if _, reserved := variables[name]; reserved {
			return nil, fmt.Errorf("%w: variables cannot set %s, which comes from each contact", ErrMailBroadcastInvalid, name)
		}
	}
	if input.MailboxID == 0 || input.TemplateID == 0 || input.ListID == 0 {
		return nil, fmt.Errorf("%w: mailbox_id, template_id, and list_id are required", ErrMailBroadcastInvalid)
	}
	if _, err := s.authorizeMailbox(ctx, viewer, input.MailboxID, canSend); err != nil {
		return nil, err
	}
	template, err := s.templates.FindTemplate(ctx, organizationID, input.TemplateID)
	if err != nil {
		return nil, err
	}
	if err = requireStandardTemplate(template); err != nil {
		return nil, err
	}
	lists, err := s.contacts.ListContactLists(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, list := range lists {
		found = found || list.ID == input.ListID
	}
	if !found {
		return nil, ErrMailContactListNotFound
	}
	return &domain.MailBroadcast{
		OrganizationID:   organizationID,
		Name:             name,
		MailboxID:        input.MailboxID,
		TemplateID:       input.TemplateID,
		ListID:           input.ListID,
		UnsubscribeLabel: label,
		Variables:        variables,
		TestPercent:      input.TestPercent,
		TestWaitMinutes:  wait,
	}, nil
}

// SendBroadcast fixes the recipients and the template of a draft and hands it to the worker,
// now or at scheduledAt. The viewer needs Send on the sender mailbox at this call; the worker
// does not check it again.
func (s *service) SendBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint, scheduledAt *time.Time) (*domain.MailBroadcast, error) {
	if !s.BroadcastsEnabled() {
		return nil, ErrMailBroadcastDisabled
	}
	organizationID := viewer.OrganizationID
	broadcast, err := s.authorizeBroadcast(ctx, viewer, id, canSend)
	if err != nil {
		return nil, err
	}
	if broadcast.Status != domain.MailBroadcastDraft {
		return nil, ErrMailBroadcastNotEditable
	}
	template, err := s.templates.FindTemplate(ctx, organizationID, broadcast.TemplateID)
	if err != nil {
		return nil, err
	}
	if err = requireStandardTemplate(template); err != nil {
		return nil, err
	}
	parsed, err := parseStoredTemplate(template)
	if err != nil {
		return nil, err
	}
	if err = s.checkBroadcastVariables(ctx, broadcast, parsed); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	broadcast.StartedBy = viewer.UserID
	broadcast.Subject, broadcast.HTMLBody, broadcast.TextBody = template.Subject, template.HTMLBody, template.TextBody
	switch {
	case scheduledAt == nil || !scheduledAt.After(now):
		broadcast.Status, broadcast.StartedAt, broadcast.ScheduledAt = domain.MailBroadcastSending, &now, nil
	case scheduledAt.Sub(now) > maxBroadcastSchedule:
		return nil, fmt.Errorf("%w: scheduled_at is more than a year ahead", ErrMailBroadcastInvalid)
	default:
		at := scheduledAt.UTC()
		broadcast.Status, broadcast.ScheduledAt = domain.MailBroadcastScheduled, &at
	}
	if _, err := s.broadcasts.StartBroadcast(ctx, broadcast); err != nil {
		return nil, err
	}
	return s.broadcasts.FindBroadcast(ctx, organizationID, id)
}

// checkBroadcastVariables refuses a template variable without a fallback that no recipient can
// fill: it is not a shared value, a contact field, or a property of any subscribed contact of
// the list, so it would fail every recipient. A variable some contacts lack still fails only them.
func (s *service) checkBroadcastVariables(ctx context.Context, broadcast *domain.MailBroadcast, parsed *parsedTemplate) error {
	properties, err := s.broadcasts.ListPropertyNames(ctx, broadcast.OrganizationID, broadcast.ListID)
	if err != nil {
		return err
	}
	// Any non-empty value stands for a value some recipient will have.
	values := make(map[string]string, len(broadcast.Variables)+len(properties)+len(broadcastContactVariables))
	for name, value := range broadcast.Variables {
		values[name] = value
	}
	for _, name := range append(properties, broadcastContactVariables...) {
		values[name] = name
	}
	if missing := parsed.render(values).Missing; len(missing) > 0 {
		return &MissingVariablesError{Names: missing}
	}
	return nil
}

// CancelBroadcast stops a broadcast for good. Stopping mail needs no access to the sender
// mailbox, so an owner or admin can always stop a broadcast; without View it only learns the new
// status. A member with the broadcast grant stops only broadcasts it could send.
func (s *service) CancelBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, bool, error) {
	if err := s.authorizeStop(ctx, viewer, id); err != nil {
		return nil, false, err
	}
	if err := s.broadcasts.CancelBroadcast(ctx, viewer.OrganizationID, id); err != nil {
		return nil, false, err
	}
	return s.stoppedBroadcast(ctx, viewer, id)
}

// PauseBroadcast stops a sending broadcast until an owner resumes it. Pending recipients stay
// pending; a message the worker is sending right now still finishes. Like cancel, it needs no
// access to the sender mailbox for an owner or admin.
func (s *service) PauseBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, bool, error) {
	if err := s.authorizeStop(ctx, viewer, id); err != nil {
		return nil, false, err
	}
	if err := s.broadcasts.PauseBroadcast(ctx, viewer.OrganizationID, id, stopReasonOwner); err != nil {
		return nil, false, err
	}
	return s.stoppedBroadcast(ctx, viewer, id)
}

// authorizeStop lets owners and admins stop any broadcast and a member only one it could send.
func (s *service) authorizeStop(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	if viewer.Role.CanManageOrganization() {
		return nil
	}
	_, err := s.authorizeBroadcast(ctx, viewer, id, canSend)
	return err
}

// stoppedBroadcast reloads a broadcast after a stop and reports whether the viewer may read its
// sender mailbox.
func (s *service) stoppedBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, bool, error) {
	broadcast, err := s.broadcasts.FindBroadcast(ctx, viewer.OrganizationID, id)
	if err != nil {
		return nil, false, err
	}
	mailbox, err := s.repo.FindMailbox(ctx, viewer.OrganizationID, broadcast.MailboxID)
	if errors.Is(err, ErrMailboxNotFound) {
		return broadcast, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	access, err := s.accessFor(ctx, viewer, mailbox)
	if err != nil {
		return nil, false, err
	}
	return broadcast, access.View && !sealedFor(mailbox, broadcast.CreatedAt), nil
}

// ResumeBroadcast continues a paused broadcast; it needs Send on the sender mailbox at this call.
// Resuming one that the health check paused is an explicit decision: it is not paused
// automatically again. Resuming a test wave paused by hand during its wait returns it to that wait.
func (s *service) ResumeBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, error) {
	organizationID := viewer.OrganizationID
	broadcast, err := s.authorizeBroadcast(ctx, viewer, id, canSend)
	if err != nil {
		return nil, err
	}
	if broadcast.Status != domain.MailBroadcastPaused {
		return nil, ErrMailBroadcastNotEditable
	}
	override := broadcast.StopReason == stopReasonBounceRate || broadcast.StopReason == stopReasonComplaintRate
	if err := s.broadcasts.ResumeBroadcast(ctx, organizationID, id, override, s.now().UTC()); err != nil {
		return nil, err
	}
	return s.broadcasts.FindBroadcast(ctx, organizationID, id)
}

// healthStop returns why a broadcast must pause, or "" while its rates are acceptable.
func healthStop(counts domain.MailBroadcastCounts) string {
	finished := counts.Sent + counts.Failed
	if finished < minHealthSample {
		return ""
	}
	switch {
	case counts.Bounced*1000 >= finished*maxBouncePerMille:
		return stopReasonBounceRate
	case counts.Complained*1000 >= finished*maxComplaintPerMille:
		return stopReasonComplaintRate
	}
	return ""
}

// checkBroadcastHealth pauses broadcasts whose bounce or complaint rate is too high, and lets a
// waiting test wave continue once its wait is over and its rates are acceptable.
func (s *service) checkBroadcastHealth(ctx context.Context, now time.Time) error {
	active, err := s.broadcasts.ListActiveBroadcasts(ctx, maxActiveBroadcasts)
	if err != nil {
		return err
	}
	for _, broadcast := range active {
		if !broadcast.SafetyOverride {
			if reason := healthStop(broadcast.Counts); reason != "" {
				slog.WarnContext(ctx, "mailbox broadcast paused by its health check",
					slog.Uint64("broadcast_id", uint64(broadcast.ID)), slog.String("reason", reason),
					slog.Int("bounced", broadcast.Counts.Bounced), slog.Int("complained", broadcast.Counts.Complained),
					slog.Int("finished", broadcast.Counts.Sent+broadcast.Counts.Failed))
				if err := s.broadcasts.PauseBroadcast(ctx, 0, broadcast.ID, reason); err != nil {
					return err
				}
				continue
			}
		}
		if broadcast.Status == domain.MailBroadcastPaused && broadcast.StopReason == stopReasonTestWave &&
			broadcast.ResumeAt != nil && !broadcast.ResumeAt.After(now) {
			if err := s.broadcasts.ResumeBroadcast(ctx, 0, broadcast.ID, false, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *service) ListBroadcastRecipients(
	ctx context.Context,
	viewer domain.OrganizationContext,
	id uint,
	status domain.MailRecipientStatus,
	page, perPage int,
) ([]*domain.MailBroadcastRecipient, int64, error) {
	switch status {
	case "", domain.MailRecipientPending, domain.MailRecipientSent, domain.MailRecipientFailed,
		domain.MailRecipientSkipped, domain.MailRecipientCanceled:
	default:
		return nil, 0, domain.ErrInvalidInput
	}
	if _, err := s.authorizeBroadcast(ctx, viewer, id, canView); err != nil {
		return nil, 0, err
	}
	return s.broadcasts.ListRecipients(ctx, viewer.OrganizationID, id, status, page, perPage)
}

// DispatchBroadcasts sends up to limit due recipients and returns how many it finished. A
// recipient is sent with an idempotency key of its broadcast and contact, so a worker that stops
// after the provider call never mails the same person twice.
func (s *service) DispatchBroadcasts(ctx context.Context, limit int) (int, error) {
	now := s.now().UTC()
	if err := s.broadcasts.PromoteScheduled(ctx, now); err != nil {
		return 0, err
	}
	if err := s.checkBroadcastHealth(ctx, now); err != nil {
		return 0, err
	}
	recipients, err := s.broadcasts.LeasePending(ctx, now, now.Add(broadcastLease), limit)
	if err != nil {
		return 0, err
	}
	run := &broadcastRun{service: s, broadcasts: map[uint]*preparedBroadcast{}, paused: map[uint]bool{}}
	processed := 0
	for _, recipient := range recipients {
		if ctx.Err() != nil {
			break
		}
		finished, err := run.send(ctx, recipient)
		if err != nil {
			return processed, err
		}
		if finished {
			processed++
		}
	}
	for id, prepared := range run.broadcasts {
		// Only a broadcast that is still sending had a stop reason that no longer applies.
		if !run.paused[id] && prepared.broadcast != nil && prepared.broadcast.Status == domain.MailBroadcastSending &&
			prepared.broadcast.StopReason != "" {
			if err := s.broadcasts.SetStopReason(ctx, id, ""); err != nil {
				return processed, err
			}
		}
	}
	if err := s.broadcasts.CompleteTestWaves(ctx, s.now().UTC()); err != nil {
		return processed, err
	}
	if _, err := s.broadcasts.FinishBroadcasts(ctx, s.now().UTC(), stopReasonAllFailed); err != nil {
		return processed, err
	}
	// Telling people is never a reason to stop sending.
	if err := s.notifyBroadcastOutcomes(ctx); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "mailbox broadcast notices skipped this round", slog.String("error", err.Error()))
	}
	return processed, nil
}

// preparedBroadcast is what one worker batch needs to send a broadcast's recipients.
type preparedBroadcast struct {
	broadcast *domain.MailBroadcast
	mailbox   *domain.Mailbox
	parsed    *parsedTemplate
	// linked tells whether the template places the unsubscribe link itself.
	linked bool
	err    error
}

type broadcastRun struct {
	service    *service
	broadcasts map[uint]*preparedBroadcast
	paused     map[uint]bool
}

func (run *broadcastRun) prepare(ctx context.Context, id uint) *preparedBroadcast {
	if prepared, ok := run.broadcasts[id]; ok {
		return prepared
	}
	prepared := &preparedBroadcast{}
	run.broadcasts[id] = prepared
	broadcast, err := run.service.broadcasts.FindBroadcastForWorker(ctx, id)
	if err != nil {
		prepared.err = err
		return prepared
	}
	prepared.broadcast = broadcast
	prepared.parsed, prepared.err = parseStoredTemplate(&domain.MailTemplate{
		Subject: broadcast.Subject, HTMLBody: broadcast.HTMLBody, TextBody: broadcast.TextBody,
	})
	if prepared.err != nil {
		return prepared
	}
	for _, name := range prepared.parsed.variables() {
		prepared.linked = prepared.linked || name == unsubscribeVariable
	}
	prepared.mailbox, err = run.service.repo.FindMailbox(ctx, broadcast.OrganizationID, broadcast.MailboxID)
	if err != nil && !errors.Is(err, ErrMailboxNotFound) {
		prepared.err = err
	}
	return prepared
}

// send finishes one recipient. It reports false when the recipient goes back to pending.
func (run *broadcastRun) send(ctx context.Context, recipient *domain.MailBroadcastRecipient) (bool, error) {
	s := run.service
	prepared := run.prepare(ctx, recipient.BroadcastID)
	if prepared.err != nil {
		if errors.Is(prepared.err, ErrMailBroadcastNotFound) {
			return false, nil
		}
		return false, prepared.err
	}
	broadcast := prepared.broadcast
	if broadcast.Status != domain.MailBroadcastSending || run.paused[broadcast.ID] {
		return false, s.broadcasts.ReleaseRecipient(ctx, recipient.ID)
	}
	finish := func(status domain.MailRecipientStatus, messageID uint, code string) (bool, error) {
		return true, s.broadcasts.FinishRecipient(ctx, recipient.ID, status, messageID, code)
	}
	if prepared.mailbox == nil {
		return finish(domain.MailRecipientFailed, 0, recipientFailedMailbox)
	}
	contact, err := s.broadcasts.FindContactForBroadcast(ctx, broadcast.OrganizationID, recipient.ContactID)
	if errors.Is(err, ErrMailContactNotFound) {
		return finish(domain.MailRecipientSkipped, 0, recipientSkippedDeleted)
	}
	if err != nil {
		return false, err
	}
	// The status at the moment of sending wins: an unsubscribe after the broadcast started counts.
	if contact.Status != domain.MailContactSubscribed {
		return finish(domain.MailRecipientSkipped, 0, recipientSkippedUnsubscribed)
	}
	// The organization's suppression list is shared with transactional mail; a hit skips the
	// recipient without touching the contact (D35).
	if s.transactional != nil {
		suppressed, lookupErr := s.transactional.IsSuppressed(ctx, broadcast.OrganizationID, normalizeEmail(contact.Email))
		if lookupErr != nil {
			return false, lookupErr
		}
		if suppressed {
			return finish(domain.MailRecipientSkipped, 0, recipientSkippedSuppressed)
		}
	}

	token := s.unsubscribeToken(broadcast.OrganizationID, contact.ID)
	unsubscribeURL := s.settings.PublicURL + "/unsubscribe/" + token
	// Shared values first; the contact's own values override them.
	values := make(map[string]string, len(broadcast.Variables)+len(contact.Properties)+4)
	for name, value := range broadcast.Variables {
		values[name] = value
	}
	for name, value := range contact.Properties {
		values[name] = value
	}
	values["email"], values["first_name"], values["last_name"] = contact.Email, contact.FirstName, contact.LastName
	values[unsubscribeVariable] = unsubscribeURL
	rendered := prepared.parsed.render(values)
	if len(rendered.Missing) > 0 {
		return finish(domain.MailRecipientFailed, 0, recipientFailedVariables)
	}
	if !prepared.linked {
		rendered.HTML, rendered.Text = withUnsubscribeFooter(rendered.HTML, rendered.Text, unsubscribeURL, broadcast.UnsubscribeLabel)
	}

	message, _, err := s.send(ctx, prepared.mailbox, SendInput{
		To:             []string{contact.Email},
		Subject:        rendered.Subject,
		HTML:           rendered.HTML,
		Text:           rendered.Text,
		IdempotencyKey: "broadcast-" + strconv.FormatUint(uint64(broadcast.ID), 10) + "-" + strconv.FormatUint(uint64(contact.ID), 10),
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + s.settings.PublicURL + "/api/mail-unsubscribes/" + token + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}, false)
	switch {
	case errors.Is(err, ErrMailSendLimitReached):
		run.paused[broadcast.ID] = true
		if stopErr := s.broadcasts.SetStopReason(ctx, broadcast.ID, stopReasonSendLimit); stopErr != nil {
			return false, stopErr
		}
		return false, s.broadcasts.ReleaseRecipient(ctx, recipient.ID)
	case errors.Is(err, ErrMailSendInProgress):
		return false, s.broadcasts.ReleaseRecipient(ctx, recipient.ID)
	case message == nil:
		slog.WarnContext(ctx, "mailbox broadcast recipient not sent",
			slog.Uint64("broadcast_id", uint64(broadcast.ID)), slog.String("error", err.Error()))
		return finish(domain.MailRecipientFailed, 0, recipientFailedMessage)
	}
	status, code := domain.MailRecipientFailed, message.FailureCode
	if message.Status == domain.MailStatusSent {
		status, code = domain.MailRecipientSent, ""
	}
	if _, err := finish(status, message.ID, code); err != nil {
		return false, err
	}
	// Suppression finds the contact through the recipient's message, so it follows the finish.
	if reason := refusalReason(message); reason != "" {
		if err := s.broadcasts.SuppressContactForMessage(ctx, message.ID, reason); err != nil {
			return false, err
		}
	}
	return true, nil
}

// refusalReason says why a message's recipient must not be mailed again: a permanent bounce or
// a suppression reported at send time.
func refusalReason(message *domain.MailMessage) string {
	for _, event := range message.Events {
		switch event.Type {
		case domain.MailEventBounced:
			return "bounced"
		case domain.MailEventRejected:
			return "rejected"
		case domain.MailEventAccepted, domain.MailEventDelivered, domain.MailEventDeferred,
			domain.MailEventFailed, domain.MailEventComplained:
		}
	}
	return ""
}

// withUnsubscribeFooter adds the unsubscribe link to bodies whose template does not place it.
func withUnsubscribeFooter(htmlBody, textBody, link, label string) (string, string) {
	if label == "" {
		label = defaultUnsubscribeLabel
	}
	if htmlBody != "" {
		footer := `<p style="margin:24px 0 0;font-size:12px;color:#71717a;text-align:center;"><a href="` +
			html.EscapeString(link) + `" style="color:#71717a;">` + html.EscapeString(label) + `</a></p>`
		if index := strings.LastIndex(strings.ToLower(htmlBody), "</body>"); index >= 0 {
			htmlBody = htmlBody[:index] + footer + htmlBody[index:]
		} else {
			htmlBody += footer
		}
	}
	if textBody != "" || htmlBody == "" {
		textBody = strings.TrimRight(textBody, "\n") + "\n\n" + label + ": " + link + "\n"
	}
	return htmlBody, textBody
}

// unsubscribeToken names one contact of one organization. It is signed with a key derived from
// the inbound secret and never expires, so a link in an old message keeps working.
func (s *service) unsubscribeToken(organizationID, contactID uint) string {
	payload := strconv.FormatUint(uint64(organizationID), 10) + "." + strconv.FormatUint(uint64(contactID), 10)
	return payload + "." + s.unsubscribeMAC(payload)
}

func (s *service) unsubscribeMAC(payload string) string {
	key := hmac.New(sha256.New, []byte(s.settings.InboundSecret))
	key.Write([]byte("zmail-unsubscribe-v1"))
	mac := hmac.New(sha256.New, key.Sum(nil))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

// Unsubscribe applies one unsubscribe link. A valid link always succeeds, even for a contact that
// was deleted, so the answer discloses nothing; a suppressed contact stays suppressed.
func (s *service) Unsubscribe(ctx context.Context, token string) error {
	if s.settings.InboundSecret == "" || len(token) > 200 {
		return ErrMailUnsubscribeInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrMailUnsubscribeInvalid
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(parts[2]), []byte(s.unsubscribeMAC(payload))) {
		return ErrMailUnsubscribeInvalid
	}
	organizationID, orgErr := strconv.ParseUint(parts[0], 10, 32)
	contactID, contactErr := strconv.ParseUint(parts[1], 10, 32)
	if orgErr != nil || contactErr != nil {
		return ErrMailUnsubscribeInvalid
	}
	contact, err := s.broadcasts.FindContactForBroadcast(ctx, uint(organizationID), uint(contactID))
	if errors.Is(err, ErrMailContactNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if contact.Status != domain.MailContactSubscribed {
		return nil
	}
	err = s.broadcasts.SetContactStatus(ctx, uint(organizationID), uint(contactID), domain.MailContactUnsubscribed, unsubscribeReasonLink)
	if errors.Is(err, ErrMailContactNotFound) {
		return nil
	}
	return err
}
