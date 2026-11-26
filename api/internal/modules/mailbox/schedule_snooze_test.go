package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// scheduleSnoozeRepo extends fakeRepository with in-memory waiting/scheduled send leasing,
// organize updates, inbox listing, and snooze wake-up so every unit test runs without Postgres.
type scheduleSnoozeRepo struct {
	*inboxRecorder
	leases map[uint]time.Time
}

var (
	_ domain.MailOrganizeRepository = (*scheduleSnoozeRepo)(nil)
	_ domain.MailSnoozeRepository   = (*scheduleSnoozeRepo)(nil)
)

func newScheduleSnoozeRepo() *scheduleSnoozeRepo {
	return &scheduleSnoozeRepo{
		inboxRecorder: newInboxRecorder(),
		leases:        map[uint]time.Time{},
	}
}

func (r *scheduleSnoozeRepo) WaitingSendOrganizations(_ context.Context) ([]uint, error) {
	seen := map[uint]bool{}
	var orgs []uint
	for _, msg := range r.messages {
		if msg.QueuedReason == "" || msg.Placement == domain.MailPlacementTrash {
			continue
		}
		box, ok := r.mailboxes[msg.MailboxID]
		if !ok || seen[box.OrganizationID] {
			continue
		}
		seen[box.OrganizationID] = true
		orgs = append(orgs, box.OrganizationID)
	}
	slices.Sort(orgs)
	return orgs, nil
}

func (r *scheduleSnoozeRepo) LeaseWaitingSends(
	_ context.Context,
	organizationID uint,
	now, leaseUntil, _ time.Time,
	sendLimit, limit int,
) ([]*domain.MailMessage, error) {
	if limit <= 0 {
		return nil, nil
	}
	var attempts int
	for _, msg := range r.messages {
		box, ok := r.mailboxes[msg.MailboxID]
		if ok && box.OrganizationID == organizationID && msg.Direction == domain.MailDirectionOutbound && msg.QueuedReason == "" {
			attempts++
		}
	}
	take := min(sendLimit-attempts, limit)
	if take <= 0 {
		return nil, nil
	}
	var candidates []*domain.MailMessage
	for _, msg := range r.messages {
		box, ok := r.mailboxes[msg.MailboxID]
		if !ok || box.OrganizationID != organizationID || msg.Placement == domain.MailPlacementTrash {
			continue
		}
		eligible := msg.QueuedReason == domain.MailQueuedForSendLimit ||
			(msg.QueuedReason == domain.MailQueuedForSchedule && msg.ScheduledAt != nil && !msg.ScheduledAt.After(now))
		if !eligible {
			continue
		}
		if until, leased := r.leases[msg.ID]; leased && until.After(now) {
			continue
		}
		copied := *msg
		candidates = append(candidates, &copied)
	}
	slices.SortFunc(candidates, func(a, b *domain.MailMessage) int { return int(a.ID) - int(b.ID) })
	if len(candidates) > take {
		candidates = candidates[:take]
	}
	for _, msg := range candidates {
		r.leases[msg.ID] = leaseUntil
	}
	return candidates, nil
}

func (r *scheduleSnoozeRepo) StartWaitingSend(_ context.Context, id uint, attemptedAt time.Time) (bool, error) {
	msg, ok := r.messages[id]
	if !ok || msg.QueuedReason == "" || msg.Placement == domain.MailPlacementTrash {
		return false, nil
	}
	msg.QueuedReason = ""
	msg.ScheduledAt = nil
	msg.SendAttemptedAt = &attemptedAt
	delete(r.leases, id)
	return true, nil
}

func (r *scheduleSnoozeRepo) CancelWaitingSend(_ context.Context, id uint, failureCode string) error {
	if msg, ok := r.messages[id]; ok && msg.QueuedReason != "" {
		msg.Status = domain.MailStatusFailed
		msg.FailureCode = failureCode
		msg.QueuedReason = ""
		msg.ScheduledAt = nil
		delete(r.leases, id)
	}
	return nil
}

