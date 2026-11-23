package mailbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

var welcomeSetup = memberWelcomeClientSetup{
	SettingsURL: "https://app.zmail.test/console/settings?tab=mail-clients",
	IMAPHost:    "imap.zmail.im", IMAPPort: 993, SMTPHost: "smtp.zmail.im", SMTPPort: 465, SubmissionPort: 587,
}

func TestMemberWelcomeRendersTheMailAppSectionInEveryLanguage(t *testing.T) {
	values := memberWelcomeValues{
		MemberName: "Alice", MailboxAddress: "alice@acme.example", OrganizationName: "Acme <& Co>",
		SignInURL: "https://app.zmail.test/login",
	}
	for _, language := range memberWelcomeLanguages {
		rendered, err := renderMemberWelcome(MemberWelcomeDefault(language), language, values, welcomeSetup)
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		if !strings.Contains(rendered.Subject, "Acme <& Co>") {
			t.Fatalf("%s subject = %q", language, rendered.Subject)
		}
		for _, want := range []string{
			"Alice", "alice@acme.example", values.SignInURL, welcomeSetup.SettingsURL,
			"imap.zmail.im", "993", "smtp.zmail.im", "465", "587", "STARTTLS",
		} {
			if !strings.Contains(rendered.Text, want) {
				t.Fatalf("%s text lacks %q:\n%s", language, want, rendered.Text)
			}
		}
		if strings.Contains(rendered.HTML, "<& Co>") || !strings.Contains(rendered.HTML, "Acme &lt;&amp; Co&gt;") {
			t.Fatalf("%s html does not escape the organization name:\n%s", language, rendered.HTML)
		}
		link := `<a href="https://app.zmail.test/console/settings?tab=mail-clients">`
		if !strings.Contains(rendered.HTML, link) || !strings.Contains(rendered.HTML, `<a href="https://app.zmail.test/login">`) {
			t.Fatalf("%s html lacks the links:\n%s", language, rendered.HTML)
		}
	}
}

func TestMemberWelcomeNameFallsBackToTheMailboxAddress(t *testing.T) {
	rendered, err := renderMemberWelcome(MemberWelcomeTemplate{Subject: "Hi {{member_name}}", Body: "x"}, MemberWelcomeEnglish,
		memberWelcomeValues{MailboxAddress: "bo@acme.example"}, welcomeSetup)
	if err != nil || rendered.Subject != "Hi bo@acme.example" {
		t.Fatalf("subject = %q, err = %v", rendered.Subject, err)
	}
}

