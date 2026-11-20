package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// withInbound lets a fixture accept signed delivery events and send broadcasts.
func withInbound(settings *Settings) {
	settings.InboundSecret = testInboundSecret
	settings.InboundTolerance = 5 * time.Minute
	settings.PublicURL = "https://app.example"
}

// eventAt is a provider event that happened at a given time.
func eventAt(eventType, eventID, providerMessageID, recipient string, at time.Time) []byte {
	return bytes.Replace(deliveryEventBody(eventType, eventID, providerMessageID, recipient),
		[]byte("2027-01-15T08:00:05Z"), []byte(at.UTC().Format(time.RFC3339Nano)), 1)
}

func (f *txFixture) deliver(t *testing.T, raw []byte) DeliveryEventResult {
	t.Helper()
	result, err := f.service.RecordDeliveryEvent(context.Background(), DeliveryEvent{
		Raw: raw, Signature: signedHeader(f.clock, deliveryEventScope, raw),
	})
	if err != nil {
		t.Fatalf("RecordDeliveryEvent: %v", err)
	}
	return result
}

// sendLive sends a live message the provider accepts under the given provider id.
func (f *txFixture) sendLive(t *testing.T, key, to, providerID string) *domain.TransactionalMessage {
	t.Helper()
	f.transport.result = mailtransport.Result{MessageID: providerID}
	message, _ := f.send(t, liveActor(), codeInput(key, to, "482913"))
	stored := f.row(t, message.ID)
	if stored.Status != string(domain.TransactionalStatusSent) || stored.ProviderMessageID == nil || *stored.ProviderMessageID != providerID {
		t.Fatalf("message %d = %s, provider id %v; want sent as %s", message.ID, stored.Status, stored.ProviderMessageID, providerID)
	}
	return message
}

type suppressionRow struct {
	Email  string
	Reason string
	Source string
}

func (f *txFixture) suppressions(t *testing.T) []suppressionRow {
	t.Helper()
	var rows []suppressionRow
	if err := f.db.Raw(`SELECT email, reason, source FROM mail_suppressions WHERE organization_id = 10 ORDER BY id`).Scan(&rows).Error; err != nil {
		t.Fatalf("read suppressions: %v", err)
	}
	return rows
}

func (f *txFixture) notificationsOf(eventType string) []TransactionalNotification {
	var found []TransactionalNotification
	for _, notification := range f.notifier.transactional {
		if notification.Type == eventType {
			found = append(found, notification)
		}
	}
	return found
}

func TestDeliveryEventRoutesToTransactional(t *testing.T) {
	f := newTxHandlerFixture(t, withInbound)
	message := f.sendLive(t, "route-1", "lilei@example.com", "prov-route@cf.example")

	post := func(raw []byte) (*httptest.ResponseRecorder, map[string]any) {
		request := httptest.NewRequest(http.MethodPost, "/v1/mail-delivery-events", bytes.NewReader(raw))
		request.Header.Set(InboundSignatureHeader, signedHeader(f.clock, deliveryEventScope, raw))
		recorder := httptest.NewRecorder()
		f.engine.ServeHTTP(recorder, request)
		body := map[string]any{}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		return recorder, body
	}
	raw := deliveryEventBody("delivered", "evt-route-1", "<prov-route@cf.example>", "LiLei@Example.com")
	recorder, body := post(raw)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("event = %d %s", recorder.Code, recorder.Body.String())
	}
	data, _ := body["data"].(map[string]any)
	if data["message_id"] != float64(message.ID) || data["message_kind"] != "transactional" ||
		data["type"] != "delivered" || data["duplicate"] != false || len(data) != 4 {
		t.Fatalf("data = %v", data)
	}
	read, readBody := f.request(http.MethodGet, "/v1/transactional-messages/"+strconv.Itoa(int(message.ID)), "live", "", "")
	readData, _ := readBody["data"].(map[string]any)
	if read.Code != http.StatusOK || readData["last_event"] != "delivered" {
		t.Fatalf("read = %d %v", read.Code, readData["last_event"])
	}
	delivered := f.notificationsOf(WebhookEmailDelivered)
	// The send call reported no recipient, so the only delivery webhook is the event's.
	if len(delivered) != 1 || delivered[0].Key != "event:evt-route-1" || delivered[0].Message.ID != message.ID ||
		delivered[0].Message.LastEvent != domain.MailEventDelivered {
		t.Fatalf("delivered notifications = %+v", delivered)
	}

	// The same event again is a duplicate.
	if recorder, body = post(raw); recorder.Code != http.StatusOK {
		t.Fatalf("duplicate = %d %s", recorder.Code, recorder.Body.String())
	}
	if data, _ = body["data"].(map[string]any); data["duplicate"] != true || data["message_kind"] != "transactional" {
		t.Fatalf("duplicate data = %v", data)
	}
	// No message, mailbox or transactional, has this provider id.
	recorder, body = post(deliveryEventBody("delivered", "evt-route-2", "<nobody@cf.example>", "a@example.com"))
	if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailMessageNotFound {
		t.Fatalf("unknown message = %d %v", recorder.Code, body["error_code"])
	}
}