func (r *scheduleSnoozeRepo) LockMessages(_ context.Context, mailboxID uint, ids []uint) ([]*domain.MailMessage, error) {
	var found []*domain.MailMessage
	for _, id := range ids {
		if msg, ok := r.messages[id]; ok && msg.MailboxID == mailboxID && !r.sealed(msg) {
			copied := *msg
			found = append(found, &copied)
		}
	}
	return found, nil
}

func (r *scheduleSnoozeRepo) LockFolder(context.Context, uint, uint) (*domain.MailFolder, error) {
	return nil, ErrMailFolderNotFound
}

func (r *scheduleSnoozeRepo) LockLabels(context.Context, uint, []uint) ([]*domain.MailLabel, error) {
	return nil, nil
}

func (r *scheduleSnoozeRepo) ApplyMessageChange(
	_ context.Context,
	ids []uint,
	membershipID uint,
	change domain.MailMessageChange,
	at time.Time,
) error {
	for _, id := range ids {
		msg, ok := r.messages[id]
		if !ok {
			continue
		}
		if change.Read != nil && msg.Direction == domain.MailDirectionInbound {
			if *change.Read {
				r.reads[[2]uint{membershipID, id}] = at
			} else {
				delete(r.reads, [2]uint{membershipID, id})
			}
		}
		if change.Placement != nil {
			msg.Placement = *change.Placement
			if *change.Placement == domain.MailPlacementTrash {
				trashed := at
				msg.TrashedAt = &trashed
				msg.SnoozedUntil = nil
				if msg.QueuedReason != "" {
					msg.Status = domain.MailStatusFailed
					msg.FailureCode = failureSendCanceled
					msg.QueuedReason = ""
					msg.ScheduledAt = nil
					delete(r.leases, id)
				}
			}
		}
		if change.SnoozedUntil != nil {
			until := change.SnoozedUntil.UTC()
			msg.SnoozedUntil = &until
		} else if change.ClearSnooze != nil && *change.ClearSnooze {
			msg.SnoozedUntil = nil
		}
		msg.UpdatedAt = at
	}
	return nil
}

