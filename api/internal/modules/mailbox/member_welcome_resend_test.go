package mailbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// newResendFixture is a welcome fixture where mailbox 51 belongs to member 902, mailbox 52 is
// reserved for an invitee, and the rest cannot be welcomed: a shared mailbox, the owner's own
// mailbox, a mailbox on the shared domain, and one whose owner left.
func newResendFixture(t *testing.T) (*txFixture, *[]auditstarter.Change) {
	t.Helper()
	f := newWelcomeFixture(t)
	for _, statement := range []string{
		`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES
			(902, 'alice', 'alice@gmail.com', 'x', now(), now()),
			(903, 'gone', 'gone@gmail.com', 'x', now(), now())`,
		`UPDATE mailboxes SET type = 'personal', owner_user_id = 902 WHERE id = 51`,
		`UPDATE mailboxes SET type = 'personal', pending_owner_email = 'bo@gmail.com' WHERE id = 52`,
		`INSERT INTO mailboxes (id, organization_id, created_by, address, display_name, type, owner_user_id, owner_left_at, created_at, updated_at)
			VALUES (53, 10, 901, 'team@acme.example', 'Team', 'shared', NULL, NULL, now(), now()),
			       (54, 10, 901, 'owner@acme.example', 'Owner', 'personal', 901, NULL, now(), now()),
			       (55, 10, 901, 'alice@mail.example', 'Alice', 'personal', 902, NULL, now(), now()),
			       (56, 10, 901, 'gone@acme.example', 'Gone', 'personal', 903, now(), now(), now()),
			       (57, 11, 901, 'other@acme.example', 'Other', 'personal', 902, NULL, now(), now())`,
	} {
		if err := f.db.Exec(statement).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	f.service.accounts = fakeAccounts{txUser: "owner@example.com", 902: "Alice@Gmail.com", 903: "gone@gmail.com"}
	audited := &[]auditstarter.Change{}
	f.service.recordAudit = func(_ context.Context, change auditstarter.Change) {
		*audited = append(*audited, change)
	}
	return f, audited
}

func (f *txFixture) resend(mailboxID uint) (MemberWelcomeMail, error) {
	return f.service.ResendMemberWelcome(context.Background(), viewerOf(10, txUser, domain.OrganizationRoleOwner), mailboxID)
}

func (f *txFixture) welcomeMessage(t *testing.T, key string) *domain.TransactionalMessage {
	t.Helper()
	var id uint
	if err := f.db.Raw(`SELECT id FROM transactional_messages WHERE idempotency_key = ?`, key).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("no message with key %q: %v", key, err)
	}
	message, err := f.repo.FindTransactional(context.Background(), 10, id)
	if err != nil {
		t.Fatalf("FindTransactional: %v", err)
	}
	return message
}

