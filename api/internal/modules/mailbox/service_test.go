package mailbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/fastmail/api/internal/domain"
	"github.com/fastagi/fastmail/api/internal/infra/mailtransport"
)

// fakeRepository is an in-memory domain.MailboxRepository for service tests.
type fakeRepository struct {
	mailboxes map[uint]*domain.Mailbox
	messages  map[uint]*domain.MailMessage
	nextID    uint
	// hideIdempotencyLookup simulates a concurrent request that stores its message between
	// this request's lookup and its insert.
	hideIdempotencyLookup bool
	lastLogFilter         domain.MailLogFilter
	lastStatsSince        time.Time
	stats                 []domain.MailDayStats
}

var _ domain.MailboxRepository = (*fakeRepository)(nil)

func newFakeRepository() *fakeRepository {
	return &fakeRepository{mailboxes: map[uint]*domain.Mailbox{}, messages: map[uint]*domain.MailMessage{}}
}

func (r *fakeRepository) id() uint {
	r.nextID++
	return r.nextID
}

func (r *fakeRepository) CreateMailbox(_ context.Context, mailbox *domain.Mailbox, limit int) error {
	owned := 0
	for _, existing := range r.mailboxes {
		if existing.Address == mailbox.Address {
			return ErrMailboxAddressTaken
		}
		if existing.UserID == mailbox.UserID {
			owned++
		}
	}
	if owned >= limit {
		return ErrMailboxLimitReached
	}
	mailbox.ID = r.id()
	stored := *mailbox
	r.mailboxes[mailbox.ID] = &stored
	return nil
}

func (r *fakeRepository) ListMailboxes(_ context.Context, userID uint) ([]*domain.Mailbox, error) {
	var owned []*domain.Mailbox
	for _, mailbox := range r.mailboxes {
		if mailbox.UserID == userID {
			owned = append(owned, mailbox)
		}
	}
	return owned, nil
}

func (r *fakeRepository) FindMailbox(_ context.Context, userID, id uint) (*domain.Mailbox, error) {
	mailbox, ok := r.mailboxes[id]
	if !ok || mailbox.UserID != userID {
		return nil, ErrMailboxNotFound
	}
	return mailbox, nil
}

func (r *fakeRepository) FindMailboxByAddress(_ context.Context, address string) (*domain.Mailbox, error) {
	for _, mailbox := range r.mailboxes {
		if mailbox.Address == address {
			return mailbox, nil
		}
	}
	return nil, ErrMailboxNotFound
}

func (r *fakeRepository) DeleteMailbox(ctx context.Context, userID, id uint) error {
	if _, err := r.FindMailbox(ctx, userID, id); err != nil {
		return err
	}
	delete(r.mailboxes, id)
	return nil
}

func (r *fakeRepository) DeleteMailboxesForUser(_ context.Context, userID uint) error {
	for id, mailbox := range r.mailboxes {
		if mailbox.UserID != userID {
			continue
		}
		for messageID, message := range r.messages {
			if message.MailboxID == id {
				delete(r.messages, messageID)
			}
		}
		delete(r.mailboxes, id)
	}
	return nil
}

func (r *fakeRepository) CreateMessage(_ context.Context, message *domain.MailMessage) (bool, error) {
	if message.IdempotencyKey != "" {
		for _, existing := range r.messages {
			if existing.MailboxID == message.MailboxID && existing.IdempotencyKey == message.IdempotencyKey {
				*message = *existing
				return false, nil
			}
		}
	}
	message.ID = r.id()
	stored := *message
	r.messages[message.ID] = &stored
	return true, nil
}

func (r *fakeRepository) FindMessageByIdempotencyKey(_ context.Context, mailboxID uint, key string) (*domain.MailMessage, error) {
	if r.hideIdempotencyLookup {
		return nil, ErrMailMessageNotFound
	}
	for _, existing := range r.messages {
		if existing.MailboxID == mailboxID && existing.IdempotencyKey == key {
			copied := *existing
			return &copied, nil
		}
	}
	return nil, ErrMailMessageNotFound
}

func (r *fakeRepository) FindOutboundMessageForUser(_ context.Context, userID, id uint) (*domain.MailMessage, error) {
	message, ok := r.messages[id]
	if !ok || message.Direction != domain.MailDirectionOutbound {
		return nil, ErrMailMessageNotFound
	}
	mailbox, ok := r.mailboxes[message.MailboxID]
	if !ok || mailbox.UserID != userID {
		return nil, ErrMailMessageNotFound
	}
	copied := *message
	return &copied, nil
}

