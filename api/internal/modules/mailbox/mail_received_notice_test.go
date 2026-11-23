package mailbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

type fakeNotificationPublisher struct {
	mu           sync.Mutex
	publications []domain.NotificationPublication
	err          error
}

func (f *fakeNotificationPublisher) Publish(_ context.Context, publication domain.NotificationPublication) (*domain.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publications = append(f.publications, publication)
	if f.err != nil {
		return nil, f.err
	}
	return &domain.Notification{ID: uint(len(f.publications)), UserID: publication.UserID}, nil
}

func (f *fakeNotificationPublisher) recipients() []uint {
	f.mu.Lock()
	defer f.mu.Unlock()
	users := make([]uint, 0, len(f.publications))
	for _, publication := range f.publications {
		users = append(users, publication.UserID)
	}
	slices.Sort(users)
	return users
}

func rawMail(from, subject, messageID string) []byte {
	lines := []string{"From: " + from, "To: team@mail.example"}
	if subject != "" {
		lines = append(lines, "Subject: "+subject)
	}
	lines = append(lines, "Message-ID: <"+messageID+">", "", "Body text.")
	return crlf(lines...)
}

func TestReceiveInboundPublishesMailReceivedToEveryoneWhoCanReadTheMailbox(t *testing.T) {
	fixture := newServiceFixture(t)
	publisher := &fakeNotificationPublisher{}
	fixture.service.notifications = publisher
	memberships := &fakeMemberships{absent: map[uint]bool{}}
	fixture.service.memberships = memberships
	ctx := context.Background()
	const viewer, sender, departed = 5, 6, 7
	team, err := fixture.service.CreateMailbox(ctx, as(1), CreateMailboxInput{
		LocalPart: "team", Type: domain.MailboxTypeShared, Members: []MailboxMemberInput{
			{UserID: testCreator, Permission: domain.MailboxPermissionManage},
			{UserID: viewer, Permission: domain.MailboxPermissionView},
			{UserID: sender, Permission: domain.MailboxPermissionSend},
			{UserID: departed, Permission: domain.MailboxPermissionView},
		},
	})
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	memberships.absent[departed] = true

	raw := rawMail("Alice Example <alice@sender.test>", "Your verification code is 482913", "code-1@sender.test")
	message, created, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("team@mail.example", raw))
	if err != nil || !created {
		t.Fatalf("ReceiveInbound = %v, created %v", err, created)
	}
	if got, want := publisher.recipients(), []uint{viewer, sender, testCreator}; !slices.Equal(got, want) {
		t.Fatalf("recipients = %v; want %v", got, want)
	}
	publication := publisher.publications[0]
	wantURL := fmt.Sprintf("/console/mailboxes?org=1&mailbox=%d&open=%d", team.ID, message.ID)
	if publication.Kind != "mail.received" ||
		publication.IdempotencyKey != fmt.Sprintf("mail.received:%d", message.ID) ||
		publication.Title != "Alice Example" ||
		publication.Body != "Your verification code is ••••••" ||
		publication.ActionURL != wantURL ||
		!slices.Equal(publication.Channels, []domain.NotificationChannel{domain.NotificationChannelWebPush}) ||
		len(publication.RequiredChannels) != 0 {
		t.Fatalf("publication = %+v", publication)
	}

	if _, created, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("team@mail.example", raw)); err != nil || created {
		t.Fatalf("retry = created %v, err %v", created, err)
	}
	if len(publisher.publications) != 3 {
		t.Fatalf("a duplicate delivery published again: %d publications", len(publisher.publications))
	}
}

func TestReceiveInboundPublishesPersonalMailOnlyToTheOwner(t *testing.T) {
	fixture := newServiceFixture(t)
	publisher := &fakeNotificationPublisher{}
	fixture.service.notifications = publisher
	ctx := context.Background()
	const owner = 5
	if _, err := fixture.service.CreateMailbox(ctx, as(1), CreateMailboxInput{
		LocalPart: "ada", Type: domain.MailboxTypePersonal, OwnerUserID: owner,
	}); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	raw := rawMail("bob@sender.test", "", "personal-1@sender.test")
	if _, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("ada@mail.example", raw)); err != nil {
		t.Fatalf("ReceiveInbound: %v", err)
	}
	if got := publisher.recipients(); !slices.Equal(got, []uint{owner}) {
		t.Fatalf("recipients = %v; want only the owner", got)
	}
	if publication := publisher.publications[0]; publication.Title != "bob@sender.test" || publication.Body != "(no subject)" {
		t.Fatalf("publication = %+v", publication)
	}
}

func TestReceiveInboundSurvivesAFailingOrAbsentNotificationStarter(t *testing.T) {
	for name, publisher := range map[string]*fakeNotificationPublisher{
		"starter absent":   {err: domain.ErrServiceUnavailable},
		"publish fails":    {err: errors.New("database unavailable")},
		"no publisher set": nil,
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			if publisher != nil {
				fixture.service.notifications = publisher
			}
			fixture.mailbox(t, 1, "hello")
			_, created, err := fixture.service.ReceiveInbound(context.Background(),
				fixture.delivery("hello@mail.example", inboundRaw("fails-1@sender.test")))
			if err != nil || !created {
				t.Fatalf("ReceiveInbound = %v, created %v; publishing must never fail receipt", err, created)
			}
		})
	}
}

func TestMailReceivedTextIsBounded(t *testing.T) {
	message := &domain.MailMessage{
		ID:          9,
		Direction:   domain.MailDirectionInbound,
		FromName:    strings.Repeat("N", 200) + "\x07",
		FromAddress: "n@sender.test",
		Subject:     strings.Repeat("s", 79) + "界界",
	}
	title, body := mailReceivedText(message)
	if utf8.RuneCountInString(title) > 160 || strings.ContainsRune(title, '\x07') {
		t.Fatalf("title = %q", title)
	}
	if utf8.RuneCountInString(body) != 80 || !strings.HasSuffix(body, "…") {
		t.Fatalf("body = %q", body)
	}
}