func TestDuplicateTransactionalEventPublishesOnce(t *testing.T) {
	f := newTxFixture(t, withInbound)
	message := f.sendLive(t, "dup-1", "lilei@example.com", "prov-dup@cf.example")
	raw := deliveryEventBody("bounced", "evt-dup", "prov-dup@cf.example", "lilei@example.com")

	first := f.deliver(t, raw)
	if !first.Recorded || first.Kind != DeliveryEventKindTransactional || first.MessageID != message.ID ||
		first.Event.Type != domain.MailEventBounced {
		t.Fatalf("first = %+v", first)
	}
	if second := f.deliver(t, raw); second.Recorded || second.Kind != DeliveryEventKindTransactional {
		t.Fatalf("second = %+v; want a duplicate", second)
	}
	bounced := f.notificationsOf(WebhookEmailBounced)
	if len(bounced) != 1 || bounced[0].BounceType != bounceTypePermanent || bounced[0].Key != "event:evt-dup" ||
		bounced[0].Reason != "5.1.1 permanent_failure" {
		t.Fatalf("bounced notifications = %+v", bounced)
	}
	var events int64
	if err := f.db.Raw(`SELECT count(*) FROM transactional_message_events WHERE message_id = ? AND type = 'bounced'`, message.ID).
		Scan(&events).Error; err != nil || events != 1 {
		t.Fatalf("bounced events = %d, %v; want 1", events, err)
	}
	if rows := f.suppressions(t); len(rows) != 1 {
		t.Fatalf("suppressions = %+v; want one", rows)
	}
}

func TestOutOfOrderEventsKeepSeverity(t *testing.T) {
	f := newTxFixture(t, withInbound)
	message := f.sendLive(t, "order-1", "lilei@example.com", "prov-order@cf.example")

	// The bounce happened last but arrives first; the earlier delivery arrives after it.
	f.deliver(t, eventAt("bounced", "evt-order-b", "prov-order@cf.example", "lilei@example.com", f.clock.Add(-time.Minute)))
	late := f.deliver(t, eventAt("delivered", "evt-order-d", "prov-order@cf.example", "lilei@example.com", f.clock.Add(-2*time.Minute)))
	if !late.Recorded {
		t.Fatalf("late delivery = %+v; want recorded", late)
	}
	if last := f.row(t, message.ID).LastEvent; last == nil || *last != string(domain.MailEventBounced) {
		t.Fatalf("last_event = %v; want bounced", last)
	}

	// A deferral followed by a delivery is delivered.
	other := f.sendLive(t, "order-2", "hanmei@example.com", "prov-order-2@cf.example")
	f.deliver(t, eventAt("deferred", "evt-order-2a", "prov-order-2@cf.example", "hanmei@example.com", f.clock.Add(-2*time.Minute)))
	f.deliver(t, eventAt("delivered", "evt-order-2b", "prov-order-2@cf.example", "hanmei@example.com", f.clock.Add(-time.Minute)))
	if last := f.row(t, other.ID).LastEvent; last == nil || *last != string(domain.MailEventDelivered) {
		t.Fatalf("last_event = %v; want delivered", last)
	}
}

// broadcastLane is a shared mailbox of organization 10 with a broadcast template and a list.
type broadcastLane struct {
	viewer   domain.OrganizationContext
	mailbox  *domain.Mailbox
	template *domain.MailTemplate
	list     *domain.MailContactList
}