func (r *fakeRepository) CreateInboundMessage(ctx context.Context, message *domain.MailMessage) (bool, error) {
	for _, existing := range r.messages {
		if existing.MailboxID == message.MailboxID && existing.DedupeKey == message.DedupeKey {
			*message = *existing
			return false, nil
		}
	}
	_, err := r.CreateMessage(ctx, message)
	return true, err
}

func (r *fakeRepository) UpdateMessageDelivery(_ context.Context, id uint, status domain.MailStatus, messageID, failureCode string) error {
	message, ok := r.messages[id]
	if !ok {
		return ErrMailMessageNotFound
	}
	message.Status, message.MessageID, message.FailureCode = status, messageID, failureCode
	return nil
}

func (r *fakeRepository) ListMessages(_ context.Context, mailboxID uint, filter domain.MailMessageFilter) ([]*domain.MailMessage, int64, error) {
	var listed []*domain.MailMessage
	for _, message := range r.messages {
		if message.MailboxID == mailboxID && (filter.Direction == "" || message.Direction == filter.Direction) {
			listed = append(listed, message)
		}
	}
	return listed, int64(len(listed)), nil
}

func (r *fakeRepository) FindMessage(_ context.Context, mailboxID, id uint) (*domain.MailMessage, error) {
	message, ok := r.messages[id]
	if !ok || message.MailboxID != mailboxID {
		return nil, ErrMailMessageNotFound
	}
	copied := *message
	return &copied, nil
}

func (r *fakeRepository) SetMessageRead(_ context.Context, mailboxID, id uint, readAt *time.Time) error {
	message, ok := r.messages[id]
	if !ok || message.MailboxID != mailboxID {
		return ErrMailMessageNotFound
	}
	message.ReadAt = readAt
	return nil
}

func (r *fakeRepository) DeleteMessage(_ context.Context, mailboxID, id uint) error {
	message, ok := r.messages[id]
	if !ok || message.MailboxID != mailboxID {
		return ErrMailMessageNotFound
	}
	delete(r.messages, id)
	return nil
}

func (r *fakeRepository) CountOutboundSince(_ context.Context, userID uint, _ time.Time) (int64, error) {
	var count int64
	for _, message := range r.messages {
		mailbox, ok := r.mailboxes[message.MailboxID]
		if ok && mailbox.UserID == userID && message.Direction == domain.MailDirectionOutbound {
			count++
		}
	}
	return count, nil
}

func (r *fakeRepository) ListMessagesForUser(_ context.Context, userID uint, filter domain.MailLogFilter) ([]*domain.MailMessage, int64, error) {
	r.lastLogFilter = filter
	var listed []*domain.MailMessage
	for _, message := range r.messages {
		if mailbox, ok := r.mailboxes[message.MailboxID]; ok && mailbox.UserID == userID {
			listed = append(listed, message)
		}
	}
	return listed, int64(len(listed)), nil
}

func (r *fakeRepository) StatsForUser(_ context.Context, _ uint, since time.Time) ([]domain.MailDayStats, error) {
	r.lastStatsSince = since
	return r.stats, nil
}

// fakeTransport records what the service hands to the provider.
type fakeTransport struct {
	sent []mailtransport.Message
	err  error
}

func (t *fakeTransport) Send(_ context.Context, mail mailtransport.Message) error {
	t.sent = append(t.sent, mail)
	return t.err
}

type serviceFixture struct {
	service   *service
	repo      *fakeRepository
	transport *fakeTransport
	now       time.Time
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	fixture := &serviceFixture{
		repo:      newFakeRepository(),
		transport: &fakeTransport{},
		now:       time.Unix(1_800_000_000, 0).UTC(),
	}
	fixture.service = NewService(fixture.repo, fixture.transport, Settings{
		Domain:           "mail.example",
		InboundSecret:    testInboundSecret,
		InboundTolerance: 5 * time.Minute,
		DailySendLimit:   3,
	})
	fixture.service.now = func() time.Time { return fixture.now }
	return fixture
}

func (f *serviceFixture) mailbox(t *testing.T, userID uint, localPart string) *domain.Mailbox {
	t.Helper()
	mailbox, err := f.service.CreateMailbox(context.Background(), userID, localPart, "Test Sender")
	if err != nil {
		t.Fatalf("CreateMailbox(%q): %v", localPart, err)
	}
	return mailbox
}

