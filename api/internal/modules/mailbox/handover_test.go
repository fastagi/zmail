package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/events"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// leave removes user from the organization as the organization starter would: the membership is
// gone, and the removed event follows the commit.
func (f *accessFixture) leave(t *testing.T, viewer domain.OrganizationContext, at time.Time) {
	t.Helper()
	f.memberships.absent[viewer.UserID] = true
	event := domain.NewOrganizationMemberRemovedEvent(accessOrg, viewer.UserID, viewer.MembershipID, at)
	if err := f.service.MemberRemoved(context.Background(), event); err != nil {
		t.Fatalf("MemberRemoved: %v", err)
	}
}

// rejoinedMembership is B's membership after rejoining.
const rejoinedMembership uint = 9002

// rejoin brings B back with a new membership and returns the new viewer.
func (f *accessFixture) rejoin(t *testing.T, at time.Time) domain.OrganizationContext {
	const userID, membershipID = accessB, rejoinedMembership
	t.Helper()
	delete(f.memberships.absent, userID)
	f.memberships.ids[userID] = membershipID
	event := domain.NewOrganizationMemberJoinedEvent(accessOrg, userID, membershipID, at)
	if err := f.service.MemberJoined(context.Background(), event); err != nil {
		t.Fatalf("MemberJoined: %v", err)
	}
	viewer := viewerOf(accessOrg, userID, domain.OrganizationRoleMember)
	viewer.MembershipID = membershipID
	return viewer
}

// handOver hands a mailbox over to C as the admin.
func (f *accessFixture) handOver(t *testing.T, mailboxID uint) *domain.Mailbox {
	t.Helper()
	mailbox, err := f.service.HandOverMailbox(context.Background(), f.admin, mailboxID, accessC)
	if err != nil {
		t.Fatalf("HandOverMailbox: %v", err)
	}
	return mailbox
}

// receiveSigned delivers one inbound message through the mail edge route's service call.
func (f *accessFixture) receiveSigned(t *testing.T, mailbox *domain.Mailbox, messageID string) *domain.MailMessage {
	t.Helper()
	raw := inboundRaw(messageID)
	now := f.service.now()
	message, created, err := f.service.ReceiveInbound(context.Background(), InboundDelivery{
		Recipient: mailbox.Address, Raw: raw, Signature: signedHeader(now, mailbox.Address, raw),
	})
	if err != nil || !created {
		t.Fatalf("ReceiveInbound = %v, created %v", err, created)
	}
	return message
}

func accessEventsOf(t *testing.T, f *accessFixture, mailboxID uint) []*domain.MailboxAccessEvent {
	t.Helper()
	events, _, err := f.service.ListMailboxAccessEvents(context.Background(), f.admin, mailboxID, 1, 100)
	if err != nil {
		t.Fatalf("ListMailboxAccessEvents: %v", err)
	}
	return events
}

func eventsOfType(events []*domain.MailboxAccessEvent, eventType domain.MailboxAccessEventType) []*domain.MailboxAccessEvent {
	var matched []*domain.MailboxAccessEvent
	for _, event := range events {
		if event.Type == eventType {
			matched = append(matched, event)
		}
	}
	return matched
}

// tick moves the fixture clock forward, so database timestamps written after it are later.
func tick() time.Time {
	time.Sleep(2 * time.Millisecond)
	at := time.Now().UTC()
	time.Sleep(2 * time.Millisecond)
	return at
}

