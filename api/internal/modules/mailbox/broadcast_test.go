package mailbox

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// stubBroadcasts stands in for the broadcast store in tests that do not use broadcasts.
type stubBroadcasts struct {
	domain.MailBroadcastRepository
}

func (*stubBroadcasts) SuppressContactForMessage(context.Context, uint, string) error { return nil }

// scriptedTransport delivers to everyone except the addresses it bounces.
type scriptedTransport struct {
	sent    []mailtransport.Message
	bounces map[string]bool
}

func (t *scriptedTransport) Send(_ context.Context, mail mailtransport.Message) (mailtransport.Result, error) {
	t.sent = append(t.sent, mail)
	if t.bounces[mail.To[0]] {
		return mailtransport.Result{MessageID: "b-" + mail.To[0], Bounced: mail.To},
			&mailtransport.Error{Code: "transport.permanent_bounce", Permanent: true}
	}
	return mailtransport.Result{MessageID: "m-" + mail.To[0], Delivered: mail.To}, nil
}

type broadcastFixture struct {
	service   *service
	repo      *repository
	transport *scriptedTransport
	mailbox   *domain.Mailbox
	template  *domain.MailTemplate
	list      *domain.MailContactList
	now       time.Time
}

func newBroadcastFixture(t *testing.T, dailyLimit int) *broadcastFixture {
	t.Helper()
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	fixture := &broadcastFixture{
		repo:      repo,
		transport: &scriptedTransport{bounces: map[string]bool{}},
		mailbox:   createMailbox(t, repo, "news@mail.example"),
		now:       time.Now().UTC(),
	}
	fixture.service = NewService(repo, &fakeMemberships{}, repo, repo, repo, nil, nil, fixture.transport, &fakeNotifier{}, Settings{
		Domain:           "mail.example",
		InboundSecret:    testInboundSecret,
		InboundTolerance: 5 * time.Minute,
		DailySendLimit:   dailyLimit,
		PublicURL:        "https://app.example",
	})
	fixture.service.now = func() time.Time { return fixture.now }
	var err error
	fixture.template, err = fixture.service.CreateTemplate(ctx, 1, testCreator, TemplateInput{
		Name:    "News",
		Subject: "News for {{first_name|you}}",
		HTML:    "<html><body><p>Hi {{first_name|there}}, plan {{plan|free}}.</p></body></html>",
		Text:    "Hi {{first_name|there}}, plan {{plan|free}}.",
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	fixture.list, err = fixture.service.CreateContactList(ctx, 1, "Newsletter")
	if err != nil {
		t.Fatalf("CreateContactList: %v", err)
	}
	return fixture
}

func (f *broadcastFixture) contact(t *testing.T, email, firstName string, status domain.MailContactStatus, inList bool) *domain.MailContact {
	t.Helper()
	input := ContactInput{Email: email, FirstName: firstName, Properties: map[string]string{"plan": "pro"}, Status: status}
	if status == domain.MailContactSuppressed {
		input.Status = ""
	}
	if inList {
		input.ListIDs = []uint{f.list.ID}
	}
	contact, err := f.service.CreateContact(context.Background(), 1, input)
	if err != nil {
		t.Fatalf("CreateContact(%s): %v", email, err)
	}
	if status == domain.MailContactSuppressed {
		if err := f.repo.SetContactStatus(context.Background(), 1, contact.ID, status, "bounced"); err != nil {
			t.Fatalf("SetContactStatus: %v", err)
		}
	}
	return contact
}

func (f *broadcastFixture) draft(t *testing.T) *domain.MailBroadcast {
	t.Helper()
	broadcast, err := f.service.CreateBroadcast(context.Background(), as(1), BroadcastInput{
		Name: " November   news ", MailboxID: f.mailbox.ID, TemplateID: f.template.ID, ListID: f.list.ID,
		UnsubscribeLabel: "Leave this list",
	})
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	return broadcast
}

func TestBroadcastSendsToSubscribedListMembers(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	ann := fixture.contact(t, "ann@example.com", "Ann", domain.MailContactSubscribed, true)
	fixture.contact(t, "bob@example.com", "Bob", domain.MailContactUnsubscribed, true)
	fixture.contact(t, "carol@example.com", "Carol", domain.MailContactSubscribed, false)
	fixture.contact(t, "gone@example.com", "", domain.MailContactSuppressed, true)
	dave := fixture.contact(t, "dave@example.com", "Dave", domain.MailContactSubscribed, true)
	erin := fixture.contact(t, "erin@example.com", "", domain.MailContactSubscribed, true)
	fixture.transport.bounces["erin@example.com"] = true

	broadcast := fixture.draft(t)
	if broadcast.Name != "November news" || broadcast.Status != domain.MailBroadcastDraft {
		t.Fatalf("draft = %+v", broadcast)
	}
	started, err := fixture.service.SendBroadcast(ctx, as(1), broadcast.ID, nil)
	if err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}
	if started.Status != domain.MailBroadcastSending || started.Counts.Total != 3 || started.Counts.Pending != 3 {
		t.Fatalf("started = %+v; want ann, dave, and erin pending", started)
	}
	// Editing the template now does not change the broadcast.
	changed := TemplateInput{Name: "News", Subject: "Changed", Text: "Changed"}
	if _, err = fixture.service.UpdateTemplate(ctx, 1, fixture.template.ID, changed); err != nil {
		t.Fatalf("UpdateTemplate: %v", err)
	}
	if _, err = fixture.service.UpdateBroadcast(ctx, as(1), broadcast.ID, BroadcastInput{Name: "x", MailboxID: fixture.mailbox.ID, TemplateID: fixture.template.ID, ListID: fixture.list.ID}); !errors.Is(err, ErrMailBroadcastNotEditable) {
		t.Fatalf("edit while sending error = %v", err)
	}
	// Dave unsubscribes through the link after the broadcast started.
	if err = fixture.service.Unsubscribe(ctx, fixture.service.unsubscribeToken(1, dave.ID)); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	processed, err := fixture.service.DispatchBroadcasts(ctx, 10)
	if err != nil || processed != 3 {
		t.Fatalf("DispatchBroadcasts = %d, %v; want 3", processed, err)
	}
	if len(fixture.transport.sent) != 2 {
		t.Fatalf("provider calls = %d; want ann and erin", len(fixture.transport.sent))
	}
	toAnn := fixture.transport.sent[0]
	if toAnn.To[0] != "ann@example.com" || toAnn.Subject != "News for Ann" || !strings.Contains(toAnn.HTMLBody, "Hi Ann, plan pro.") {
		t.Fatalf("message to ann = %+v", toAnn)
	}
	link := "https://app.example/unsubscribe/" + fixture.service.unsubscribeToken(1, ann.ID)
	if !strings.Contains(toAnn.HTMLBody, `<a href="`+link+`" style="color:#71717a;">Leave this list</a></p></body>`) ||
		!strings.Contains(toAnn.TextBody, "Leave this list: "+link) {
		t.Fatalf("unsubscribe footer missing:\n%s\n%s", toAnn.HTMLBody, toAnn.TextBody)
	}
	if toAnn.Headers["List-Unsubscribe"] != "<https://app.example/api/mail-unsubscribes/"+fixture.service.unsubscribeToken(1, ann.ID)+">" ||
		toAnn.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("headers = %v", toAnn.Headers)
	}

	finished, err := fixture.service.GetBroadcast(ctx, as(1), broadcast.ID)
	if err != nil || finished.Status != domain.MailBroadcastSent || finished.FinishedAt == nil ||
		finished.Counts.Sent != 1 || finished.Counts.Failed != 1 || finished.Counts.Skipped != 1 {
		t.Fatalf("finished = %+v, %v", finished, err)
	}
	recipients, total, err := fixture.service.ListBroadcastRecipients(ctx, as(1), broadcast.ID, domain.MailRecipientFailed, 1, 10)
	if err != nil || total != 1 || recipients[0].Email != "erin@example.com" || recipients[0].MessageID == 0 ||
		recipients[0].FailureCode != "transport.permanent_bounce" {
		t.Fatalf("failed recipients = %+v (%d), %v", recipients, total, err)
	}
	erinNow, _ := fixture.repo.FindContact(ctx, 1, erin.ID)
	if erinNow.Status != domain.MailContactSuppressed || erinNow.StatusReason != "bounced" {
		t.Fatalf("erin after a bounce = %+v; want suppressed", erinNow)
	}

	// A later complaint from the provider about ann's message suppresses ann.
	raw := deliveryEventBody("complained", "evt-complaint", "m-ann@example.com", "ann@example.com")
	if _, _, recorded, err := fixture.service.RecordDeliveryEvent(ctx, DeliveryEvent{
		Raw: raw, Signature: signedHeader(time.Now(), deliveryEventScope, raw),
	}); err != nil || !recorded {
		t.Fatalf("RecordDeliveryEvent = %v, %v", recorded, err)
	}
	annNow, _ := fixture.repo.FindContact(ctx, 1, ann.ID)
	if annNow.Status != domain.MailContactSuppressed || annNow.StatusReason != "complained" {
		t.Fatalf("ann after a complaint = %+v", annNow)
	}

	if processed, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil || processed != 0 {
		t.Fatalf("second dispatch = %d, %v; want nothing left", processed, err)
	}
	if err := fixture.service.DeleteBroadcast(ctx, as(1), broadcast.ID); err != nil {
		t.Fatalf("DeleteBroadcast after sending: %v", err)
	}
}