func TestCreateMailboxBuildsAddressOnTheConfiguredDomain(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 7, "  Hello ")
	if mailbox.Address != "hello@mail.example" || mailbox.UserID != 7 || mailbox.DisplayName != "Test Sender" {
		t.Fatalf("mailbox = %+v", mailbox)
	}

	if _, err := fixture.service.CreateMailbox(context.Background(), 8, "HELLO", ""); !errors.Is(err, ErrMailboxAddressTaken) {
		t.Fatalf("duplicate address error = %v", err)
	}
	if _, err := fixture.service.CreateMailbox(context.Background(), 7, "postmaster", ""); !errors.Is(err, ErrMailboxAddressReserved) {
		t.Fatalf("reserved address error = %v", err)
	}
	if _, err := fixture.service.CreateMailbox(context.Background(), 7, "ok", `Eve <eve@evil.test>`); !errors.Is(err, ErrMailboxAddressInvalid) {
		t.Fatalf("display name error = %v", err)
	}
}

func TestMailboxesAreInvisibleToOtherUsers(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "owner")
	message, _, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	const stranger = 2
	if _, _, err := fixture.service.ListMessages(ctx, stranger, mailbox.ID, domain.MailMessageFilter{}); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("ListMessages error = %v", err)
	}
	if _, err := fixture.service.GetMessage(ctx, stranger, mailbox.ID, message.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("GetMessage error = %v", err)
	}
	if _, _, err := fixture.service.SendMessage(ctx, stranger, mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("SendMessage error = %v", err)
	}
	if _, err := fixture.service.SetMessageRead(ctx, stranger, mailbox.ID, message.ID, false); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("SetMessageRead error = %v", err)
	}
	if err := fixture.service.DeleteMessage(ctx, stranger, mailbox.ID, message.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("DeleteMessage error = %v", err)
	}
	if err := fixture.service.DeleteMailbox(ctx, stranger, mailbox.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("DeleteMailbox error = %v", err)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("transport calls = %d; a stranger's send must not reach the provider", len(fixture.transport.sent))
	}
}