// AC-10: the owner leaves; the personal mailbox awaits handover, still receives mail, and
// nobody can read it.
func TestOwnerDepartureAwaitsHandover(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box := f.personal.ID

	f.leave(t, f.b, tick())

	listed := listedMailbox(t, f.service, f.admin, box)
	if listed == nil || listed.Type != domain.MailboxTypePersonal || listed.OwnerLeftAt == nil {
		t.Fatalf("admin sees %+v; want the personal mailbox awaiting handover", listed)
	}
	left := eventsOfType(accessEventsOf(t, f, box), domain.MailboxAccessOwnerLeft)
	if len(left) != 1 || left[0].ActorUserID != nil || left[0].SubjectUserID == nil || *left[0].SubjectUserID != accessB ||
		left[0].Detail["reason"] != "left_organization" {
		t.Fatalf("owner_left events = %+v", left)
	}

	// Mail still arrives.
	arrived := f.receiveSigned(t, f.personal, "after-leaving@sender.test")
	// Nobody reads it: the admin is refused, colleagues do not see the mailbox.
	_, err := f.service.GetMessage(ctx, f.admin, box, arrived.ID)
	expectError(t, "admin GetMessage", err, domain.ErrPermissionDenied)
	_, _, err = f.service.ListMessages(ctx, f.c, box, domain.MailMessageFilter{})
	expectError(t, "colleague ListMessages", err, ErrMailboxNotFound)

	// A repeated event changes nothing but the time it records.
	f.leave(t, f.b, tick())
	if left := eventsOfType(accessEventsOf(t, f, box), domain.MailboxAccessOwnerLeft); len(left) != 2 {
		t.Fatalf("owner_left events after a repeat = %d; want 2", len(left))
	}
}

// AC-11: handover makes the mailbox shared with the successor and seals what came before the
// owner left, on every surface.
func TestHandoverSealsHistory(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box := f.personal.ID
	sealed := f.personalMessage
	if _, err := f.service.SetMessageRead(ctx, f.b, box, sealed.ID, true); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}
	leftAt := tick()
	f.leave(t, f.b, leftAt)
	visible := f.receiveSigned(t, f.personal, "after-leaving@sender.test")

	handed := f.handOver(t, box)
	if handed.Type != domain.MailboxTypeShared || handed.OwnerUserID != nil || handed.OwnerLeftAt != nil ||
		handed.SealedBefore == nil || !handed.SealedBefore.Equal(leftAt.Truncate(time.Microsecond)) {
		t.Fatalf("handed over mailbox = %+v; want shared, no owner, sealed at %v", handed, leftAt)
	}
	if !handed.Access.Administer || handed.Access.View {
		t.Fatalf("admin's access after handover = %+v", handed.Access)
	}
	members, err := f.service.ListMailboxMembers(ctx, f.admin, box)
	if err != nil || len(members) != 1 || members[0].UserID != accessC || members[0].Permission != domain.MailboxPermissionSend {
		t.Fatalf("members = %+v, %v; want C with send", members, err)
	}
	handedEvents := eventsOfType(accessEventsOf(t, f, box), domain.MailboxAccessHandedOver)
	if len(handedEvents) != 1 || *handedEvents[0].SubjectUserID != accessC || *handedEvents[0].ActorUserID != accessAdmin ||
		handedEvents[0].Detail["former_owner_user_id"] != float64(accessB) || handedEvents[0].Detail["sealed_before"] == nil {
		t.Fatalf("handed_over events = %+v", handedEvents)
	}

	// The successor sees mail from after the boundary only, everywhere.
	_, err = f.service.GetMessage(ctx, f.c, box, sealed.ID)
	expectError(t, "sealed GetMessage", err, ErrMailMessageNotFound)
	_, err = f.service.GetAttachment(ctx, f.c, box, sealed.ID, 1)
	expectError(t, "sealed GetAttachment", err, ErrMailMessageNotFound)
	_, err = f.service.SetMessageRead(ctx, f.c, box, sealed.ID, true)
	expectError(t, "sealed SetMessageRead", err, ErrMailMessageNotFound)
	if got := readRows(t, f, f.c.MembershipID); got != 0 {
		t.Fatalf("marking a sealed message read stored %d read rows", got)
	}
	listed, total, err := f.service.ListMessages(ctx, f.c, box, domain.MailMessageFilter{})
	if err != nil || total != 1 || len(listed) != 1 || listed[0].ID != visible.ID {
		t.Fatalf("successor list = %v (%d), %v; want only the message after the boundary", listed, total, err)
	}
	log, total, err := f.service.ListLog(ctx, f.c, domain.MailLogFilter{})
	for _, entry := range log {
		if entry.ID == sealed.ID {
			t.Fatal("the log shows a sealed message")
		}
	}
	if err != nil || total != 2 {
		t.Fatalf("successor log total = %d, %v; want 2 (team@ and the unsealed one)", total, err)
	}
	stats, err := f.service.Stats(ctx, f.c, 1)
	if err != nil || len(stats) != 1 || stats[0].Received != 2 {
		t.Fatalf("successor stats = %+v, %v; want 2 received", stats, err)
	}
	if got := unreadOf(t, f, f.c, box); got != 1 {
		t.Fatalf("successor unread = %d; want 1", got)
	}
	// The former owner keeps no read records of the mailbox.
	if got := readRows(t, f, f.b.MembershipID); got != 0 {
		t.Fatalf("former owner read rows = %d; want 0", got)
	}

	// A second handover of the now shared mailbox conflicts.
	_, err = f.service.HandOverMailbox(ctx, f.admin, box, accessD)
	expectError(t, "second handover", err, ErrMailboxAccessConflict)
}