func TestBroadcastPausesAtTheSendLimitAndResumes(t *testing.T) {
	fixture := newBroadcastFixture(t, 1)
	ctx := context.Background()
	fixture.contact(t, "a@example.com", "A", domain.MailContactSubscribed, true)
	fixture.contact(t, "b@example.com", "B", domain.MailContactSubscribed, true)
	broadcast := fixture.draft(t)
	if _, err := fixture.service.SendBroadcast(ctx, as(1), broadcast.ID, nil); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}
	processed, err := fixture.service.DispatchBroadcasts(ctx, 10)
	if err != nil || processed != 1 {
		t.Fatalf("DispatchBroadcasts = %d, %v; want 1 before the limit", processed, err)
	}
	paused, _ := fixture.service.GetBroadcast(ctx, as(1), broadcast.ID)
	if paused.Status != domain.MailBroadcastSending || paused.StopReason != "send_limit_reached" || paused.Counts.Pending != 1 {
		t.Fatalf("paused = %+v", paused)
	}

	// A day later the window has room again.
	fixture.now = fixture.now.Add(25 * time.Hour)
	if processed, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil || processed != 1 {
		t.Fatalf("resumed dispatch = %d, %v", processed, err)
	}
	done, _ := fixture.service.GetBroadcast(ctx, as(1), broadcast.ID)
	if done.Status != domain.MailBroadcastSent || done.StopReason != "" || done.Counts.Sent != 2 {
		t.Fatalf("done = %+v", done)
	}
}