func (r *scheduleSnoozeRepo) DeleteTrashedMessages(context.Context, uint, []uint) (int, error) {
	return 0, nil
}
func (r *scheduleSnoozeRepo) PurgeExpiredTrash(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func (r *scheduleSnoozeRepo) ListThreads(context.Context, uint, uint, domain.MailMessageFilter) ([]domain.MailThreadSummary, int64, error) {
	return nil, 0, nil
}
func (r *scheduleSnoozeRepo) ThreadMessages(context.Context, uint, uint, uint) ([]*domain.MailMessage, error) {
	return nil, nil
}
func (r *scheduleSnoozeRepo) MessageCounts(context.Context, uint, uint) (domain.MailMessageCounts, error) {
	return domain.MailMessageCounts{}, nil
}
func (r *scheduleSnoozeRepo) CreateFolder(context.Context, *domain.MailFolder, int) error { return nil }
func (r *scheduleSnoozeRepo) RenameFolder(context.Context, uint, uint, string) (*domain.MailFolder, error) {
	return nil, nil
}
func (r *scheduleSnoozeRepo) DeleteFolder(context.Context, uint, uint) error { return nil }
func (r *scheduleSnoozeRepo) ListFolders(context.Context, uint) ([]*domain.MailFolder, error) {
	return nil, nil
}
func (r *scheduleSnoozeRepo) CountFolders(context.Context, uint) (int64, error)      { return 0, nil }
func (r *scheduleSnoozeRepo) CreateLabel(context.Context, *domain.MailLabel, int) error { return nil }
func (r *scheduleSnoozeRepo) UpdateLabel(context.Context, *domain.MailLabel) error   { return nil }
func (r *scheduleSnoozeRepo) DeleteLabel(context.Context, uint, uint) error          { return nil }
func (r *scheduleSnoozeRepo) ListLabels(context.Context, uint) ([]*domain.MailLabel, error) {
	return nil, nil
}
func (r *scheduleSnoozeRepo) CountLabels(context.Context, uint) (int64, error) { return 0, nil }

func (r *scheduleSnoozeRepo) ListMessages(ctx context.Context, mailboxID, membershipID uint, filter domain.MailMessageFilter) ([]*domain.MailMessage, int64, error) {
	all, _, err := r.fakeRepository.ListMessages(ctx, mailboxID, membershipID, domain.MailMessageFilter{
		Direction: filter.Direction,
		Unread:    filter.Unread,
		Query:     filter.Query,
	})
	if err != nil {
		return nil, 0, err
	}
	now := filter.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	filtered := make([]*domain.MailMessage, 0, len(all))
	for _, msg := range all {
		placement := placementOrDefault(msg.Placement)
		if filter.Placement == "" && placement == domain.MailPlacementTrash {
			continue
		}
		if filter.Placement != "" && placement != filter.Placement {
			continue
		}
		activelySnoozed := msg.SnoozedUntil != nil && msg.SnoozedUntil.After(now)
		switch {
		case filter.Snoozed != nil && *filter.Snoozed:
			if !activelySnoozed {
				continue
			}
		case filter.Snoozed != nil && !*filter.Snoozed:
			if activelySnoozed {
				continue
			}
		case filter.Placement == domain.MailPlacementDefault && filter.Direction != domain.MailDirectionOutbound:
			if activelySnoozed {
				continue
			}
		}
		copied := *msg
		filtered = append(filtered, &copied)
	}
	return filtered, int64(len(filtered)), nil
}

func (r *scheduleSnoozeRepo) ListInbox(_ context.Context, query domain.InboxQuery) ([]*domain.MailMessage, error) {
	r.queries = append(r.queries, query)
	now := query.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	listed := []*domain.MailMessage{}
	for _, msg := range r.messages {
		if !slices.Contains(query.MailboxIDs, msg.MailboxID) || placementOrDefault(msg.Placement) == domain.MailPlacementTrash {
			continue
		}
		activelySnoozed := msg.SnoozedUntil != nil && msg.SnoozedUntil.After(now)
		switch query.View {
		case domain.InboxViewSnoozed:
			if !activelySnoozed {
				continue
			}
		case domain.InboxViewAll:
			if msg.Direction != domain.MailDirectionInbound || placementOrDefault(msg.Placement) != domain.MailPlacementDefault || activelySnoozed {
				continue
			}
		case domain.InboxViewSent:
			if msg.Direction != domain.MailDirectionOutbound || placementOrDefault(msg.Placement) != domain.MailPlacementDefault {
				continue
			}
		default:
			continue
		}
		copied := *msg
		copied.MailboxAddress = r.mailboxes[msg.MailboxID].Address
		listed = append(listed, &copied)
	}
	slices.SortFunc(listed, func(a, b *domain.MailMessage) int { return int(b.ID) - int(a.ID) })
	if len(listed) > query.Limit {
		listed = listed[:query.Limit]
	}
	return listed, nil
}

func (r *scheduleSnoozeRepo) WakeDueSnoozedMessages(_ context.Context, now time.Time) (int, error) {
	at := now.UTC()
	woken := 0
	for _, msg := range r.messages {
		if placementOrDefault(msg.Placement) != domain.MailPlacementDefault {
			continue
		}
		if msg.SnoozedUntil != nil && !msg.SnoozedUntil.After(at) {
			msg.SnoozedUntil = nil
			msg.UpdatedAt = at
			for key := range r.reads {
				if key[1] == msg.ID {
					delete(r.reads, key)
				}
			}
			woken++
		}
	}
	return woken, nil
}

type scheduleSnoozeUnitScene struct {
	repo      *scheduleSnoozeRepo
	transport *fakeTransport
	svc       *service
	clock     time.Time
	viewer    domain.OrganizationContext
	viewOnly  domain.OrganizationContext
	mailbox   *domain.Mailbox
}

func newScheduleSnoozeUnitScene(t *testing.T) *scheduleSnoozeUnitScene {
	t.Helper()
	repo := newScheduleSnoozeRepo()
	transport := &fakeTransport{}
	memberships := newAccessMemberships()
	svc := NewService(repo, memberships, nil, nil, nil, repo, nil, nil, nil, transport, &fakeNotifier{}, accessSettings(), nil, nil, nil, nil)
	clock := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }

	scene := &scheduleSnoozeUnitScene{
		repo:      repo,
		transport: transport,
		svc:       svc,
		clock:     clock,
		viewer:    viewerOf(accessOrg, accessB, domain.OrganizationRoleMember),
		viewOnly:  viewerOf(accessOrg, accessC, domain.OrganizationRoleMember),
	}
	svc.now = func() time.Time { return scene.clock }
	admin := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleAdmin)
	box, err := svc.CreateMailbox(context.Background(), admin, CreateMailboxInput{
		LocalPart: "team",
		Type:      domain.MailboxTypeShared,
		Members: []MailboxMemberInput{
			{UserID: accessB, Permission: domain.MailboxPermissionSend},
			{UserID: accessC, Permission: domain.MailboxPermissionView},
		},
	})
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	scene.mailbox = box
	return scene
}

