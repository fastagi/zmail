package mailbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// publishedTypes lists the webhook types handed to the webhook starter, sorted.
func (p *recordingPublisher) publishedTypes() []string {
	types := make([]string, 0, len(p.events))
	for _, event := range p.events {
		types = append(types, event.Type)
	}
	slices.Sort(types)
	return types
}

// AC-9: personal mailboxes never publish; a shared mailbox publishes only with its switch on and
// only for messages created at or after its seal boundary.
func TestWebhookOnlyForEnabledSharedMailbox(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	publisher := &recordingPublisher{}
	f.service.notifier = NewWebhookNotifier(webhookConfig(true), publisher)
	outcomes := mailtransport.Result{Delivered: []string{"bob@example.com"}, Bounced: []string{"gone@example.net"}}
	input := SendInput{To: []string{"bob@example.com", "gone@example.net"}, Subject: "s", Text: "b"}
	everything := []string{WebhookMailBounced, WebhookMailDelivered, WebhookMailSent}

	// Personal: never.
	outcomes.MessageID = "prov-personal@mail.example"
	f.transport.result = outcomes
	if _, _, err := f.service.SendMessage(ctx, f.b, f.personal.ID, input); err != nil {
		t.Fatalf("send from personal: %v", err)
	}
	// Shared, switch off by default: nothing.
	if f.team.WebhooksEnabled {
		t.Fatalf("team@ starts with webhooks on")
	}
	outcomes.MessageID = "prov-team-off@mail.example"
	f.transport.result = outcomes
	if _, _, err := f.service.SendMessage(ctx, f.b, f.team.ID, input); err != nil {
		t.Fatalf("send from team@ (off): %v", err)
	}
	if len(publisher.events) != 0 {
		t.Fatalf("published %v with no switch on; want nothing", publisher.publishedTypes())
	}

	// Shared, switch on: sent, delivered, and bounced.
	if _, err := f.service.SetMailboxWebhooks(ctx, f.admin, f.team.ID, true); err != nil {
		t.Fatalf("SetMailboxWebhooks: %v", err)
	}
	outcomes.MessageID = "prov-team-on@mail.example"
	f.transport.result = outcomes
	if _, _, err := f.service.SendMessage(ctx, f.b, f.team.ID, input); err != nil {
		t.Fatalf("send from team@ (on): %v", err)
	}
	if got := publisher.publishedTypes(); !slices.Equal(got, everything) {
		t.Fatalf("published %v; want %v", got, everything)
	}
	// A provider bounce arriving later for a message sent while the switch was on publishes too.
	publisher.events = nil
	recordBounce(t, f, "evt-team-on", "<prov-team-on@mail.example>")
	if got := publisher.publishedTypes(); !slices.Equal(got, []string{WebhookMailBounced}) {
		t.Fatalf("late bounce published %v; want one bounce", got)
	}

	// A late bounce for a sealed message publishes nothing, even with the switch on; a message
	// sent after the handover publishes.
	f.leave(t, f.b, tick())
	f.handOver(t, f.personal.ID)
	if _, err := f.service.SetMailboxWebhooks(ctx, f.admin, f.personal.ID, true); err != nil {
		t.Fatalf("SetMailboxWebhooks after handover: %v", err)
	}
	publisher.events = nil
	recordBounce(t, f, "evt-sealed", "<prov-personal@mail.example>")
	if len(publisher.events) != 0 {
		t.Fatalf("late bounce of a sealed message published %v; want nothing", publisher.publishedTypes())
	}
	tick()
	outcomes.MessageID = "prov-handed@mail.example"
	f.transport.result = outcomes
	if _, _, err := f.service.SendMessage(ctx, f.c, f.personal.ID, input); err != nil {
		t.Fatalf("send from the handed-over mailbox: %v", err)
	}
	if got := publisher.publishedTypes(); !slices.Equal(got, everything) {
		t.Fatalf("after handover published %v; want %v", got, everything)
	}

	// Switched off again: nothing.
	if _, err := f.service.SetMailboxWebhooks(ctx, f.admin, f.team.ID, false); err != nil {
		t.Fatalf("SetMailboxWebhooks off: %v", err)
	}
	publisher.events = nil
	recordBounce(t, f, "evt-team-off-again", "<prov-team-on@mail.example>")
	if len(publisher.events) != 0 {
		t.Fatalf("published %v after switching off; want nothing", publisher.publishedTypes())
	}
}