func TestBroadcastScheduleCancelAndValidation(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	fixture.contact(t, "a@example.com", "A", domain.MailContactSubscribed, true)

	scheduled := fixture.draft(t)
	at := fixture.now.Add(time.Hour)
	started, err := fixture.service.SendBroadcast(ctx, as(1), scheduled.ID, &at)
	if err != nil || started.Status != domain.MailBroadcastScheduled || started.ScheduledAt == nil {
		t.Fatalf("scheduled = %+v, %v", started, err)
	}
	if processed, _ := fixture.service.DispatchBroadcasts(ctx, 10); processed != 0 {
		t.Fatalf("a scheduled broadcast was sent early")
	}
	if err = fixture.service.DeleteBroadcast(ctx, as(1), scheduled.ID); !errors.Is(err, ErrMailBroadcastNotEditable) {
		t.Fatalf("delete while scheduled error = %v", err)
	}
	canceled, _, err := fixture.service.CancelBroadcast(ctx, as(1), scheduled.ID)
	if err != nil || canceled.Status != domain.MailBroadcastCanceled || canceled.Counts.Canceled != 1 {
		t.Fatalf("canceled = %+v, %v", canceled, err)
	}
	fixture.now = fixture.now.Add(2 * time.Hour)
	if processed, _ := fixture.service.DispatchBroadcasts(ctx, 10); processed != 0 || len(fixture.transport.sent) != 0 {
		t.Fatalf("a canceled broadcast was sent")
	}
	if _, _, err = fixture.service.CancelBroadcast(ctx, as(1), scheduled.ID); !errors.Is(err, ErrMailBroadcastNotEditable) {
		t.Fatalf("second cancel error = %v", err)
	}

	far := fixture.now.Add(400 * 24 * time.Hour)
	if _, err = fixture.service.SendBroadcast(ctx, as(1), fixture.draft(t).ID, &far); !errors.Is(err, ErrMailBroadcastInvalid) {
		t.Fatalf("far schedule error = %v", err)
	}
	empty, _ := fixture.service.CreateContactList(ctx, 1, "Empty")
	noOne, err := fixture.service.CreateBroadcast(ctx, as(1), BroadcastInput{
		Name: "Nobody", MailboxID: fixture.mailbox.ID, TemplateID: fixture.template.ID, ListID: empty.ID,
	})
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	if _, err = fixture.service.SendBroadcast(ctx, as(1), noOne.ID, nil); !errors.Is(err, ErrMailBroadcastNoRecipients) {
		t.Fatalf("empty list error = %v", err)
	}
	still, _ := fixture.service.GetBroadcast(ctx, as(1), noOne.ID)
	if still.Status != domain.MailBroadcastDraft {
		t.Fatalf("a refused send changed the draft: %+v", still)
	}

	for name, input := range map[string]BroadcastInput{
		"no name":       {MailboxID: fixture.mailbox.ID, TemplateID: fixture.template.ID, ListID: fixture.list.ID},
		"long label":    {Name: "x", MailboxID: fixture.mailbox.ID, TemplateID: fixture.template.ID, ListID: fixture.list.ID, UnsubscribeLabel: strings.Repeat("l", 51)},
		"missing parts": {Name: "x"},
	} {
		if _, err = fixture.service.CreateBroadcast(ctx, as(1), input); !errors.Is(err, ErrMailBroadcastInvalid) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if _, err = fixture.service.CreateBroadcast(ctx, as(2), BroadcastInput{
		Name: "x", MailboxID: fixture.mailbox.ID, TemplateID: fixture.template.ID, ListID: fixture.list.ID,
	}); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("another organization's mailbox error = %v", err)
	}
	if _, err = fixture.service.GetBroadcast(ctx, as(2), scheduled.ID); !errors.Is(err, ErrMailBroadcastNotFound) {
		t.Fatalf("another organization's broadcast error = %v", err)
	}
	listed, total, err := fixture.service.ListBroadcasts(ctx, as(1), 1, 10)
	if err != nil || total != 3 || listed[0].ID != noOne.ID || listed[0].HTMLBody != "" {
		t.Fatalf("ListBroadcasts = %d, %v; want newest first without snapshots", total, err)
	}
}