func (s *scheduleSnoozeUnitScene) seedInbound(t *testing.T, subject string) *domain.MailMessage {
	t.Helper()
	msg := &domain.MailMessage{
		MailboxID:   s.mailbox.ID,
		Direction:   domain.MailDirectionInbound,
		Status:      domain.MailStatusReceived,
		Placement:   domain.MailPlacementDefault,
		FromAddress: "alice@example.com",
		To:          []string{s.mailbox.Address},
		Subject:     subject,
		TextBody:    "hello " + subject,
		CreatedAt:   s.clock,
		UpdatedAt:   s.clock,
	}
	if _, err := s.repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("seedInbound(%q): %v", subject, err)
	}
	return msg
}

func TestScheduleSendValidation(t *testing.T) {
	scene := newScheduleSnoozeUnitScene(t)
	ctx := context.Background()

	past := scene.clock.Add(-5 * time.Minute)
	tooSoon := scene.clock.Add(30 * time.Second)
	tooFar := scene.clock.Add(366 * 24 * time.Hour)

	for name, when := range map[string]time.Time{
		"in the past":    past,
		"under 1 minute": tooSoon,
		"over 365 days":  tooFar,
	} {
		t.Run(name, func(t *testing.T) {
			at := when
			_, _, err := scene.svc.SendMessage(ctx, scene.viewer, scene.mailbox.ID, SendInput{
				To:          []string{"bob@example.com"},
				Subject:     "Scheduled",
				Text:        "body",
				ScheduledAt: &at,
			})
			if !errors.Is(err, ErrMailMessageInvalid) {
				t.Fatalf("err = %v; want ErrMailMessageInvalid", err)
			}
		})
	}
	if len(scene.transport.sent) != 0 {
		t.Fatalf("transport sent %d messages on invalid schedules; want 0", len(scene.transport.sent))
	}

	// Boundaries: exactly +1m and +365d are accepted.
	for _, offset := range []time.Duration{time.Minute, 365 * 24 * time.Hour} {
		at := scene.clock.Add(offset)
		msg, created, err := scene.svc.SendMessage(ctx, scene.viewer, scene.mailbox.ID, SendInput{
			To:          []string{"bob@example.com"},
			Subject:     "Boundary",
			Text:        "body",
			ScheduledAt: &at,
		})
		if err != nil || !created {
			t.Fatalf("offset %v: msg=%+v created=%v err=%v", offset, msg, created, err)
		}
		if msg.Status != domain.MailStatusQueued || msg.QueuedReason != domain.MailQueuedForSchedule || msg.ScheduledAt == nil || !msg.ScheduledAt.Equal(at) {
			t.Fatalf("offset %v: got status=%q reason=%q scheduled_at=%v", offset, msg.Status, msg.QueuedReason, msg.ScheduledAt)
		}
	}
	if len(scene.transport.sent) != 0 {
		t.Fatalf("scheduled sends called transport immediately: %d", len(scene.transport.sent))
	}
}