func TestSendMessageRecordsSentAndNormalizesRecipients(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")

	message, _, err := fixture.service.SendMessage(context.Background(), 1, mailbox.ID, SendInput{
		To:        []string{"Bob@Example.com", "bob@example.com"},
		Cc:        []string{"bob@example.com", "amy@example.org"},
		Subject:   "  Greetings ",
		Text:      "Hello",
		InReplyTo: "<prev@example.com>",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if message.Status != domain.MailStatusSent || message.FailureCode != "" || message.ReadAt == nil {
		t.Fatalf("message = %+v", message)
	}
	if fixture.repo.messages[message.ID].Status != domain.MailStatusSent {
		t.Fatalf("stored status = %q", fixture.repo.messages[message.ID].Status)
	}

	sent := fixture.transport.sent[0]
	if sent.FromAddress != "hello@mail.example" || sent.FromName != "Test Sender" {
		t.Fatalf("from = %q %q", sent.FromAddress, sent.FromName)
	}
	if len(sent.To) != 1 || sent.To[0] != "bob@example.com" || len(sent.Cc) != 1 || sent.Cc[0] != "amy@example.org" {
		t.Fatalf("recipients = %v %v; duplicates across to and cc must collapse", sent.To, sent.Cc)
	}
	if sent.Subject != "Greetings" || sent.InReplyTo != "prev@example.com" {
		t.Fatalf("subject = %q, in_reply_to = %q", sent.Subject, sent.InReplyTo)
	}
}

func TestSendMessageReportsProviderFailureInsteadOfSent(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	fixture.transport.err = &mailtransport.Error{Code: "email.sending.error.throttled"}

	message, _, err := fixture.service.SendMessage(context.Background(), 1, mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if !errors.Is(err, ErrMailDeliveryFailed) {
		t.Fatalf("error = %v; want ErrMailDeliveryFailed", err)
	}
	if message == nil || message.Status != domain.MailStatusFailed || message.FailureCode != "email.sending.error.throttled" {
		t.Fatalf("message = %+v", message)
	}
	stored := fixture.repo.messages[message.ID]
	if stored.Status != domain.MailStatusFailed || stored.FailureCode != "email.sending.error.throttled" {
		t.Fatalf("stored = %+v; the failure must be persisted", stored)
	}

	fixture.transport.err = mailtransport.ErrNotConfigured
	_, _, err = fixture.service.SendMessage(context.Background(), 1, mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if !errors.Is(err, ErrMailSenderNotConfigured) {
		t.Fatalf("error = %v; want ErrMailSenderNotConfigured", err)
	}
}

func TestSendMessageRecordsOutcomeAfterTheCallerCancels(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	ctx, cancel := context.WithCancel(context.Background())
	canceling := &cancelingRepository{fakeRepository: fixture.repo}
	fixture.service.repo = canceling
	fixture.service.transport = transportFunc(func(context.Context, mailtransport.Message) error {
		cancel() // the client disconnects while the provider call is in flight
		return nil
	})

	message, _, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if canceling.updateContextErr != nil {
		t.Fatalf("delivery was recorded with a canceled context: %v", canceling.updateContextErr)
	}
	if fixture.repo.messages[message.ID].Status != domain.MailStatusSent {
		t.Fatalf("stored status = %q; want sent", fixture.repo.messages[message.ID].Status)
	}
}

type transportFunc func(context.Context, mailtransport.Message) error

func (f transportFunc) Send(ctx context.Context, mail mailtransport.Message) error {
	return f(ctx, mail)
}

type cancelingRepository struct {
	*fakeRepository
	updateContextErr error
}

func (r *cancelingRepository) UpdateMessageDelivery(ctx context.Context, id uint, status domain.MailStatus, messageID, failureCode string) error {
	r.updateContextErr = ctx.Err()
	return r.fakeRepository.UpdateMessageDelivery(ctx, id, status, messageID, failureCode)
}

func TestSendMessageValidatesInput(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	tooMany := make([]string, 0, domain.MaxMailRecipients+1)
	for i := 0; i <= domain.MaxMailRecipients; i++ {
		tooMany = append(tooMany, "user"+string(rune('a'+i%26))+string(rune('a'+i/26))+"@example.com")
	}

	cases := map[string]SendInput{
		"no recipients":       {Subject: "s", Text: "b"},
		"bad recipient":       {To: []string{"nope"}, Subject: "s", Text: "b"},
		"too many recipients": {To: tooMany, Subject: "s", Text: "b"},
		"empty subject":       {To: []string{"x@example.com"}, Subject: "  ", Text: "b"},
		"header injection":    {To: []string{"x@example.com"}, Subject: "s\r\nBcc: evil@example.com", Text: "b"},
		"no body":             {To: []string{"x@example.com"}, Subject: "s", Text: "  "},
		"bad in_reply_to":     {To: []string{"x@example.com"}, Subject: "s", Text: "b", InReplyTo: "not an id"},
	}
	for name, input := range cases {
		if _, _, err := fixture.service.SendMessage(context.Background(), 1, mailbox.ID, input); !errors.Is(err, ErrMailMessageInvalid) {
			t.Fatalf("%s: error = %v; want ErrMailMessageInvalid", name, err)
		}
	}
	if len(fixture.transport.sent) != 0 || len(fixture.repo.messages) != 0 {
		t.Fatalf("invalid input reached the provider or storage")
	}
}

func TestSendMessageEnforcesTheDailyLimit(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}
	for i := 0; i < 3; i++ {
		if _, _, err := fixture.service.SendMessage(context.Background(), 1, mailbox.ID, input); err != nil {
			t.Fatalf("send %d: %v", i+1, err)
		}
	}
	if _, _, err := fixture.service.SendMessage(context.Background(), 1, mailbox.ID, input); !errors.Is(err, ErrMailSendLimitReached) {
		t.Fatalf("error = %v; want ErrMailSendLimitReached", err)
	}
	if len(fixture.transport.sent) != 3 {
		t.Fatalf("transport calls = %d; want 3", len(fixture.transport.sent))
	}
}

func inboundRaw(messageID string) []byte {
	return crlf(
		"From: Alice <alice@sender.test>",
		"To: hello@mail.example",
		"Subject: Inbound",
		"Message-ID: <"+messageID+">",
		"",
		"Hi from outside.",
	)
}

func (f *serviceFixture) delivery(recipient string, raw []byte) InboundDelivery {
	return InboundDelivery{
		Recipient: recipient,
		Signature: signedHeader(testInboundSecret, f.now, recipient, raw),
		Raw:       raw,
	}
}

func TestReceiveInboundStoresOnceAndDeduplicatesRetries(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	raw := inboundRaw("in-1@sender.test")

	message, created, err := fixture.service.ReceiveInbound(context.Background(), fixture.delivery("Hello@Mail.Example", raw))
	if err != nil || !created {
		t.Fatalf("ReceiveInbound = %v, created %v", err, created)
	}
	if message.MailboxID != mailbox.ID || message.Direction != domain.MailDirectionInbound ||
		message.Status != domain.MailStatusReceived || message.ReadAt != nil {
		t.Fatalf("message = %+v", message)
	}
	if message.FromAddress != "alice@sender.test" || message.Subject != "Inbound" || message.TextBody != "Hi from outside." {
		t.Fatalf("message = %+v", message)
	}

	again, created, err := fixture.service.ReceiveInbound(context.Background(), fixture.delivery("hello@mail.example", raw))
	if err != nil || created || again.ID != message.ID {
		t.Fatalf("retry = id %d, created %v, err %v; want the stored message", again.ID, created, err)
	}
	if len(fixture.repo.messages) != 1 {
		t.Fatalf("stored messages = %d; want 1", len(fixture.repo.messages))
	}
}

func TestReceiveInboundRejectsUntrustedOrUndeliverableMail(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.mailbox(t, 1, "hello")
	raw := inboundRaw("in-2@sender.test")
	ctx := context.Background()

	unsigned := InboundDelivery{Recipient: "hello@mail.example", Signature: "t=1,v1=00", Raw: raw}
	if _, _, err := fixture.service.ReceiveInbound(ctx, unsigned); !errors.Is(err, ErrMailInboundSignature) {
		t.Fatalf("bad signature error = %v", err)
	}
	// An unsigned request for an unknown address gets the same answer, so it cannot probe addresses.
	unsigned.Recipient = "nobody@mail.example"
	if _, _, err := fixture.service.ReceiveInbound(ctx, unsigned); !errors.Is(err, ErrMailInboundSignature) {
		t.Fatalf("unsigned unknown recipient error = %v", err)
	}
	if _, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("nobody@mail.example", raw)); !errors.Is(err, ErrMailRecipientNotFound) {
		t.Fatalf("unknown recipient error = %v", err)
	}
	garbage := []byte("not a message")
	if _, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", garbage)); !errors.Is(err, ErrMailInboundUnparseable) {
		t.Fatalf("garbage error = %v", err)
	}
	oversized := make([]byte, MaxInboundBytes+1)
	if _, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", oversized)); !errors.Is(err, ErrMailInboundTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}

	fixture.service.settings.InboundSecret = ""
	if _, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", raw)); !errors.Is(err, ErrMailInboundDisabled) {
		t.Fatalf("disabled error = %v", err)
	}
	if len(fixture.repo.messages) != 0 {
		t.Fatalf("stored messages = %d; nothing should be stored", len(fixture.repo.messages))
	}
}