// Handover is for owners and admins of personal mailboxes, to current members.
func TestHandoverRules(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	_, err := f.service.HandOverMailbox(ctx, f.b, f.personal.ID, accessC)
	expectError(t, "owner hands over", err, domain.ErrPermissionDenied)
	_, err = f.service.HandOverMailbox(ctx, f.c, f.personal.ID, accessC)
	expectError(t, "colleague hands over", err, ErrMailboxNotFound)
	_, err = f.service.HandOverMailbox(ctx, f.admin, f.personal.ID, notMember)
	expectError(t, "successor outside the organization", err, ErrMailboxAccessInvalid)
	_, err = f.service.HandOverMailbox(ctx, f.admin, f.team.ID, accessC)
	expectError(t, "shared mailbox", err, ErrMailboxAccessConflict)

	router := accessRouter(f.service, f.admin)
	recorder, body := serve(router, http.MethodPost, fmt.Sprintf("/mailboxes/%d/handovers", f.personal.ID), `{"successor_user_id":3}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusCreated || data["type"] != "shared" || data["sealed_before"] == nil || data["owner_user_id"] != nil {
		t.Fatalf("POST handovers = %d %v", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodPost, fmt.Sprintf("/mailboxes/%d/handovers", f.personal.ID), `{"successor_user_id":3}`)
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailboxAccessConflict {
		t.Fatalf("second POST handovers = %d %v", recorder.Code, body)
	}
}

// Without a departure event the owner's leaving time is unknown, so the boundary is the
// handover: it seals more, never less.
func TestMissedDepartureEventSealsAtHandover(t *testing.T) {
	f := newAccessFixture(t)
	handoverAt := time.Unix(1_900_000_000, 0).UTC()
	f.service.now = func() time.Time { return handoverAt }
	f.memberships.absent[accessB] = true // left, but the event never arrived

	handed := f.handOver(t, f.personal.ID)
	if handed.SealedBefore == nil || !handed.SealedBefore.Equal(handoverAt) {
		t.Fatalf("sealed_before = %v; want the handover time %v", handed.SealedBefore, handoverAt)
	}
}

// An owner still in the organization is handed over at the handover time, and loses access.
func TestHandoverOfAPresentOwnerSealsAtHandover(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	handoverAt := time.Unix(1_900_000_000, 0).UTC()
	f.service.now = func() time.Time { return handoverAt }
	f.repo.(*fakeRepository).mailboxes[f.personal.ID].OwnerLeftAt = &time.Time{} // stale label

	handed := f.handOver(t, f.personal.ID)
	if handed.SealedBefore == nil || !handed.SealedBefore.Equal(handoverAt) {
		t.Fatalf("sealed_before = %v; want %v", handed.SealedBefore, handoverAt)
	}
	_, _, err := f.service.ListMessages(ctx, f.b, f.personal.ID, domain.MailMessageFilter{})
	expectError(t, "former owner ListMessages", err, ErrMailboxNotFound)
}

// Review focus 5: leave, rejoin, leave again: the boundary is the latest departure, so mail the
// owner could read while back is sealed.
func TestBoundaryIsLatestDeparture(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box := f.personal.ID
	f.leave(t, f.b, tick())
	back := f.rejoin(t, tick())
	whileBack := f.receiveSigned(t, f.personal, "while-back@sender.test")
	if _, err := f.service.GetMessage(ctx, back, box, whileBack.ID); err != nil {
		t.Fatalf("returned owner GetMessage: %v", err)
	}
	lastLeft := tick()
	f.leave(t, back, lastLeft)

	handed := f.handOver(t, box)
	if !handed.SealedBefore.Equal(lastLeft.Truncate(time.Microsecond)) {
		t.Fatalf("sealed_before = %v; want the latest departure %v", handed.SealedBefore, lastLeft)
	}
	_, err := f.service.GetMessage(ctx, f.c, box, whileBack.ID)
	expectError(t, "mail from while the owner was back", err, ErrMailMessageNotFound)
}

// AC-16: an owner who rejoins before a handover gets the mailbox back with all mail.
func TestRejoinBeforeHandoverRestores(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box := f.personal.ID
	f.leave(t, f.b, tick())
	back := f.rejoin(t, tick())

	listed := listedMailbox(t, f.service, back, box)
	if listed == nil || listed.OwnerLeftAt != nil || !listed.Access.View {
		t.Fatalf("returned owner sees %+v; want their mailbox back, not awaiting handover", listed)
	}
	if _, err := f.service.GetMessage(ctx, back, box, f.personalMessage.ID); err != nil {
		t.Fatalf("returned owner reads old mail: %v", err)
	}
	returned := eventsOfType(accessEventsOf(t, f, box), domain.MailboxAccessOwnerReturned)
	if len(returned) != 1 || returned[0].ActorUserID != nil || *returned[0].SubjectUserID != accessB {
		t.Fatalf("owner_returned events = %+v", returned)
	}
	// Joining again without having left writes nothing.
	f.rejoin(t, tick())
	if returned := eventsOfType(accessEventsOf(t, f, box), domain.MailboxAccessOwnerReturned); len(returned) != 1 {
		t.Fatalf("owner_returned events after a repeat = %d; want 1", len(returned))
	}
}

// AC-17: after a handover, the returning former owner gets nothing back.
func TestRejoinAfterHandoverDoesNotRestore(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box := f.personal.ID
	f.leave(t, f.b, tick())
	f.handOver(t, box)
	back := f.rejoin(t, tick())

	if listedMailbox(t, f.service, back, box) != nil {
		t.Fatal("the returning former owner sees the handed over mailbox")
	}
	_, err := f.service.GetMessage(ctx, back, box, f.personalMessage.ID)
	expectError(t, "returning former owner GetMessage", err, ErrMailboxNotFound)
	if returned := eventsOfType(accessEventsOf(t, f, box), domain.MailboxAccessOwnerReturned); len(returned) != 0 {
		t.Fatalf("owner_returned written for a handed over mailbox: %+v", returned)
	}
}

// Leaving the organization is recorded on each shared mailbox the person was a member of, from
// the mailbox's own history, once.
func TestDepartureRecordsSharedMemberRemoval(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	// C is on team@; on old@ C was removed earlier; on legacy@ C was a backfilled creator.
	old, err := f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{LocalPart: "old", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessC, Permission: domain.MailboxPermissionView}, {UserID: accessD, Permission: domain.MailboxPermissionView}}})
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if err = f.service.RemoveMailboxMember(ctx, f.admin, old.ID, accessC); err != nil {
		t.Fatalf("RemoveMailboxMember: %v", err)
	}
	legacy, err := f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{LocalPart: "legacy", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessD, Permission: domain.MailboxPermissionManage}}})
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	backfilled := &domain.MailboxAccessEvent{OrganizationID: accessOrg, MailboxID: legacy.ID, Type: domain.MailboxAccessCreated,
		Detail: map[string]any{"type": "shared", "backfilled": true, "membership_id": f.c.MembershipID, "permission": "manage"}}
	if err := f.repo.AddMailboxAccessEvent(ctx, backfilled); err != nil {
		t.Fatalf("AddMailboxAccessEvent: %v", err)
	}

	f.leave(t, f.c, tick())
	f.leave(t, f.c, tick()) // a repeated event records nothing new

	for _, box := range []*domain.Mailbox{f.team, legacy} {
		removed := eventsOfType(accessEventsOf(t, f, box.ID), domain.MailboxAccessMemberRemoved)
		if len(removed) != 1 || removed[0].ActorUserID != nil || *removed[0].SubjectUserID != accessC ||
			removed[0].Detail["reason"] != "left_organization" {
			t.Fatalf("%s member_removed events = %+v", box.Address, removed)
		}
	}
	if removed := eventsOfType(accessEventsOf(t, f, old.ID), domain.MailboxAccessMemberRemoved); len(removed) != 1 || removed[0].ActorUserID == nil {
		t.Fatalf("old@ member_removed events = %+v; want only the earlier removal", removed)
	}
	if removed := eventsOfType(accessEventsOf(t, f, f.personal.ID), domain.MailboxAccessMemberRemoved); len(removed) != 0 {
		t.Fatalf("personal mailbox member_removed events = %+v", removed)
	}
}

// AC-12: an admin deletes the sealed history with the address as confirmation.
func TestDeleteSealedHistory(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	box := f.personal.ID

	_, err := f.service.DeleteSealedHistory(ctx, f.admin, f.team.ID, f.team.Address)
	expectError(t, "no boundary", err, ErrMailboxAccessConflict)

	f.leave(t, f.b, tick())
	after := f.receiveSigned(t, f.personal, "after-leaving@sender.test")
	f.handOver(t, box)

	_, err = f.service.DeleteSealedHistory(ctx, f.c, box, f.personal.Address)
	expectError(t, "send member deletes", err, domain.ErrPermissionDenied)
	_, err = f.service.DeleteSealedHistory(ctx, f.d, box, f.personal.Address)
	expectError(t, "stranger deletes", err, ErrMailboxNotFound)
	_, err = f.service.DeleteSealedHistory(ctx, f.admin, box, "other@mail.example")
	expectError(t, "wrong confirmation", err, ErrMailboxAccessInvalid)
	var stored int64
	f.repo.(*repository).db.Model(&MailMessagePO{}).Where("mailbox_id = ?", box).Count(&stored)
	if stored != 2 {
		t.Fatalf("messages after a refused deletion = %d; want 2", stored)
	}

	router := accessRouter(f.service, f.admin)
	recorder, body := serve(router, http.MethodDelete, fmt.Sprintf("/mailboxes/%d/sealed-messages", box),
		`{"confirm_address":"`+strings.ToUpper(f.personal.Address)+`"}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["deleted_messages"] != float64(1) || data["deleted_broadcasts"] != float64(0) {
		t.Fatalf("DELETE sealed-messages = %d %v", recorder.Code, body)
	}
	f.repo.(*repository).db.Model(&MailMessagePO{}).Where("mailbox_id = ?", box).Count(&stored)
	if stored != 1 {
		t.Fatalf("messages after deletion = %d; want 1", stored)
	}
	var attachments int64
	f.repo.(*repository).db.Model(&MailAttachmentPO{}).Where("message_id = ?", f.personalMessage.ID).Count(&attachments)
	if attachments != 0 {
		t.Fatalf("attachments of deleted sealed messages = %d", attachments)
	}
	if _, err := f.service.GetMessage(ctx, f.c, box, after.ID); err != nil {
		t.Fatalf("the unsealed message is gone: %v", err)
	}
	listed := listedMailbox(t, f.service, f.admin, box)
	if listed.SealedBefore == nil {
		t.Fatal("deleting the sealed history moved the boundary")
	}
	deleted := eventsOfType(accessEventsOf(t, f, box), domain.MailboxAccessSealedDeleted)
	if len(deleted) != 1 || deleted[0].Detail["deleted_messages"] != float64(1) || deleted[0].SubjectUserID != nil {
		t.Fatalf("sealed_deleted events = %+v", deleted)
	}
}

// Sealed broadcasts still running are canceled before the sealed history goes; broadcasts after
// the boundary stay.
func TestDeleteSealedHistoryCancelsRunningBroadcast(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	f.service.now = time.Now
	templateID, listID := broadcastScene(t, f)
	input := BroadcastInput{Name: "Personal news", MailboxID: f.personal.ID, TemplateID: templateID, ListID: listID}
	running, err := f.service.CreateBroadcast(ctx, f.b, input)
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	if _, err = f.service.SendBroadcast(ctx, f.b, running.ID, nil); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}
	f.leave(t, f.b, tick())
	f.handOver(t, f.personal.ID)
	later, err := f.service.CreateBroadcast(ctx, f.c, input)
	if err != nil {
		t.Fatalf("successor CreateBroadcast: %v", err)
	}

	// The sealed broadcast is hidden from the successor and the admin.
	_, err = f.service.GetBroadcast(ctx, f.c, running.ID)
	expectError(t, "sealed GetBroadcast", err, ErrMailBroadcastNotFound)
	_, _, err = f.service.ListBroadcastRecipients(ctx, f.c, running.ID, "", 1, 15)
	expectError(t, "sealed recipients", err, ErrMailBroadcastNotFound)
	listed, total, err := f.service.ListBroadcasts(ctx, f.c, 1, 15)
	if err != nil || total != 1 || len(listed) != 1 || listed[0].ID != later.ID {
		t.Fatalf("successor broadcasts = %v (%d), %v; want only the later one", listed, total, err)
	}
	// The handover already canceled the sealed broadcast; pausing it reveals nothing.
	if _, visible, pauseErr := f.service.PauseBroadcast(ctx, f.admin, running.ID); !errors.Is(pauseErr, ErrMailBroadcastNotEditable) || visible {
		t.Fatalf("pause sealed = visible %v, %v; want not editable and hidden", visible, pauseErr)
	}

	counts, err := f.service.DeleteSealedHistory(ctx, f.admin, f.personal.ID, f.personal.Address)
	if err != nil || counts.Broadcasts != 1 {
		t.Fatalf("DeleteSealedHistory = %+v, %v; want one broadcast", counts, err)
	}
	db := f.repo.(*repository).db
	var broadcasts, recipients int64
	db.Model(&MailBroadcastPO{}).Where("id = ?", running.ID).Count(&broadcasts)
	db.Model(&MailBroadcastRecipientPO{}).Where("broadcast_id = ?", running.ID).Count(&recipients)
	if broadcasts != 0 || recipients != 0 {
		t.Fatalf("sealed broadcast rows left: %d broadcasts, %d recipients", broadcasts, recipients)
	}
	if _, err := f.service.GetBroadcast(ctx, f.c, later.ID); err != nil {
		t.Fatalf("the later broadcast is gone: %v", err)
	}
}