func TestMemberWelcomeCopyValidation(t *testing.T) {
	cases := map[string]struct {
		language      MemberWelcomeLanguage
		subject, body string
		field         string
	}{
		"unknown language":  {"fr-FR", "", "", "language"},
		"unknown variable":  {MemberWelcomeEnglish, "Hi {{password}}", "", "subject"},
		"malformed":         {MemberWelcomeEnglish, "", "Hi {{member_name", "body"},
		"two-line subject":  {MemberWelcomeEnglish, "Hi\nthere", "", "subject"},
		"subject too long":  {MemberWelcomeEnglish, strings.Repeat("x", maxMemberWelcomeSubject+1), "", "subject"},
		"body too long":     {MemberWelcomeChinese, "", strings.Repeat("字", maxMemberWelcomeBody+1), "body"},
		"variable in body:": {MemberWelcomeChinese, "", "{{token}}", "body"},
	}
	for name, tc := range cases {
		err := validateMemberWelcomeCopy(tc.language, tc.subject, tc.body)
		var validation *ValidationError
		if !errors.As(err, &validation) || len(validation.Fields[tc.field]) == 0 {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if err := validateMemberWelcomeCopy(MemberWelcomeEnglish, "Welcome {{member_name|friend}}", "{{sign_in_url}}"); err != nil {
		t.Fatalf("valid copy refused: %v", err)
	}
}

// newWelcomeFixture is a transactional fixture with a verified organization domain, a console
// origin, and the mail app server members are told about.
func newWelcomeFixture(t *testing.T) *txFixture {
	t.Helper()
	f := newTxFixture(t, func(settings *Settings) {
		settings.PublicURL = "https://app.zmail.test"
		settings.ClientIMAPHost, settings.ClientIMAPPort = "imap.zmail.im", 993
		settings.ClientSMTPHost, settings.ClientSMTPPort, settings.ClientSubmissionPort = "smtp.zmail.im", 465, 587
	})
	for _, statement := range []string{
		`INSERT INTO mail_domains (organization_id, name, zone_id, zone_name, status, created_by, created_at, updated_at)
			VALUES (10, 'acme.example', 'zone-9', 'acme.example', 'verified', 901, now(), now())`,
		`INSERT INTO mailboxes (id, organization_id, created_by, address, display_name, created_at, updated_at)
			VALUES (51, 10, 901, 'alice@acme.example', 'Alice', now(), now()),
			       (52, 10, 901, 'bo@acme.example', '', now(), now())`,
	} {
		if err := f.db.Exec(statement).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return f
}

func (f *txFixture) welcome(t *testing.T, mailboxID uint, address, recipient string) memberWelcomeOutcome {
	t.Helper()
	mailbox := &domain.Mailbox{ID: mailboxID, OrganizationID: 10, Address: address}
	return f.service.welcomeMember(context.Background(), viewerOf(10, txUser, domain.OrganizationRoleOwner), mailbox, recipient, "Alice")
}

func TestWelcomeMemberQueuesOneMessagePerMailbox(t *testing.T) {
	f := newWelcomeFixture(t)
	first := f.welcome(t, 51, "alice@acme.example", "Alice@Gmail.com")
	if first.Status != MemberWelcomeQueued || first.MessageID == 0 {
		t.Fatalf("first = %+v", first)
	}
	message, err := f.repo.FindTransactional(context.Background(), 10, first.MessageID)
	if err != nil {
		t.Fatalf("FindTransactional: %v", err)
	}
	if message.TemplateKey != memberWelcomeTemplateKey || message.ToAddress != "alice@gmail.com" ||
		message.FromAddress != "no-reply@acme.example" || message.FromName != "Org" || message.TemplateID != nil ||
		message.IdempotencyKey == nil || *message.IdempotencyKey != "member-welcome:51" || message.Mode != domain.APIKeyModeLive {
		t.Fatalf("message = %+v", message)
	}
	if message.Status != domain.TransactionalStatusSent {
		t.Fatalf("status = %s; the welcome is dispatched right away", message.Status)
	}
	if len(f.transport.sent) != 1 || !strings.Contains(f.transport.sent[0].TextBody, "imap.zmail.im") {
		t.Fatalf("sent = %+v", f.transport.sent)
	}

	again := f.welcome(t, 51, "alice@acme.example", "alice@gmail.com")
	if again.Status != MemberWelcomeAlreadySent {
		t.Fatalf("again = %+v", again)
	}
	if got := f.count(t); got != 1 {
		t.Fatalf("messages = %d; want one welcome per mailbox", got)
	}
	if other := f.welcome(t, 52, "bo@acme.example", "bo@gmail.com"); other.Status != MemberWelcomeQueued {
		t.Fatalf("second mailbox = %+v", other)
	}
}

func TestWelcomeMemberHonorsTheSwitchAndTheInstance(t *testing.T) {
	f := newWelcomeFixture(t)
	owner := viewerOf(10, txUser, domain.OrganizationRoleOwner)
	if _, err := f.service.UpdateMemberWelcomeSetting(context.Background(), owner, MemberWelcomeInput{
		Enabled: false, Language: MemberWelcomeEnglish,
	}); err != nil {
		t.Fatalf("UpdateMemberWelcomeSetting: %v", err)
	}
	if got := f.welcome(t, 51, "alice@acme.example", "alice@gmail.com"); got.Status != MemberWelcomeDisabled {
		t.Fatalf("disabled = %+v", got)
	}
	if _, err := f.service.UpdateMemberWelcomeSetting(context.Background(), owner, MemberWelcomeInput{
		Enabled: true, Language: MemberWelcomeEnglish,
	}); err != nil {
		t.Fatalf("UpdateMemberWelcomeSetting: %v", err)
	}
	f.service.settings.PublicURL = ""
	if got := f.welcome(t, 51, "alice@acme.example", "alice@gmail.com"); got.Status != MemberWelcomeNotConfigured {
		t.Fatalf("without a console origin = %+v", got)
	}
	f.service.settings.PublicURL = "https://app.zmail.test"
	if got := f.welcome(t, 51, "alice@unverified.example", "alice@gmail.com"); got.Status != MemberWelcomeFailed ||
		got.ErrorCode != domain.CodeMailDomainNotVerified {
		t.Fatalf("unverified sender = %+v", got)
	}
	if got := f.count(t); got != 0 {
		t.Fatalf("messages = %d; nothing is stored unless queued", got)
	}
	if got := f.welcome(t, 51, "alice@acme.example", "alice@gmail.com"); got.Status != MemberWelcomeQueued {
		t.Fatalf("after fixing = %+v; a failed welcome is retried", got)
	}
}

func TestWelcomeMemberRecordsASuppressedRecipient(t *testing.T) {
	f := newWelcomeFixture(t)
	if err := f.db.Exec(`INSERT INTO mail_suppressions (organization_id, email, reason, source, created_at)
		VALUES (10, 'alice@gmail.com', 'bounced', 'transactional', now())`).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := f.welcome(t, 51, "alice@acme.example", "alice@gmail.com")
	if got.Status != MemberWelcomeSuppressed || got.MessageID == 0 || len(f.transport.sent) != 0 {
		t.Fatalf("suppressed = %+v, sent = %d", got, len(f.transport.sent))
	}
}

func TestMemberWelcomeSettingIsForManagers(t *testing.T) {
	f := newWelcomeFixture(t)
	ctx := context.Background()
	owner := viewerOf(10, txUser, domain.OrganizationRoleOwner)
	member := viewerOf(10, 902, domain.OrganizationRoleMember)

	setting, err := f.service.MemberWelcomeSetting(ctx, owner)
	if err != nil || !setting.Enabled || setting.Language != MemberWelcomeChinese || setting.Customized() || setting.UpdatedAt != nil {
		t.Fatalf("default = %+v, %v", setting, err)
	}
	if _, err = f.service.MemberWelcomeSetting(ctx, member); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member read: %v", err)
	}
	if _, err = f.service.UpdateMemberWelcomeSetting(ctx, member, MemberWelcomeInput{Enabled: true, Language: MemberWelcomeEnglish}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member update: %v", err)
	}
	if _, err = f.service.PreviewMemberWelcome(ctx, member, MemberWelcomePreviewInput{Language: MemberWelcomeEnglish}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member preview: %v", err)
	}

	saved, err := f.service.UpdateMemberWelcomeSetting(ctx, owner, MemberWelcomeInput{
		Enabled: true, Language: MemberWelcomeEnglish, Subject: "  Hello {{member_name}}  ", Body: "Line one\r\nLine two",
	})
	if err != nil || saved.Subject != "Hello {{member_name}}" || saved.Body != "Line one\nLine two" || !saved.Customized() || saved.UpdatedAt == nil {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	reset, err := f.service.UpdateMemberWelcomeSetting(ctx, owner, MemberWelcomeInput{
		Enabled: true, Language: MemberWelcomeEnglish, Subject: MemberWelcomeDefault(MemberWelcomeEnglish).Subject,
	})
	if err != nil || reset.Customized() || reset.Effective() != MemberWelcomeDefault(MemberWelcomeEnglish) {
		t.Fatalf("reset = %+v, %v", reset, err)
	}
	if _, err = f.service.UpdateMemberWelcomeSetting(ctx, owner, MemberWelcomeInput{
		Enabled: true, Language: MemberWelcomeEnglish, Body: "{{password}}",
	}); err == nil {
		t.Fatal("an unknown variable was saved")
	}

	preview, err := f.service.PreviewMemberWelcome(ctx, owner, MemberWelcomePreviewInput{Language: MemberWelcomeChinese})
	if err != nil || !strings.Contains(preview.Subject, "Org") || !strings.Contains(preview.Text, "xiaoming@example.com") ||
		!strings.Contains(preview.Text, "https://app.zmail.test/login") || !strings.Contains(preview.HTML, "<p>") {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if got := f.count(t); got != 0 {
		t.Fatalf("a preview stored %d messages", got)
	}
}

// failingWelcomeStore refuses every read, like an unreachable database.
type failingWelcomeStore struct{}

func (failingWelcomeStore) FindMemberWelcomeSetting(context.Context, uint) (MemberWelcomeSetting, error) {
	return MemberWelcomeSetting{}, domain.ErrServiceUnavailable
}

func (failingWelcomeStore) SaveMemberWelcomeSetting(context.Context, uint, MemberWelcomeInput, uint, time.Time) (MemberWelcomeSetting, error) {
	return MemberWelcomeSetting{}, domain.ErrServiceUnavailable
}

func (failingWelcomeStore) ClaimMemberWelcome(context.Context, uint, uint, string, time.Time) (bool, error) {
	return false, domain.ErrServiceUnavailable
}

func (failingWelcomeStore) LinkMemberWelcomeMessage(context.Context, uint, uint) error {
	return domain.ErrServiceUnavailable
}

func (failingWelcomeStore) LockMemberWelcome(context.Context, uint, uint, string, time.Time) (MemberWelcomeMailPO, bool, error) {
	return MemberWelcomeMailPO{}, false, domain.ErrServiceUnavailable
}

func (failingWelcomeStore) SaveMemberWelcomeResend(context.Context, MemberWelcomeMailPO) error {
	return domain.ErrServiceUnavailable
}

func (failingWelcomeStore) ListWelcomeCandidates(context.Context, uint, uint) ([]*domain.Mailbox, error) {
	return nil, domain.ErrServiceUnavailable
}

func (failingWelcomeStore) ListMemberWelcomeDeliveries(context.Context, uint, uint) ([]memberWelcomeDelivery, error) {
	return nil, domain.ErrServiceUnavailable
}

func TestRosterReportsWelcomeFailuresWithoutFailingTheImport(t *testing.T) {
	f := newRosterFixture(t)
	f.service.welcome = failingWelcomeStore{}
	result := f.run(t, false,
		MemberImportRow{Email: "alice@gmail.com", Name: "Alice"},
		MemberImportRow{Email: "not-an-email"},
	)
	if result.Invited != 1 || result.Failed != 1 || result.WelcomeFailed != 1 || result.WelcomeQueued != 0 {
		t.Fatalf("counts = %+v", result)
	}
	row := result.Rows[0]
	if row.Status != MemberImportInvited || row.WelcomeStatus != MemberWelcomeFailed || row.WelcomeErrorCode != domain.CodeServiceUnavailable {
		t.Fatalf("row = %+v", row)
	}
	if result.Rows[1].WelcomeStatus != "" {
		t.Fatalf("a failed row has a welcome: %+v", result.Rows[1])
	}

	dry := f.run(t, true, MemberImportRow{Email: "carol@gmail.com"})
	if dry.Rows[0].WelcomeStatus != "" || dry.WelcomeFailed != 0 {
		t.Fatalf("a dry run welcomed: %+v", dry)
	}
}