func TestBroadcastsNeedUnsubscribeSettings(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	fixture.contact(t, "a@example.com", "A", domain.MailContactSubscribed, true)
	fixture.service.settings.PublicURL = ""
	if _, err := fixture.service.SendBroadcast(context.Background(), as(1), fixture.draft(t).ID, nil); !errors.Is(err, ErrMailBroadcastDisabled) {
		t.Fatalf("send without a public URL error = %v", err)
	}
}

func TestUnsubscribeLinks(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	ann := fixture.contact(t, "ann@example.com", "Ann", domain.MailContactSubscribed, true)
	gone := fixture.contact(t, "gone@example.com", "", domain.MailContactSuppressed, true)
	token := fixture.service.unsubscribeToken(1, ann.ID)

	for _, bad := range []string{"", "1.2", token + "x", strings.Replace(token, "1.", "2.", 1), "1.999.AAAA", strings.Repeat("a", 201)} {
		if err := fixture.service.Unsubscribe(ctx, bad); !errors.Is(err, ErrMailUnsubscribeInvalid) {
			t.Errorf("token %q error = %v", bad, err)
		}
	}
	for range 2 {
		if err := fixture.service.Unsubscribe(ctx, token); err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}
	}
	annNow, _ := fixture.repo.FindContact(ctx, 1, ann.ID)
	if annNow.Status != domain.MailContactUnsubscribed || annNow.StatusReason != "unsubscribe_link" {
		t.Fatalf("ann = %+v", annNow)
	}
	if err := fixture.service.Unsubscribe(ctx, fixture.service.unsubscribeToken(1, gone.ID)); err != nil {
		t.Fatalf("Unsubscribe suppressed: %v", err)
	}
	goneNow, _ := fixture.repo.FindContact(ctx, 1, gone.ID)
	if goneNow.Status != domain.MailContactSuppressed {
		t.Fatalf("a suppressed contact was unsuppressed: %+v", goneNow)
	}
	if err := fixture.service.DeleteContact(ctx, 1, ann.ID); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if err := fixture.service.Unsubscribe(ctx, token); err != nil {
		t.Fatalf("a deleted contact's link must still answer success: %v", err)
	}
}

func TestUnsubscribeFooterPlacement(t *testing.T) {
	html, text := withUnsubscribeFooter("<p>x</p>", "", "https://a.example/u/1?a=<b>", "")
	if !strings.HasSuffix(html, `<a href="https://a.example/u/1?a=&lt;b&gt;" style="color:#71717a;">Unsubscribe</a></p>`) || text != "" {
		t.Fatalf("html-only footer = %q / %q", html, text)
	}
	_, text = withUnsubscribeFooter("", "Body\n", "https://a.example/u", "Leave")
	if text != "Body\n\nLeave: https://a.example/u\n" {
		t.Fatalf("text footer = %q", text)
	}
	if !slices.Contains([]string{"bounced"}, refusalReason(&domain.MailMessage{Events: []domain.MailEvent{{Type: domain.MailEventBounced}}})) {
		t.Fatal("a bounce at send time is not a refusal")
	}
}