// Review focus 2: a delivery event arriving after handover for a message sent before the
// boundary is stored but stays invisible and uncounted.
func TestEventsOfSealedMessagesStayHidden(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	notifier := &fakeNotifier{}
	f.service.notifier = notifier
	f.transport.result = mailtransport.Result{MessageID: "prov-sealed@mail.example"}
	sent, _, err := f.service.SendMessage(ctx, f.b, f.personal.ID, sendInput())
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	f.leave(t, f.b, tick())
	f.handOver(t, f.personal.ID)
	notifier.sent = nil

	raw := deliveryEventBody("bounced", "evt-sealed", "<prov-sealed@mail.example>", "x@example.com")
	now := f.service.now()
	_, _, recorded, err := f.service.RecordDeliveryEvent(ctx, DeliveryEvent{Raw: raw, Signature: signedHeader(now, deliveryEventScope, raw)})
	if err != nil || !recorded {
		t.Fatalf("RecordDeliveryEvent = %v, recorded %v", err, recorded)
	}

	_, err = f.service.GetMessage(ctx, f.c, f.personal.ID, sent.ID)
	expectError(t, "sealed GetMessage", err, ErrMailMessageNotFound)
	log, _, err := f.service.ListLog(ctx, f.c, domain.MailLogFilter{MailboxID: f.personal.ID})
	if err != nil || len(log) != 0 {
		t.Fatalf("successor log = %v, %v; want nothing", log, err)
	}
	stats, err := f.service.Stats(ctx, f.c, 1)
	if err != nil || stats[0].Sent != 0 {
		t.Fatalf("successor stats = %+v, %v; want nothing sent", stats, err)
	}
	// The notification describes a sealed message, which the webhook guard never publishes.
	if len(notifier.sent) != 1 || notifier.sent[0].SealedBefore == nil ||
		!notifier.sent[0].Message.CreatedAt.Before(*notifier.sent[0].SealedBefore) ||
		notifier.sent[0].MailboxType != domain.MailboxTypeShared {
		t.Fatalf("notifications = %+v; want one for a sealed message", notifier.sent)
	}
}