func TestSetMessageReadTogglesState(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.mailbox(t, 1, "hello")
	ctx := context.Background()
	message, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", inboundRaw("in-3@sender.test")))
	if err != nil {
		t.Fatalf("ReceiveInbound: %v", err)
	}

	read, err := fixture.service.SetMessageRead(ctx, 1, message.MailboxID, message.ID, true)
	if err != nil || read.ReadAt == nil || !read.ReadAt.Equal(fixture.now) {
		t.Fatalf("read = %+v, err %v", read, err)
	}
	unread, err := fixture.service.SetMessageRead(ctx, 1, message.MailboxID, message.ID, false)
	if err != nil || unread.ReadAt != nil {
		t.Fatalf("unread = %+v, err %v", unread, err)
	}
	if _, err := fixture.service.SetMessageRead(ctx, 1, message.MailboxID, 999, true); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("missing message error = %v", err)
	}
}

func TestAccountDeletionRemovesOnlyThatUsersMail(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	leaving := fixture.mailbox(t, 1, "leaving")
	staying := fixture.mailbox(t, 2, "staying")
	for _, mailbox := range []*domain.Mailbox{leaving, staying} {
		if _, _, err := fixture.service.SendMessage(ctx, mailbox.UserID, mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}

	if fixture.service.AccountDeletionCleanerName() != "mailbox" {
		t.Fatalf("cleaner name = %q", fixture.service.AccountDeletionCleanerName())
	}
	if err := fixture.service.CleanAccountDeletion(ctx, 1); err != nil {
		t.Fatalf("CleanAccountDeletion: %v", err)
	}
	if owned, _ := fixture.service.ListMailboxes(ctx, 1); len(owned) != 0 {
		t.Fatalf("deleted account still owns %d mailboxes", len(owned))
	}
	if owned, _ := fixture.service.ListMailboxes(ctx, 2); len(owned) != 1 {
		t.Fatalf("another account lost its mailbox")
	}
	if len(fixture.repo.messages) != 1 {
		t.Fatalf("messages left = %d; want only the other account's", len(fixture.repo.messages))
	}
}

func TestSendMessageWithAnIdempotencyKeySendsOnce(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b", IdempotencyKey: " order-42 "}

	first, created, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, input)
	if err != nil || !created || first.IdempotencyKey != "order-42" {
		t.Fatalf("first send = %+v, created %v, err %v", first, created, err)
	}
	// The retry differs only in ways normalization removes.
	retry := input
	retry.To = []string{"X@Example.com"}
	retry.Subject = " s "
	again, created, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, retry)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("retry = id %d, created %v, err %v; want the first message %d", again.ID, created, err, first.ID)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("transport calls = %d; a retry must not send again", len(fixture.transport.sent))
	}

	changed := input
	changed.Text = "a different body"
	if _, _, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, changed); !errors.Is(err, ErrMailIdempotencyConflict) {
		t.Fatalf("changed content error = %v; want ErrMailIdempotencyConflict", err)
	}

	// The key is scoped to one mailbox.
	other := fixture.mailbox(t, 1, "other")
	if _, created, err := fixture.service.SendMessage(ctx, 1, other.ID, input); err != nil || !created {
		t.Fatalf("same key in another mailbox = created %v, err %v", created, err)
	}
	if len(fixture.transport.sent) != 2 {
		t.Fatalf("transport calls = %d; want 2", len(fixture.transport.sent))
	}
}

