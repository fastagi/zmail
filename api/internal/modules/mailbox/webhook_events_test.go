package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

func TestWebhookCatalogListsTheStarterAndMailEvents(t *testing.T) {
	catalog, err := NewWebhookCatalog()
	if err != nil {
		t.Fatalf("NewWebhookCatalog: %v", err)
	}
	types := catalog.Types()
	want := append([]string{"webhook.test"}, mailWebhookTypes...)
	slices.Sort(types)
	slices.Sort(want)
	if !slices.Equal(types, want) {
		t.Fatalf("catalog types = %v; want %v", types, want)
	}
}

func TestMailWebhookPayloadValidationIsStrict(t *testing.T) {
	valid := `{"message_id":1,"mailbox_id":2,"mailbox_address":"a@mail.example","direction":"outbound",` +
		`"from":"a@mail.example","to":["b@example.com"],"recipient":"","reason":""}`
	if err := validateMailWebhookPayload(json.RawMessage(valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	for name, payload := range map[string]string{
		"unknown field": `{"message_id":1,"mailbox_id":2,"mailbox_address":"a@mail.example","direction":"outbound","from":"a@mail.example","to":[],"recipient":"","reason":"","subject":"secret"}`,
		"no message":    `{"message_id":0,"mailbox_id":2,"mailbox_address":"a@mail.example","direction":"outbound","from":"a@mail.example","to":[],"recipient":"","reason":""}`,
		"bad direction": `{"message_id":1,"mailbox_id":2,"mailbox_address":"a@mail.example","direction":"sideways","from":"a@mail.example","to":[],"recipient":"","reason":""}`,
		"missing to":    `{"message_id":1,"mailbox_id":2,"mailbox_address":"a@mail.example","direction":"outbound","from":"a@mail.example","recipient":"","reason":""}`,
		"trailing data": valid + `{}`,
		"not an object": `[]`,
	} {
		if err := validateMailWebhookPayload(json.RawMessage(payload)); err == nil {
			t.Errorf("%s: payload accepted", name)
		}
	}
}

// recordingPublisher captures what the notifier hands to the webhook starter.
type recordingPublisher struct {
	events []domain.WebhookEvent
}

func (p *recordingPublisher) PublishWebhook(_ context.Context, event domain.WebhookEvent) (*domain.WebhookReceipt, error) {
	p.events = append(p.events, event)
	return &domain.WebhookReceipt{}, nil
}

func webhookConfig(selected bool) *config.Config {
	cfg := &config.Config{}
	if selected {
		cfg.Starters.Optional = []string{config.StarterWebhook}
	}
	return cfg
}

func TestWebhookNotifierPublishesAValidEventWithoutMailContent(t *testing.T) {
	publisher := &recordingPublisher{}
	notifier := NewWebhookNotifier(webhookConfig(true), publisher)
	now := time.Unix(1_800_000_000, 0).UTC()
	notifier.now = func() time.Time { return now }
	message := &domain.MailMessage{
		ID: 42, MailboxID: 7, Direction: domain.MailDirectionOutbound, FromAddress: "hello@mail.example",
		To: []string{"bob@example.com"}, Subject: "Private subject", TextBody: "Private body",
	}
	notification := MailNotification{
		Type: WebhookMailBounced, OrganizationID: 3, MailboxAddress: "hello@mail.example", Message: message,
		MailboxType: domain.MailboxTypeShared, WebhooksEnabled: true,
		Recipient: "bob@example.com", Reason: "permanent_failure", OccurredAt: now.Add(-time.Minute), Key: "event:abc/+=",
	}
	if err := notifier.Notify(context.Background(), notification); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if len(publisher.events) != 1 {
		t.Fatalf("published %d events; want 1", len(publisher.events))
	}
	event := publisher.events[0]
	if event.OrganizationID != 3 || event.Source != "mailbox" || event.Type != WebhookMailBounced ||
		!event.OccurredAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("event = %+v", event)
	}
	if !regexp.MustCompile(`^m42:bounced:[0-9a-f]{24}$`).MatchString(event.EventID) {
		t.Fatalf("event id = %q", event.EventID)
	}
	if err := validateMailWebhookPayload(event.Data); err != nil {
		t.Fatalf("published payload fails its own catalog validation: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(event.Data, &fields); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if len(fields) != 8 || fields["recipient"] != "bob@example.com" || fields["reason"] != "permanent_failure" {
		t.Fatalf("payload = %s", event.Data)
	}

	// The same notification gets the same event id, so a repeat is recognized as a duplicate.
	if err := notifier.Notify(context.Background(), notification); err != nil {
		t.Fatalf("Notify again: %v", err)
	}
	if publisher.events[1].EventID != event.EventID {
		t.Fatalf("event id changed between identical notifications")
	}

	// A provider timestamp outside the accepted window is replaced by the current time.
	notification.OccurredAt = now.AddDate(0, -3, 0)
	if err := notifier.Notify(context.Background(), notification); err != nil {
		t.Fatalf("Notify stale: %v", err)
	}
	if !publisher.events[2].OccurredAt.Equal(now) {
		t.Fatalf("stale occurred_at = %v; want %v", publisher.events[2].OccurredAt, now)
	}
}

func TestWebhookNotifierIsSilentWithoutTheStarterOrAnOrganization(t *testing.T) {
	message := &domain.MailMessage{ID: 1, MailboxID: 1, Direction: domain.MailDirectionInbound, FromAddress: "a@b.test"}
	publisher := &recordingPublisher{}
	off := NewWebhookNotifier(webhookConfig(false), publisher)
	if err := off.Notify(context.Background(), MailNotification{
		Type: WebhookMailReceived, OrganizationID: 1, Message: message, MailboxType: domain.MailboxTypeShared, WebhooksEnabled: true,
	}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	on := NewWebhookNotifier(webhookConfig(true), publisher)
	if err := on.Notify(context.Background(), MailNotification{
		Type: WebhookMailReceived, Message: message, MailboxType: domain.MailboxTypeShared, WebhooksEnabled: true,
	}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if len(publisher.events) != 0 {
		t.Fatalf("published %d events; want none", len(publisher.events))
	}
}

func TestSendPublishesSentAndPerRecipientOutcomes(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 5, "hello")
	fixture.transport.result = mailtransport.Result{
		MessageID: "prov-1@mail.example", Delivered: []string{"bob@example.com"},
		Bounced: []string{"gone@example.net"}, Queued: []string{"later@example.org"},
	}
	message, _, err := fixture.service.SendMessage(context.Background(), as(5), mailbox.ID, SendInput{
		To: []string{"bob@example.com", "gone@example.net", "later@example.org"}, Subject: "s", Text: "b",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	types := fixture.notifier.types()
	slices.Sort(types)
	want := []string{WebhookMailBounced, WebhookMailDelivered, WebhookMailSent}
	if !slices.Equal(types, want) {
		t.Fatalf("notifications = %v; want %v", types, want)
	}
	for _, notification := range fixture.notifier.sent {
		if notification.OrganizationID != 5 || notification.MailboxAddress != "hello@mail.example" || notification.Message.ID != message.ID {
			t.Fatalf("notification = %+v", notification)
		}
	}
}

func TestFailedSendPublishesFailed(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 5, "hello")
	fixture.transport.err = &mailtransport.Error{Code: "transport.unavailable"}
	_, _, err := fixture.service.SendMessage(context.Background(), as(5), mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if !errors.Is(err, ErrMailDeliveryFailed) {
		t.Fatalf("SendMessage error = %v", err)
	}
	if len(fixture.notifier.sent) != 1 || fixture.notifier.sent[0].Type != WebhookMailFailed ||
		fixture.notifier.sent[0].Reason != "transport.unavailable" {
		t.Fatalf("notifications = %+v", fixture.notifier.sent)
	}
}

func TestInboundPublishesReceivedOnceAcrossRetries(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.mailbox(t, 5, "hello")
	raw := inboundRaw("in-hook@sender.test")
	for range 2 {
		if _, _, err := fixture.service.ReceiveInbound(context.Background(), fixture.delivery("hello@mail.example", raw)); err != nil {
			t.Fatalf("ReceiveInbound: %v", err)
		}
	}
	if got := fixture.notifier.types(); !slices.Equal(got, []string{WebhookMailReceived}) {
		t.Fatalf("notifications = %v; want one received", got)
	}
}

func TestDeliveryEventPublishesOnceAcrossRedeliveries(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 5, "hello")
	fixture.transport.result = mailtransport.Result{MessageID: "prov-9@mail.example", Queued: []string{"bob@example.com"}}
	if _, _, err := fixture.service.SendMessage(ctx, as(5), mailbox.ID, SendInput{To: []string{"bob@example.com"}, Subject: "s", Text: "b"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	fixture.notifier.sent = nil
	raw := deliveryEventBody("bounced", "evt-9", "prov-9@mail.example", "bob@example.com")
	for range 2 {
		if _, err := fixture.service.RecordDeliveryEvent(ctx, fixture.signedEvent(raw)); err != nil {
			t.Fatalf("RecordDeliveryEvent: %v", err)
		}
	}
	if len(fixture.notifier.sent) != 1 {
		t.Fatalf("notifications = %+v; want exactly one", fixture.notifier.sent)
	}
	got := fixture.notifier.sent[0]
	if got.Type != WebhookMailBounced || got.OrganizationID != 5 || got.MailboxAddress != "hello@mail.example" ||
		got.Recipient != "bob@example.com" || got.Key != "event:evt-9" {
		t.Fatalf("notification = %+v", got)
	}
}

// A webhook that cannot be queued must never lose the record of real mail.
func TestMailStateIsRecordedWhenPublishingFails(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 5, "hello")
	fixture.transport.result = mailtransport.Result{MessageID: "prov-f@mail.example", Delivered: []string{"bob@example.com"}}
	fixture.notifier.failures = 1

	message, _, err := fixture.service.SendMessage(ctx, as(5), mailbox.ID, SendInput{To: []string{"bob@example.com"}, Subject: "s", Text: "b"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	stored := fixture.repo.messages[message.ID]
	if stored.Status != domain.MailStatusSent || stored.MessageID != "prov-f@mail.example" || stored.LastEvent != domain.MailEventDelivered {
		t.Fatalf("stored message = %+v", stored)
	}
	if len(fixture.repo.events[message.ID]) != 2 {
		t.Fatalf("events = %+v; want accepted and delivered exactly once", fixture.repo.events[message.ID])
	}
	if len(fixture.notifier.sent) != 0 {
		t.Fatalf("notifications = %+v; want none after the failure", fixture.notifier.sent)
	}

	fixture.notifier.failures = 1
	inbound, created, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", inboundRaw("in-f@sender.test")))
	if err != nil || !created {
		t.Fatalf("ReceiveInbound = %v, created %v", err, created)
	}
	if _, ok := fixture.repo.messages[inbound.ID]; !ok {
		t.Fatalf("inbound message was not stored")
	}
}