// A successor can neither delete a sealed message by id nor replay its idempotency key.
func TestSuccessorCannotDeleteOrReplaySealedMessages(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	input := sendInput()
	input.IdempotencyKey = "order-42"
	sent, _, err := f.service.SendMessage(ctx, f.b, f.personal.ID, input)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	f.leave(t, f.b, tick())
	f.handOver(t, f.personal.ID)
	sends := len(f.transport.sent)

	err = f.service.DeleteMessage(ctx, f.c, f.personal.ID, sent.ID)
	expectError(t, "delete sealed", err, ErrMailMessageNotFound)
	replayed, created, err := f.service.SendMessage(ctx, f.c, f.personal.ID, input)
	expectError(t, "replay sealed key", err, ErrMailIdempotencyConflict)
	if replayed != nil || created || len(f.transport.sent) != sends {
		t.Fatalf("replay = %+v, created %v, sends %d -> %d; want no data and nothing sent", replayed, created, sends, len(f.transport.sent))
	}
	other := input
	other.Subject = "Different"
	_, _, err = f.service.SendMessage(ctx, f.c, f.personal.ID, other)
	expectError(t, "reuse sealed key with other content", err, ErrMailIdempotencyConflict)
}

// Review focus 3: two admins hand over the same mailbox at once; exactly one wins.
func TestConcurrentHandoverHasOneWinner(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	repo := f.repo.(*repository)
	locked, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- repo.InTransaction(ctx, func(tx context.Context) error {
			if _, err := repo.HandOverMailbox(tx, f.personal.ID, time.Now().UTC()); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	second := make(chan error, 1)
	go func() {
		_, err := f.service.HandOverMailbox(ctx, f.admin, f.personal.ID, accessC)
		second <- err
	}()
	select {
	case err := <-second:
		close(release)
		t.Fatalf("the second handover finished (%v) while the first was uncommitted", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first handover: %v", err)
	}
	expectError(t, "second handover", <-second, ErrMailboxAccessConflict)

	// Racing service calls: one succeeds, the other conflicts.
	f.leave(t, f.d, tick())
	other, err := f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{LocalPart: "c", Type: domain.MailboxTypePersonal, OwnerUserID: accessC})
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.service.HandOverMailbox(ctx, f.admin, other.ID, accessB)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case strings.Contains(err.Error(), ErrMailboxAccessConflict.Error()):
			conflicts++
		default:
			t.Fatalf("racing handover: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("racing handovers: %d wins, %d conflicts; want 1 and 1", wins, conflicts)
	}
}

// The mailbox starter subscribes to the organization starter's membership events.
func TestMembershipEventsReachTheMailboxStarter(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	bus := events.NewEventBus()
	NewHandler(f.service).RegisterEvents(bus)
	f.memberships.absent[accessB] = true

	if err := bus.Publish(ctx, domain.NewOrganizationMemberRemovedEvent(accessOrg, accessB, f.b.MembershipID, time.Now())); err != nil {
		t.Fatalf("Publish removed: %v", err)
	}
	if listed := listedMailbox(t, f.service, f.admin, f.personal.ID); listed.OwnerLeftAt == nil {
		t.Fatal("the removed event did not reach the mailbox starter")
	}
	if err := bus.Publish(ctx, domain.NewOrganizationMemberJoinedEvent(accessOrg, accessB, rejoinedMembership, time.Now())); err != nil {
		t.Fatalf("Publish joined: %v", err)
	}
	if listed := listedMailbox(t, f.service, f.admin, f.personal.ID); listed.OwnerLeftAt != nil {
		t.Fatal("the joined event did not reach the mailbox starter")
	}

	// A failing handler is logged and never fails the membership change.
	f.service.repo = &failingRepository{fakeRepository: f.repo.(*fakeRepository)}
	if err := bus.Publish(ctx, domain.NewOrganizationMemberRemovedEvent(accessOrg, accessB, f.b.MembershipID, time.Now())); err != nil {
		t.Fatalf("a failing handler failed the publication: %v", err)
	}
}

// failingRepository fails every transaction.
type failingRepository struct {
	*fakeRepository
}

func (r *failingRepository) InTransaction(context.Context, func(context.Context) error) error {
	return fmt.Errorf("database unavailable")
}

// Ruling R1: handover cancels the mailbox's broadcasts still running from before the boundary,
// so a sealed broadcast never sends new mail the successor could read. A broadcast created after
// the boundary keeps running.
func TestHandoverCancelsSealedRunningBroadcasts(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	f.service.now = time.Now
	f.transport.result = mailtransport.Result{Delivered: []string{"ann@example.com"}}
	templateID, listID := broadcastScene(t, f)
	input := BroadcastInput{Name: "Personal news", MailboxID: f.personal.ID, TemplateID: templateID, ListID: listID}
	start := func(name string) *domain.MailBroadcast {
		t.Helper()
		input.Name = name
		created, err := f.service.CreateBroadcast(ctx, f.b, input)
		if err != nil {
			t.Fatalf("CreateBroadcast %s: %v", name, err)
		}
		started, err := f.service.SendBroadcast(ctx, f.b, created.ID, nil)
		if err != nil {
			t.Fatalf("SendBroadcast %s: %v", name, err)
		}
		return started
	}
	sealed := start("sealed")
	leftAt := tick()
	// Created after the departure time the event carries, before the event arrived.
	unsealed := start("unsealed")
	f.leave(t, f.b, leftAt)

	f.handOver(t, f.personal.ID)

	db := f.repo.(*repository).db
	var stored MailBroadcastPO
	if err := db.First(&stored, sealed.ID).Error; err != nil {
		t.Fatalf("load sealed broadcast: %v", err)
	}
	if stored.Status != string(domain.MailBroadcastCanceled) || stored.FinishedAt == nil {
		t.Fatalf("sealed broadcast status = %s, finished %v; want canceled", stored.Status, stored.FinishedAt)
	}
	var pending int64
	db.Model(&MailBroadcastRecipientPO{}).Where("broadcast_id = ? AND status = ?", sealed.ID, string(domain.MailRecipientPending)).Count(&pending)
	if pending != 0 {
		t.Fatalf("sealed broadcast keeps %d pending recipients", pending)
	}
	if running, err := f.service.GetBroadcast(ctx, f.c, unsealed.ID); err != nil || running.Status != domain.MailBroadcastSending {
		t.Fatalf("unsealed broadcast = %+v, %v; want still sending", running, err)
	}

	// Dispatching sends only the unsealed broadcast's mail.
	f.transport.sent = nil
	if _, err := f.service.DispatchBroadcasts(ctx, 50); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	if len(f.transport.sent) != 1 {
		t.Fatalf("dispatch sent %d messages; want only the unsealed broadcast's one", len(f.transport.sent))
	}
	var sealedSent int64
	db.Model(&MailBroadcastRecipientPO{}).Where("broadcast_id = ? AND status = ?", sealed.ID, string(domain.MailRecipientSent)).Count(&sealedSent)
	if sealedSent != 0 {
		t.Fatalf("sealed broadcast sent %d recipients after handover", sealedSent)
	}
	if done, err := f.service.GetBroadcast(ctx, f.c, unsealed.ID); err != nil || done.Counts.Sent != 1 {
		t.Fatalf("unsealed broadcast = %+v, %v; want its recipient sent", done, err)
	}
}

// rejoinAtTransaction lets the former owner rejoin the moment the handover transaction begins:
// after every read the handover makes before it.
type rejoinAtTransaction struct {
	domain.MailboxRepository
	rejoin func()
}

func (r *rejoinAtTransaction) InTransaction(ctx context.Context, fn func(context.Context) error) error {
	if r.rejoin != nil {
		r.rejoin()
		r.rejoin = nil
	}
	return r.MailboxRepository.InTransaction(ctx, fn)
}

// Fix 4: the former owner's membership is read inside the handover transaction, so an owner who
// rejoined before the handover commits is sealed at the handover, never at their old departure.
func TestHandoverRereadsFormerOwnerInsideTransaction(t *testing.T) {
	f := newPostgresAccessFixture(t)
	f.service.now = time.Now
	leftAt := tick()
	f.leave(t, f.b, leftAt)
	f.service.repo = &rejoinAtTransaction{MailboxRepository: f.service.repo, rejoin: func() { delete(f.memberships.absent, accessB) }}

	before := time.Now().UTC().Truncate(time.Microsecond)
	handed := f.handOver(t, f.personal.ID)
	if handed.SealedBefore == nil || handed.SealedBefore.Before(before) {
		t.Fatalf("sealed_before = %v; want the handover time (at or after %v), not the departure %v",
			handed.SealedBefore, before, leftAt)
	}
}