func TestScheduleSendHTTPAndWorkerDispatch(t *testing.T) {
	scene := newScheduleSnoozeUnitScene(t)
	ctx := context.Background()
	sendAt := scene.clock.Add(2 * time.Hour)

	payload, _ := json.Marshal(map[string]any{
		"to":           []string{"bob@example.com"},
		"subject":      "Later note",
		"text":         "See you soon",
		"scheduled_at": sendAt.Format(time.RFC3339),
	})
	router := accessRouter(scene.svc, scene.viewer)
	rec, body := serve(router, http.MethodPost, fmt.Sprintf("/mailboxes/%d/messages", scene.mailbox.ID), string(payload))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST scheduled message = %d %v; want 202", rec.Code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["status"] != "queued" || data["queued_reason"] != domain.MailQueuedForSchedule || data["scheduled_at"] == nil {
		t.Fatalf("scheduled response = %v; want queued/scheduled with scheduled_at", data)
	}
	msgID := uint(data["id"].(float64))

	// A future scheduled email must NOT block an immediate personal send.
	immediate, _, err := scene.svc.SendMessage(ctx, scene.viewer, scene.mailbox.ID, SendInput{
		To:      []string{"carol@example.com"},
		Subject: "Now",
		Text:    "urgent",
	})
	if err != nil || immediate.Status != domain.MailStatusSent || immediate.QueuedReason != "" {
		t.Fatalf("immediate send while scheduled exists = %+v, %v; want sent immediately", immediate, err)
	}

	// Before scheduled_at, the worker does not send the scheduled message.
	before := len(scene.transport.sent)
	if dispatched, err := scene.svc.DispatchQueuedSends(ctx, 25); err != nil || dispatched != 0 {
		t.Fatalf("DispatchQueuedSends before due = %d, %v; want 0", dispatched, err)
	}

	// Schedule a second email and move it to Trash to cancel it.
	cancelAt := scene.clock.Add(2 * time.Hour)
	canceled, _, err := scene.svc.SendMessage(ctx, scene.viewer, scene.mailbox.ID, SendInput{
		To:          []string{"dave@example.com"},
		Subject:     "Canceled scheduled",
		Text:        "never send",
		ScheduledAt: &cancelAt,
	})
	if err != nil {
		t.Fatalf("schedule second message: %v", err)
	}
	trash := domain.MailPlacementTrash
	if _, err := scene.svc.UpdateMessage(ctx, scene.viewer, scene.mailbox.ID, canceled.ID, MessageUpdate{Placement: &trash}); err != nil {
		t.Fatalf("trash scheduled message: %v", err)
	}
	storedCanceled := scene.repo.messages[canceled.ID]
	if storedCanceled.Status != domain.MailStatusFailed || storedCanceled.FailureCode != failureSendCanceled ||
		storedCanceled.QueuedReason != "" || storedCanceled.ScheduledAt != nil {
		t.Fatalf("trashed scheduled = %+v; want failed/send.canceled with cleared schedule", storedCanceled)
	}

	// Advance clock to scheduled_at: only the non-trashed scheduled email is delivered.
	scene.clock = sendAt
	dispatched, err := scene.svc.DispatchQueuedSends(ctx, 25)
	if err != nil || dispatched != 1 {
		t.Fatalf("DispatchQueuedSends at due = %d, %v; want 1", dispatched, err)
	}
	if len(scene.transport.sent) != before+1 || scene.transport.sent[before].Subject != "Later note" {
		t.Fatalf("sent after dispatch = %+v; want Later note", scene.transport.sent[before:])
	}
	delivered := scene.repo.messages[msgID]
	if delivered.Status != domain.MailStatusSent || delivered.QueuedReason != "" || delivered.ScheduledAt != nil || delivered.SendAttemptedAt == nil {
		t.Fatalf("delivered scheduled message = %+v; want sent with cleared queued_reason and scheduled_at", delivered)
	}
}