func TestSendMessageReplaysAFailedOrUnfinishedAttempt(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b", IdempotencyKey: "k1"}

	fixture.transport.err = &mailtransport.Error{Code: "transport.unreachable"}
	failed, _, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, input)
	if !errors.Is(err, ErrMailDeliveryFailed) {
		t.Fatalf("first send error = %v", err)
	}

	// The provider may have accepted the first attempt, so the same key must not send again
	// even now that the provider would answer.
	fixture.transport.err = nil
	again, created, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, input)
	if !errors.Is(err, ErrMailDeliveryFailed) || created || again.ID != failed.ID || again.FailureCode != "transport.unreachable" {
		t.Fatalf("replay = %+v, created %v, err %v", again, created, err)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("transport calls = %d; want 1", len(fixture.transport.sent))
	}

	fixture.repo.messages[failed.ID].Status = domain.MailStatusQueued
	if _, _, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, input); !errors.Is(err, ErrMailSendInProgress) {
		t.Fatalf("unfinished attempt error = %v; want ErrMailSendInProgress", err)
	}
}

func TestSendMessageSurvivesAConcurrentRequestWithTheSameKey(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b", IdempotencyKey: "race"}
	first, _, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, input)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}

	// The second request misses the lookup and loses the insert to the unique index.
	fixture.repo.hideIdempotencyLookup = true
	again, created, err := fixture.service.SendMessage(ctx, 1, mailbox.ID, input)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("racing send = id %d, created %v, err %v", again.ID, created, err)
	}
	if len(fixture.transport.sent) != 1 || len(fixture.repo.messages) != 1 {
		t.Fatalf("transport calls = %d, messages = %d; want 1 and 1", len(fixture.transport.sent), len(fixture.repo.messages))
	}
}

func TestSendMessageRejectsAnUnusableIdempotencyKey(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	for _, key := range []string{"has space", "非ascii", strings.Repeat("k", 256), "line\nbreak"} {
		input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b", IdempotencyKey: key}
		if _, _, err := fixture.service.SendMessage(context.Background(), 1, mailbox.ID, input); !errors.Is(err, ErrMailIdempotencyKeyInvalid) {
			t.Fatalf("key %q error = %v; want ErrMailIdempotencyKeyInvalid", key, err)
		}
	}
	if len(fixture.transport.sent) != 0 {
		t.Fatalf("an invalid key reached the provider")
	}
}