// AC-9, inbound: one received message each for a personal mailbox, a shared mailbox with
// webhooks off, and a shared mailbox with webhooks on publishes exactly one mail.message.received,
// for the last.
func TestInboundWebhookOnlyForEnabledSharedMailbox(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	publisher := &recordingPublisher{}
	f.service.notifier = NewWebhookNotifier(webhookConfig(true), publisher)
	f.service.now = time.Now
	enabled, err := f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{
		LocalPart: "ops", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessC, Permission: domain.MailboxPermissionView}},
	})
	if err != nil {
		t.Fatalf("create ops@: %v", err)
	}
	if enabled, err = f.service.SetMailboxWebhooks(ctx, f.admin, enabled.ID, true); err != nil {
		t.Fatalf("SetMailboxWebhooks: %v", err)
	}
	if f.team.WebhooksEnabled {
		t.Fatal("team@ starts with webhooks on")
	}

	f.receiveSigned(t, f.personal, "in-personal@sender.test")
	f.receiveSigned(t, f.team, "in-team-off@sender.test")
	received := f.receiveSigned(t, enabled, "in-ops-on@sender.test")

	if len(publisher.events) != 1 || publisher.events[0].Type != WebhookMailReceived {
		t.Fatalf("published %v; want one %s", publisher.publishedTypes(), WebhookMailReceived)
	}
	var payload mailWebhookPayload
	if err := json.Unmarshal(publisher.events[0].Data, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.MailboxID != enabled.ID || payload.MessageID != received.ID || payload.Direction != string(domain.MailDirectionInbound) {
		t.Fatalf("payload = %+v; want message %d of ops@ (%d)", payload, received.ID, enabled.ID)
	}
}

func recordBounce(t *testing.T, f *accessFixture, eventID, providerID string) {
	t.Helper()
	raw := deliveryEventBody("bounced", eventID, providerID, "gone@example.net")
	now := f.service.now()
	_, _, recorded, err := f.service.RecordDeliveryEvent(context.Background(),
		DeliveryEvent{Raw: raw, Signature: signedHeader(now, deliveryEventScope, raw)})
	if err != nil || !recorded {
		t.Fatalf("RecordDeliveryEvent = %v, recorded %v", err, recorded)
	}
}

// The guard itself: a notification of a personal mailbox, a switched-off mailbox, or a sealed
// message is never handed to the webhook starter.
func TestWebhookNotifierGuard(t *testing.T) {
	boundary := time.Unix(1_800_000_000, 0).UTC()
	message := &domain.MailMessage{
		ID: 1, MailboxID: 1, Direction: domain.MailDirectionInbound, FromAddress: "a@b.test",
		CreatedAt: boundary,
	}
	publishable := MailNotification{
		Type: WebhookMailReceived, OrganizationID: 1, MailboxAddress: "team@mail.example", Message: message,
		MailboxType: domain.MailboxTypeShared, WebhooksEnabled: true, SealedBefore: &boundary,
	}
	cases := map[string]func(MailNotification) MailNotification{
		"personal": func(n MailNotification) MailNotification { n.MailboxType = domain.MailboxTypePersonal; return n },
		"switch off": func(n MailNotification) MailNotification {
			n.WebhooksEnabled = false
			return n
		},
		"sealed": func(n MailNotification) MailNotification {
			sealed := *n.Message
			sealed.CreatedAt = boundary.Add(-time.Nanosecond)
			n.Message = &sealed
			return n
		},
	}
	for name, change := range cases {
		publisher := &recordingPublisher{}
		notifier := NewWebhookNotifier(webhookConfig(true), publisher)
		if err := notifier.Notify(context.Background(), change(publishable)); err != nil || len(publisher.events) != 0 {
			t.Fatalf("%s: Notify = %v, published %d; want nothing", name, err, len(publisher.events))
		}
	}
	publisher := &recordingPublisher{}
	notifier := NewWebhookNotifier(webhookConfig(true), publisher)
	if err := notifier.Notify(context.Background(), publishable); err != nil || len(publisher.events) != 1 {
		t.Fatalf("message at the boundary: Notify = %v, published %d; want 1", err, len(publisher.events))
	}
}