func TestSnoozeValidationHideListAndWake(t *testing.T) {
	scene := newScheduleSnoozeUnitScene(t)
	ctx := context.Background()
	msgA := scene.seedInbound(t, "Snooze me")
	msgB := scene.seedInbound(t, "Keep visible")

	// Mark msgA read before snoozing so we prove waking marks it unread again.
	if _, err := scene.svc.SetMessageRead(ctx, scene.viewer, scene.mailbox.ID, msgA.ID, true); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}

	// Validation: past, now, >365d, and conflicting snoozed_until + clear_snooze.
	past := scene.clock.Add(-time.Minute)
	atNow := scene.clock
	tooFar := scene.clock.Add(366 * 24 * time.Hour)
	validUntil := scene.clock.Add(3 * time.Hour)
	for name, upd := range map[string]MessageUpdate{
		"in the past":        {SnoozedUntil: &past},
		"equal to now":       {SnoozedUntil: &atNow},
		"over 365 days":      {SnoozedUntil: &tooFar},
		"both set and clear": {SnoozedUntil: &validUntil, ClearSnooze: boolPtr(true)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := scene.svc.UpdateMessage(ctx, scene.viewer, scene.mailbox.ID, msgA.ID, upd); err == nil {
				t.Fatalf("expected validation error for %s", name)
			} else if _, ok := asValidationError(err); !ok {
				t.Fatalf("err = %v; want ValidationError", err)
			}
		})
	}

	// View-only member cannot snooze.
	if _, err := scene.svc.UpdateMessage(ctx, scene.viewOnly, scene.mailbox.ID, msgA.ID, MessageUpdate{SnoozedUntil: &validUntil}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("view-only snooze err = %v; want ErrPermissionDenied", err)
	}

	// Snooze msgA via HTTP PUT and verify snoozed_until is returned.
	router := accessRouter(scene.svc, scene.viewer)
	snoozeBody, _ := json.Marshal(map[string]any{"snoozed_until": validUntil.Format(time.RFC3339)})
	rec, body := serve(router, http.MethodPut, fmt.Sprintf("/mailboxes/%d/messages/%d", scene.mailbox.ID, msgA.ID), string(snoozeBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT snooze = %d %v; want 200", rec.Code, body)
	}
	if data, _ := body["data"].(map[string]any); data["snoozed_until"] == nil {
		t.Fatalf("PUT snooze response missing snoozed_until: %v", data)
	}

	// Inbox listing excludes msgA while snoozed; snoozed view includes only msgA with snoozed_until.
	allPage, err := scene.svc.ListInbox(ctx, scene.viewer, InboxRequest{View: domain.InboxViewAll, PerPage: 15})
	if err != nil || len(allPage.Items) != 1 || allPage.Items[0].Message.ID != msgB.ID {
		t.Fatalf("ListInbox(all) = %+v, %v; want only msgB", allPage.Items, err)
	}
	snoozedPage, err := scene.svc.ListInbox(ctx, scene.viewer, InboxRequest{View: domain.InboxViewSnoozed, PerPage: 15})
	if err != nil || len(snoozedPage.Items) != 1 || snoozedPage.Items[0].Message.ID != msgA.ID || snoozedPage.Items[0].Message.SnoozedUntil == nil {
		t.Fatalf("ListInbox(snoozed) = %+v, %v; want only msgA with snoozed_until", snoozedPage.Items, err)
	}
	inboxItems := toInboxItemResponses(snoozedPage.Items)
	if inboxItems[0].SnoozedUntil == nil || !inboxItems[0].SnoozedUntil.Equal(validUntil) {
		t.Fatalf("InboxItemResponse.SnoozedUntil = %v; want %v", inboxItems[0].SnoozedUntil, validUntil)
	}

	// Single-mailbox ListMessages excludes snoozed in Inbox and returns snoozed with ?folder=snoozed / ?snoozed=true.
	rec, body = serve(router, http.MethodGet, fmt.Sprintf("/mailboxes/%d/messages?placement=default&direction=inbound", scene.mailbox.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET mailbox inbox = %d %v", rec.Code, body)
	}
	if items := dataItems(t, body); len(items) != 1 {
		t.Fatalf("mailbox inbox items = %d; want 1 (msgB only)", len(items))
	}
	rec, body = serve(router, http.MethodGet, fmt.Sprintf("/mailboxes/%d/messages?folder=snoozed", scene.mailbox.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET folder=snoozed = %d %v", rec.Code, body)
	}
	if items := dataItems(t, body); len(items) != 1 {
		t.Fatalf("folder=snoozed items = %d; want 1 (msgA only)", len(items))
	}

	// Batch clear_snooze clears snooze on msgB after batch snoozing it.
	if updated, err := scene.svc.UpdateMessages(ctx, scene.viewer, scene.mailbox.ID, []uint{msgB.ID}, MessageUpdate{SnoozedUntil: &validUntil}); err != nil || updated != 1 {
		t.Fatalf("batch snooze = %d, %v", updated, err)
	}
	if updated, err := scene.svc.UpdateMessages(ctx, scene.viewer, scene.mailbox.ID, []uint{msgB.ID}, MessageUpdate{ClearSnooze: boolPtr(true)}); err != nil || updated != 1 {
		t.Fatalf("batch clear_snooze = %d, %v", updated, err)
	}
	if scene.repo.messages[msgB.ID].SnoozedUntil != nil {
		t.Fatalf("msgB snoozed_until still set after clear_snooze")
	}

	// Advance clock to validUntil: WakeDueSnoozedMessages clears snoozed_until and marks msgA unread.
	scene.clock = validUntil
	woken, err := scene.svc.WakeDueSnoozedMessages(ctx, scene.clock)
	if err != nil || woken != 1 {
		t.Fatalf("WakeDueSnoozedMessages = %d, %v; want 1", woken, err)
	}
	refreshed, err := scene.svc.GetMessage(ctx, scene.viewer, scene.mailbox.ID, msgA.ID)
	if err != nil {
		t.Fatalf("GetMessage(msgA): %v", err)
	}
	if refreshed.SnoozedUntil != nil || refreshed.Read {
		t.Fatalf("woken msgA = snoozed_until %v, read %v; want nil and unread", refreshed.SnoozedUntil, refreshed.Read)
	}
	// Back in Inbox and gone from Snoozed view.
	allAfter, err := scene.svc.ListInbox(ctx, scene.viewer, InboxRequest{View: domain.InboxViewAll, PerPage: 15})
	if err != nil || len(allAfter.Items) != 2 {
		t.Fatalf("ListInbox(all) after wake = %d, %v; want 2", len(allAfter.Items), err)
	}
	snoozedAfter, err := scene.svc.ListInbox(ctx, scene.viewer, InboxRequest{View: domain.InboxViewSnoozed, PerPage: 15})
	if err != nil || len(snoozedAfter.Items) != 0 {
		t.Fatalf("ListInbox(snoozed) after wake = %d, %v; want 0", len(snoozedAfter.Items), err)
	}
}

func TestPostgresScheduleSendAndSnoozeRoundTrip(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	start := f.clock

	// 1. Schedule a single email 2 hours ahead; verify immediate sends are unaffected.
	sendAt := start.Add(2 * time.Hour)
	scheduled, _, err := f.service.SendMessage(ctx, f.b, f.team.ID, SendInput{
		To:          []string{"bob@example.com"},
		Subject:     "pg-scheduled",
		Text:        "hello future",
		ScheduledAt: &sendAt,
	})
	if err != nil || scheduled.QueuedReason != domain.MailQueuedForSchedule || scheduled.ScheduledAt == nil {
		t.Fatalf("scheduled = %+v, %v", scheduled, err)
	}
	immediate := f.mustSend(t, "pg-immediate", false)
	if immediate.Status != domain.MailStatusSent {
		t.Fatalf("immediate send = %+v; want sent", immediate)
	}
	if got := f.dispatch(t, 25); got != 0 {
		t.Fatalf("dispatch before scheduled_at = %d; want 0", got)
	}

	// Advance clock to scheduled_at and dispatch.
	f.clock = sendAt
	if got := f.dispatch(t, 25); got != 1 {
		t.Fatalf("dispatch at scheduled_at = %d; want 1", got)
	}
	po := f.stored(t, scheduled.ID)
	if po.Status != string(domain.MailStatusSent) || po.QueuedReason != nil || po.ScheduledAt != nil {
		t.Fatalf("dispatched scheduled PO = status %q reason %v scheduled_at %v", po.Status, po.QueuedReason, po.ScheduledAt)
	}

	// 2. Snooze an inbound inbox message, verify it hides from Inbox & unread counts, appears in
	// snoozed view, and wakes up unread once its deadline passes.
	if _, err := f.service.SetMessageRead(ctx, f.b, f.team.ID, f.teamMessage.ID, true); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}
	snoozeUntil := f.clock.Add(time.Hour)
	updated, err := f.service.UpdateMessage(ctx, f.b, f.team.ID, f.teamMessage.ID, MessageUpdate{SnoozedUntil: &snoozeUntil})
	if err != nil || updated.SnoozedUntil == nil {
		t.Fatalf("snooze update = %+v, %v", updated, err)
	}
	inboxPage, err := f.service.ListInbox(ctx, f.b, InboxRequest{View: domain.InboxViewAll, MailboxID: f.team.ID, PerPage: 15})
	if err != nil || len(inboxPage.Items) != 0 {
		t.Fatalf("ListInbox(all) while snoozed = %d, %v; want 0", len(inboxPage.Items), err)
	}
	snoozedPage, err := f.service.ListInbox(ctx, f.b, InboxRequest{View: domain.InboxViewSnoozed, MailboxID: f.team.ID, PerPage: 15})
	if err != nil || len(snoozedPage.Items) != 1 || snoozedPage.Items[0].Message.ID != f.teamMessage.ID {
		t.Fatalf("ListInbox(snoozed) = %+v, %v; want teamMessage", snoozedPage.Items, err)
	}

	// Advance clock past snoozeUntil and trigger opportunistic wake via ListInbox.
	f.clock = snoozeUntil.Add(time.Second)
	wokenPage, err := f.service.ListInbox(ctx, f.b, InboxRequest{View: domain.InboxViewAll, MailboxID: f.team.ID, PerPage: 15})
	if err != nil || len(wokenPage.Items) != 1 || wokenPage.Items[0].Message.ID != f.teamMessage.ID {
		t.Fatalf("ListInbox(all) after snooze due = %+v, %v; want teamMessage woken", wokenPage.Items, err)
	}
	if wokenPage.Items[0].Message.Read || wokenPage.Items[0].Message.SnoozedUntil != nil {
		t.Fatalf("woken message = read %v snoozed_until %v; want unread and nil", wokenPage.Items[0].Message.Read, wokenPage.Items[0].Message.SnoozedUntil)
	}
}