func TestResendMemberWelcomeSendsAFreshMessage(t *testing.T) {
	f, audited := newResendFixture(t)
	if first := f.welcome(t, 51, "alice@acme.example", "alice@gmail.com"); first.Status != MemberWelcomeQueued {
		t.Fatalf("first = %+v", first)
	}
	f.clock = f.clock.Add(2 * time.Minute)

	got, err := f.resend(51)
	if err != nil {
		t.Fatalf("ResendMemberWelcome: %v", err)
	}
	if got.MailboxID != 51 || got.Address != "alice@acme.example" || got.ResendCount != 1 || got.InvitationPending ||
		got.OwnerUserID == nil || *got.OwnerUserID != 902 || got.QueuedAt == nil || !got.QueuedAt.Equal(f.clock) {
		t.Fatalf("resent = %+v", got)
	}
	if got.Status != MemberWelcomeDeliveryQueued && got.Status != MemberWelcomeDeliverySent {
		t.Fatalf("status = %s", got.Status)
	}
	message := f.welcomeMessage(t, "member-welcome:51:1")
	if message.ToAddress != "alice@gmail.com" || message.TemplateKey != memberWelcomeTemplateKey ||
		message.FromAddress != "no-reply@acme.example" {
		t.Fatalf("message = %+v", message)
	}
	if len(f.transport.sent) != 2 {
		t.Fatalf("sent = %d; want the first welcome and the resend", len(f.transport.sent))
	}
	if again := f.welcome(t, 51, "alice@acme.example", "alice@gmail.com"); again.Status != MemberWelcomeAlreadySent {
		t.Fatalf("a roster upload after a resend = %+v", again)
	}
	if len(*audited) != 1 || (*audited)[0].Action != "resend" || (*audited)[0].Resource != "member_welcome_mails" ||
		(*audited)[0].TargetID != "51" || (*audited)[0].Metadata["resend_count"] != 1 {
		t.Fatalf("audited = %+v", *audited)
	}

	list, err := f.service.ListMemberWelcomeMails(context.Background(), viewerOf(10, txUser, domain.OrganizationRoleOwner))
	if err != nil {
		t.Fatalf("ListMemberWelcomeMails: %v", err)
	}
	if len(list) != 2 || list[0].MailboxID != 51 || list[1].MailboxID != 52 {
		t.Fatalf("list = %+v; want only the member's and the invitee's mailboxes", list)
	}
	if list[0].Status != MemberWelcomeDeliverySent || list[0].SentAt == nil || list[0].ResendCount != 1 {
		t.Fatalf("welcomed = %+v", list[0])
	}
	if list[1].Status != MemberWelcomeDeliveryNotSent || !list[1].InvitationPending || list[1].OwnerUserID != nil ||
		list[1].QueuedAt != nil || list[1].SentAt != nil {
		t.Fatalf("never welcomed = %+v", list[1])
	}
}

func TestResendMemberWelcomeCompletesAWelcomeNeverSent(t *testing.T) {
	f, _ := newResendFixture(t)
	got, err := f.resend(52)
	if err != nil {
		t.Fatalf("ResendMemberWelcome: %v", err)
	}
	if got.ResendCount != 1 || !got.InvitationPending {
		t.Fatalf("resent = %+v", got)
	}
	if message := f.welcomeMessage(t, "member-welcome:52:1"); message.ToAddress != "bo@gmail.com" {
		t.Fatalf("to = %s; an invitee is welcomed at the invitation address", message.ToAddress)
	}
	if again := f.welcome(t, 52, "bo@acme.example", "bo@gmail.com"); again.Status != MemberWelcomeAlreadySent {
		t.Fatalf("a roster upload after a resend = %+v", again)
	}
}

func TestResendMemberWelcomeIsRateLimited(t *testing.T) {
	f, _ := newResendFixture(t)
	if first := f.welcome(t, 51, "alice@acme.example", "alice@gmail.com"); first.Status != MemberWelcomeQueued {
		t.Fatalf("first = %+v", first)
	}
	assertLimited := func(label string, wantWait time.Duration) {
		t.Helper()
		_, err := f.resend(51)
		var retry *RetryAfterError
		if !errors.Is(err, ErrMemberWelcomeResendLimited) || !errors.As(err, &retry) {
			t.Fatalf("%s: err = %v", label, err)
		}
		if retry.RetryAfter <= 0 || retry.RetryAfter > wantWait {
			t.Fatalf("%s: retry after %s, want at most %s", label, retry.RetryAfter, wantWait)
		}
	}
	assertLimited("right after the first welcome", time.Minute)

	start := f.clock
	for n := 1; n <= 5; n++ {
		f.clock = f.clock.Add(61 * time.Second)
		if _, err := f.resend(51); err != nil {
			t.Fatalf("resend %d: %v", n, err)
		}
		if n < 5 {
			assertLimited("within a minute", time.Minute)
		}
	}
	f.clock = f.clock.Add(time.Hour)
	assertLimited("a sixth resend in 24 hours", 24*time.Hour-f.clock.Sub(start.Add(61*time.Second)))

	f.clock = start.Add(61*time.Second + 24*time.Hour)
	got, err := f.resend(51)
	if err != nil || got.ResendCount != 6 {
		t.Fatalf("after the window = %+v, %v", got, err)
	}
	f.welcomeMessage(t, "member-welcome:51:6")
}