func TestWebhookSwitchRules(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()

	_, err := f.service.SetMailboxWebhooks(ctx, f.admin, f.personal.ID, true)
	expectError(t, "admin switches a personal mailbox", err, ErrMailboxAccessConflict)
	_, err = f.service.SetMailboxWebhooks(ctx, f.b, f.personal.ID, true)
	expectError(t, "owner switches their personal mailbox", err, ErrMailboxAccessConflict)
	_, err = f.service.SetMailboxWebhooks(ctx, f.d, f.team.ID, true)
	expectError(t, "D switches a mailbox they cannot see", err, ErrMailboxNotFound)
	_, err = f.service.SetMailboxWebhooks(ctx, f.c, f.team.ID, true)
	expectError(t, "view member switches", err, domain.ErrPermissionDenied)
	_, err = f.service.SetMailboxWebhooks(ctx, f.b, f.team.ID, true)
	expectError(t, "send member switches", err, domain.ErrPermissionDenied)
	if events := eventsOfType(accessEventsOf(t, f, f.team.ID), domain.MailboxAccessWebhooksChanged); len(events) != 0 {
		t.Fatalf("refused switches wrote %d events", len(events))
	}

	// A manage holder may switch, and the history records who did it.
	if _, err = f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessC, domain.MailboxPermissionManage); err != nil {
		t.Fatalf("SetMailboxMember: %v", err)
	}
	mailbox, err := f.service.SetMailboxWebhooks(ctx, f.c, f.team.ID, true)
	if err != nil || !mailbox.WebhooksEnabled || !mailbox.Access.ManageMembers {
		t.Fatalf("manage holder switches on = %+v, %v", mailbox, err)
	}
	// Setting the current value writes nothing.
	if _, err = f.service.SetMailboxWebhooks(ctx, f.admin, f.team.ID, true); err != nil {
		t.Fatalf("switch on again: %v", err)
	}
	mailbox, err = f.service.SetMailboxWebhooks(ctx, f.admin, f.team.ID, false)
	if err != nil || mailbox.WebhooksEnabled {
		t.Fatalf("admin switches off = %+v, %v", mailbox, err)
	}
	changes := eventsOfType(accessEventsOf(t, f, f.team.ID), domain.MailboxAccessWebhooksChanged)
	if len(changes) != 2 {
		t.Fatalf("webhooks_changed events = %d; want 2", len(changes))
	}
	off, on := changes[0], changes[1] // newest first
	if !sameID(on.ActorUserID, accessC) || on.SubjectUserID != nil || on.Detail["enabled"] != true ||
		!sameID(off.ActorUserID, accessAdmin) || off.SubjectUserID != nil || off.Detail["enabled"] != false {
		t.Fatalf("webhooks_changed = %+v / %+v", on, off)
	}
	if listedMailbox(t, f.service, f.c, f.team.ID).WebhooksEnabled {
		t.Fatalf("listed team@ still has webhooks on")
	}
}

func TestWebhookSwitchRoute(t *testing.T) {
	f := newAccessFixture(t)
	admin := accessRouter(f.service, f.admin)
	path := fmt.Sprintf("/mailboxes/%d/webhook-setting", f.team.ID)

	if recorder, _ := serve(admin, http.MethodPut, path, `{}`); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing enabled = %d; want 422", recorder.Code)
	}
	recorder, body := serve(admin, http.MethodPut, path, `{"enabled":true}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("switch on = %d %s; want 200", recorder.Code, recorder.Body)
	}
	if data, _ := body["data"].(map[string]any); data["webhooks_enabled"] != true || data["type"] != "shared" {
		t.Fatalf("switch on body = %v", body)
	}
	personal := fmt.Sprintf("/mailboxes/%d/webhook-setting", f.personal.ID)
	if recorder, _ = serve(admin, http.MethodPut, personal, `{"enabled":true}`); recorder.Code != http.StatusConflict {
		t.Fatalf("personal = %d; want 409", recorder.Code)
	}
	if recorder, _ = serve(accessRouter(f.service, f.c), http.MethodPut, path, `{"enabled":false}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("view member = %d; want 403", recorder.Code)
	}
}