func TestSendFromAddressUsesOnlyTheCallersMailbox(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	fixture.mailbox(t, 1, "mine")
	fixture.mailbox(t, 2, "theirs")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}

	message, created, err := fixture.service.SendFromAddress(ctx, 1, "Mine@Mail.Example", input)
	if err != nil || !created || message.FromAddress != "mine@mail.example" {
		t.Fatalf("SendFromAddress = %+v, created %v, err %v", message, created, err)
	}
	for _, from := range []string{"theirs@mail.example", "nobody@mail.example"} {
		if _, _, err := fixture.service.SendFromAddress(ctx, 1, from, input); !errors.Is(err, ErrMailboxNotFound) {
			t.Fatalf("from %q error = %v; want ErrMailboxNotFound", from, err)
		}
	}
	if _, _, err := fixture.service.SendFromAddress(ctx, 1, "Mine <mine@mail.example>", input); !errors.Is(err, ErrMailMessageInvalid) {
		t.Fatalf("display-name from error = %v", err)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("transport calls = %d; want 1", len(fixture.transport.sent))
	}

	if found, err := fixture.service.GetOutboundMessage(ctx, 1, message.ID); err != nil || found.ID != message.ID {
		t.Fatalf("GetOutboundMessage = %+v, %v", found, err)
	}
	if _, err := fixture.service.GetOutboundMessage(ctx, 2, message.ID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("another user's lookup error = %v", err)
	}
}

func TestSendFingerprintSeparatesFieldBoundaries(t *testing.T) {
	a := sendFingerprint([]string{"a@x.io"}, nil, "ab", "c", "", "")
	b := sendFingerprint([]string{"a@x.io"}, nil, "a", "bc", "", "")
	if a == b {
		t.Fatal("moving text between subject and body kept the same fingerprint")
	}
}

func TestListLogValidatesFiltersAndTrimsTheQuery(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mine := fixture.mailbox(t, 1, "mine")
	fixture.mailbox(t, 2, "theirs")
	if _, _, err := fixture.service.SendMessage(ctx, 1, mine.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	messages, total, err := fixture.service.ListLog(ctx, 1, domain.MailLogFilter{Query: "  receipt  "})
	if err != nil || total != 1 || len(messages) != 1 {
		t.Fatalf("ListLog = %d messages, total %d, err %v", len(messages), total, err)
	}
	if fixture.repo.lastLogFilter.Query != "receipt" {
		t.Fatalf("query = %q; want it trimmed", fixture.repo.lastLogFilter.Query)
	}
	if _, total, _ := fixture.service.ListLog(ctx, 2, domain.MailLogFilter{}); total != 0 {
		t.Fatalf("another user sees %d messages", total)
	}

	invalid := []domain.MailLogFilter{
		{Direction: "sideways"},
		{Status: "delivered"},
		{Query: strings.Repeat("q", 201)},
	}
	for _, filter := range invalid {
		if _, _, err := fixture.service.ListLog(ctx, 1, filter); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("filter %+v error = %v; want ErrInvalidInput", filter, err)
		}
	}
}

func TestStatsCoversEveryDayOfTheWindow(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.now = time.Date(2026, 11, 16, 15, 30, 0, 0, time.UTC)
	fixture.repo.stats = []domain.MailDayStats{
		{Day: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), Sent: 4, Failed: 1, Received: 2},
	}

	stats, err := fixture.service.Stats(context.Background(), 1, 3)
	if err != nil || len(stats) != 3 {
		t.Fatalf("Stats = %v, %v", stats, err)
	}
	if want := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC); !fixture.repo.lastStatsSince.Equal(want) {
		t.Fatalf("window starts %v; want %v", fixture.repo.lastStatsSince, want)
	}
	if stats[0].Sent != 0 || !stats[0].Day.Equal(time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("first day = %+v; want an empty 14th", stats[0])
	}
	if stats[1].Sent != 4 || stats[1].Failed != 1 || stats[1].Received != 2 {
		t.Fatalf("second day = %+v", stats[1])
	}
	if !stats[2].Day.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) || stats[2].Received != 0 {
		t.Fatalf("last day = %+v; want an empty today", stats[2])
	}

	for _, days := range []int{0, -1, MaxStatsDays + 1} {
		if _, err := fixture.service.Stats(context.Background(), 1, days); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("days %d error = %v; want ErrInvalidInput", days, err)
		}
	}
}