func TestResendMemberWelcomeRefusals(t *testing.T) {
	f, audited := newResendFixture(t)
	ctx := context.Background()
	member := viewerOf(10, 902, domain.OrganizationRoleMember)
	if _, err := f.service.ResendMemberWelcome(ctx, member, 51); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member resend: %v", err)
	}
	if _, err := f.service.ListMemberWelcomeMails(ctx, member); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member list: %v", err)
	}
	for _, id := range []uint{53, 54, 55, 56} {
		if _, err := f.resend(id); !errors.Is(err, ErrMemberWelcomeNotEligible) {
			t.Fatalf("mailbox %d: err = %v", id, err)
		}
	}
	for _, id := range []uint{57, 999} {
		if _, err := f.resend(id); !errors.Is(err, ErrMailboxNotFound) {
			t.Fatalf("mailbox %d: err = %v", id, err)
		}
	}

	owner := viewerOf(10, txUser, domain.OrganizationRoleOwner)
	if _, err := f.service.UpdateMemberWelcomeSetting(ctx, owner, MemberWelcomeInput{Enabled: false, Language: MemberWelcomeEnglish}); err != nil {
		t.Fatalf("UpdateMemberWelcomeSetting: %v", err)
	}
	if _, err := f.resend(51); !errors.Is(err, ErrMemberWelcomeDisabled) {
		t.Fatalf("disabled: err = %v", err)
	}
	if _, err := f.service.UpdateMemberWelcomeSetting(ctx, owner, MemberWelcomeInput{Enabled: true, Language: MemberWelcomeEnglish}); err != nil {
		t.Fatalf("UpdateMemberWelcomeSetting: %v", err)
	}
	f.service.settings.PublicURL = ""
	if _, err := f.resend(51); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("not configured: err = %v", err)
	}
	f.service.settings.PublicURL = "https://app.zmail.test"
	if got := f.count(t); got != 0 {
		t.Fatalf("messages = %d; a refused resend stores nothing", got)
	}
	// Refusals count toward no limit: the first real resend goes through at once.
	if _, err := f.resend(51); err != nil {
		t.Fatalf("after refusals: %v", err)
	}
	resends := 0
	for _, change := range *audited {
		if change.Resource == "member_welcome_mails" {
			resends++
		}
	}
	if resends != 1 {
		t.Fatalf("audited %d resends; refusals are not audited as resends", resends)
	}
}

// The Members tab reads member mailbox addresses from the mailbox list: an admin sees every
// member's Zmail personal mailbox there, a member only their own.
func TestAdminListsEveryMemberPersonalMailbox(t *testing.T) {
	f, _ := newResendFixture(t)
	ctx := context.Background()
	owned := func(viewer domain.OrganizationContext) map[string]uint {
		t.Helper()
		mailboxes, err := f.service.ListMailboxes(ctx, viewer, MailboxListScopeAll)
		if err != nil {
			t.Fatalf("ListMailboxes: %v", err)
		}
		addresses := map[string]uint{}
		for _, mailbox := range mailboxes {
			if mailbox.Type == domain.MailboxTypePersonal && mailbox.OwnerUserID != nil {
				addresses[mailbox.Address] = *mailbox.OwnerUserID
			}
		}
		return addresses
	}
	admin := owned(viewerOf(10, 904, domain.OrganizationRoleAdmin))
	if admin["alice@acme.example"] != 902 || admin["owner@acme.example"] != txUser {
		t.Fatalf("admin sees %v; want every member's personal mailbox with its owner", admin)
	}
	member := owned(viewerOf(10, 903, domain.OrganizationRoleMember))
	if _, ok := member["alice@acme.example"]; ok {
		t.Fatalf("member sees %v; another member's mailbox stays out of their list", member)
	}
}