func (f *txFixture) broadcastLane(t *testing.T) *broadcastLane {
	t.Helper()
	ctx := context.Background()
	lane := &broadcastLane{viewer: viewerOf(10, txUser, domain.OrganizationRoleOwner)}
	if err := f.db.Exec(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at)
		VALUES (?, 10, ?, 'owner', now(), now())`, lane.viewer.MembershipID, txUser).Error; err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	lane.mailbox = &domain.Mailbox{OrganizationID: 10, Address: "news@mail.example", DisplayName: "News", Type: domain.MailboxTypeShared}
	if err := f.repo.CreateMailbox(ctx, lane.mailbox, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	addMember(t, f.repo, lane.mailbox.ID, lane.viewer, domain.MailboxPermissionManage)
	var err error
	if lane.template, err = f.service.CreateTemplate(ctx, lane.viewer, TemplateInput{
		Name: "News", Subject: "News", HTML: "<p>News</p>", Text: "News",
	}); err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if lane.list, err = f.service.CreateContactList(ctx, 10, "Newsletter"); err != nil {
		t.Fatalf("CreateContactList: %v", err)
	}
	return lane
}

func (f *txFixture) contactIn(t *testing.T, lane *broadcastLane, email string) *domain.MailContact {
	t.Helper()
	contact, err := f.service.CreateContact(context.Background(), 10, ContactInput{Email: email, ListIDs: []uint{lane.list.ID}})
	if err != nil {
		t.Fatalf("CreateContact(%s): %v", email, err)
	}
	return contact
}

// runBroadcast sends a broadcast to the lane's list and returns its recipients.
func (f *txFixture) runBroadcast(t *testing.T, lane *broadcastLane) []*domain.MailBroadcastRecipient {
	t.Helper()
	ctx := context.Background()
	broadcast, err := f.service.CreateBroadcast(ctx, lane.viewer, BroadcastInput{
		Name: "News", MailboxID: lane.mailbox.ID, TemplateID: lane.template.ID, ListID: lane.list.ID,
	})
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	if _, err = f.service.SendBroadcast(ctx, lane.viewer, broadcast.ID, nil); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}
	if _, err = f.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	recipients, _, err := f.service.ListBroadcastRecipients(ctx, lane.viewer, broadcast.ID, "", 1, 10)
	if err != nil {
		t.Fatalf("ListBroadcastRecipients: %v", err)
	}
	return recipients
}

func TestPermanentBounceSuppressesAcrossLanes(t *testing.T) {
	f := newTxFixture(t, withInbound)
	ctx := context.Background()
	lane := f.broadcastLane(t)
	lilei := f.contactIn(t, lane, "LiLei@Example.com")

	f.sendLive(t, "lanes-1", " LiLei@Example.COM ", "prov-lanes@cf.example")
	result := f.deliver(t, deliveryEventBody("bounced", "evt-lanes", "<prov-lanes@cf.example>", "lilei@example.com"))
	if !result.Recorded || result.Kind != DeliveryEventKindTransactional {
		t.Fatalf("bounce = %+v", result)
	}
	if rows := f.suppressions(t); len(rows) != 1 ||
		rows[0] != (suppressionRow{Email: "lilei@example.com", Reason: "bounced", Source: "transactional"}) {
		t.Fatalf("suppressions = %+v", rows)
	}

	// The next transactional message to the address is suppressed and never reaches the provider.
	calls := len(f.transport.sent)
	next, created := f.send(t, liveActor(), codeInput("lanes-2", "lilei@EXAMPLE.com", "111111"))
	if !created || next.Status != domain.TransactionalStatusSuppressed || len(f.transport.sent) != calls {
		t.Fatalf("next = %s (created %v), provider calls %d -> %d", next.Status, created, calls, len(f.transport.sent))
	}

	// A broadcast skips the address but leaves the contact as it is.
	recipients := f.runBroadcast(t, lane)
	if len(recipients) != 1 || recipients[0].Status != domain.MailRecipientSkipped ||
		recipients[0].FailureCode != "contact_suppressed" || recipients[0].MessageID != 0 {
		t.Fatalf("recipients = %+v; want lilei skipped as contact_suppressed", recipients)
	}
	if len(f.transport.sent) != calls {
		t.Fatalf("the broadcast called the provider for a suppressed address")
	}
	stored, err := f.repo.FindContact(ctx, 10, lilei.ID)
	if err != nil || stored.Status != domain.MailContactSubscribed {
		t.Fatalf("contact = %+v, %v; want still subscribed", stored, err)
	}
}

func TestBroadcastComplaintJoinsSuppressionList(t *testing.T) {
	f := newTxFixture(t, withInbound)
	lane := f.broadcastLane(t)
	ann := f.contactIn(t, lane, "Ann@Example.com")
	f.transport.result = mailtransport.Result{MessageID: "prov-ann@cf.example", Delivered: []string{"ann@example.com"}}
	recipients := f.runBroadcast(t, lane)
	if len(recipients) != 1 || recipients[0].Status != domain.MailRecipientSent {
		t.Fatalf("recipients = %+v", recipients)
	}

	// A deferral of a broadcast message adds nothing.
	f.deliver(t, deliveryEventBody("deferred", "evt-ann-0", "prov-ann@cf.example", "ann@example.com"))
	if rows := f.suppressions(t); len(rows) != 0 {
		t.Fatalf("suppressions after a deferral = %+v", rows)
	}
	result := f.deliver(t, deliveryEventBody("complained", "evt-ann", "prov-ann@cf.example", "Ann@Example.com"))
	if !result.Recorded || result.Kind != DeliveryEventKindMailbox || result.MessageID != recipients[0].MessageID {
		t.Fatalf("complaint = %+v", result)
	}
	if rows := f.suppressions(t); len(rows) != 1 ||
		rows[0] != (suppressionRow{Email: "ann@example.com", Reason: "complained", Source: "broadcast"}) {
		t.Fatalf("suppressions = %+v", rows)
	}
	// The existing contact rule still applies.
	if stored, _ := f.repo.FindContact(context.Background(), 10, ann.ID); stored.Status != domain.MailContactSuppressed {
		t.Fatalf("contact = %+v; want suppressed", stored)
	}
	// A transactional message to ann is now suppressed too.
	if next, _ := f.send(t, liveActor(), codeInput("ann-1", "ann@example.com", "111111")); next.Status != domain.TransactionalStatusSuppressed {
		t.Fatalf("transactional to ann = %s; want suppressed", next.Status)
	}
}

func TestDeferredAndRejectedDoNotSuppress(t *testing.T) {
	f := newTxFixture(t, withInbound)
	message := f.sendLive(t, "soft-1", "lilei@example.com", "prov-soft@cf.example")

	f.deliver(t, deliveryEventBody("deferred", "evt-soft-1", "prov-soft@cf.example", "lilei@example.com"))
	f.deliver(t, deliveryEventBody("rejected", "evt-soft-2", "prov-soft@cf.example", "lilei@example.com"))
	f.deliver(t, deliveryEventBody("failed", "evt-soft-3", "prov-soft@cf.example", "lilei@example.com"))
	if rows := f.suppressions(t); len(rows) != 0 {
		t.Fatalf("suppressions = %+v; want none", rows)
	}
	if bounced := f.notificationsOf(WebhookEmailBounced); len(bounced) != 1 || bounced[0].BounceType != bounceTypeTemporary ||
		bounced[0].Key != "event:evt-soft-1" {
		t.Fatalf("bounced notifications = %+v; want one temporary", bounced)
	}
	if suppressed := f.notificationsOf(WebhookEmailSuppressed); len(suppressed) != 1 || suppressed[0].Key != "event:evt-soft-2" {
		t.Fatalf("suppressed notifications = %+v", suppressed)
	}
	if failed := f.notificationsOf(WebhookEmailFailed); len(failed) != 1 || failed[0].Key != "event:evt-soft-3" {
		t.Fatalf("failed notifications = %+v", failed)
	}
	// Delivery events never change the send status.
	if stored := f.row(t, message.ID); stored.Status != string(domain.TransactionalStatusSent) {
		t.Fatalf("status = %s; want sent", stored.Status)
	}
	if again, _ := f.send(t, liveActor(), codeInput("soft-2", "lilei@example.com", "111111")); again.Status == domain.TransactionalStatusSuppressed {
		t.Fatalf("a deferral or rejection suppressed the address")
	}
}

func TestSimulatedBounceNeverSuppresses(t *testing.T) {
	f := newTxFixture(t, withInbound)
	simulated, _ := f.send(t, liveActor(), codeInput("sim-1", "Bounced+u1@TEST.zmail.im", "111111"))
	complained, _ := f.send(t, testActor(), codeInput("sim-2", "complained@test.zmail.im", "111111"))
	for _, id := range []uint{simulated.ID, complained.ID} {
		if stored := f.row(t, id); stored.Status != string(domain.TransactionalStatusSent) || stored.LastEvent == nil {
			t.Fatalf("simulated message %d = %s / %v", id, stored.Status, stored.LastEvent)
		}
	}
	if len(f.transport.sent) != 0 {
		t.Fatalf("simulated mail reached the provider")
	}
	// Even a provider event matched to a simulated message adds nothing to the list.
	if err := f.db.Exec(`UPDATE transactional_messages SET provider_message_id = 'prov-sim@cf.example' WHERE id = ?`, simulated.ID).Error; err != nil {
		t.Fatalf("tag: %v", err)
	}
	if result := f.deliver(t, deliveryEventBody("bounced", "evt-sim", "prov-sim@cf.example", "bounced+u1@test.zmail.im")); !result.Recorded {
		t.Fatalf("event = %+v", result)
	}
	if rows := f.suppressions(t); len(rows) != 0 {
		t.Fatalf("suppressions = %+v; want none", rows)
	}
	if again, _ := f.send(t, liveActor(), codeInput("sim-3", "bounced+u1@test.zmail.im", "111111")); again.Status == domain.TransactionalStatusSuppressed {
		t.Fatalf("a simulated bounce suppressed the address")
	}
}
