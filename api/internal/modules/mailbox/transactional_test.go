package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// testSecretKey is the transactional encryption key of the tests; examples, not real secrets.
const testSecretKey = "transactional-test-key-0123456789abcdef"

// txUser is the member that sends in organization 10, seeded by openTransactionalRepository.
const txUser = 901

// fakeAccounts serves account addresses by user id.
type fakeAccounts map[uint]string

func (a fakeAccounts) FindByID(_ context.Context, id uint) (*domain.User, error) {
	email, ok := a[id]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return &domain.User{ID: id, Email: email}, nil
}

// txFixture runs the service against the real migrations, so the stored rows obey the production
// constraints, with the log provider sending from the instance domain.
type txFixture struct {
	service   *service
	repo      *repository
	db        *gorm.DB
	transport *fakeTransport
	notifier  *fakeNotifier
	clock     time.Time
}

func newTxFixture(t *testing.T, edit func(*Settings)) *txFixture {
	t.Helper()
	repo, db := openTransactionalRepository(t)
	settings := Settings{
		Domain: "mail.example", Provider: config.MailboxProviderLog, DailySendLimit: 100,
		TransactionalEnabled: true, SecretEncryptionKey: testSecretKey, SimulatorDomain: "test.zmail.im",
	}
	if edit != nil {
		edit(&settings)
	}
	f := &txFixture{repo: repo, db: db, transport: &fakeTransport{}, notifier: &fakeNotifier{}, clock: time.Now().UTC().Truncate(time.Microsecond)}
	if settings.Provider != config.MailboxProviderLog {
		// A real provider sends only from a verified organization domain.
		if err := db.Exec(`INSERT INTO mail_domains (organization_id, name, zone_id, zone_name, status, created_by, created_at, updated_at)
			VALUES (10, 'mail.example', 'zone-1', 'mail.example', 'verified', ?, now(), now())`, txUser).Error; err != nil {
			t.Fatalf("seed domain: %v", err)
		}
	}
	f.service = NewService(repo, &fakeMemberships{}, repo, repo, repo, repo, repo, nil, nil, f.transport, f.notifier, settings,
		repo, ratelimit.NewMemoryStores(), fakeAccounts{txUser: "Owner.Account@Example.COM"}, nil)
	f.service.now = func() time.Time { return f.clock }
	f.service.background = func(run func()) { run() }
	if _, err := f.service.CreateTemplate(context.Background(), viewerOf(10, txUser, domain.OrganizationRoleOwner), TemplateInput{
		Kind:        domain.MailTemplateTransactional,
		Name:        "Signup code",
		Key:         "signup-code",
		FromAddress: "no-reply@mail.example",
		FromName:    "Acme",
		Subject:     "{{app}} verification code: {{code}}",
		HTML:        "<p>Hi {{name|there}}, your code is {{code}}</p>",
		Text:        "Hi {{name|there}}, your code is {{code}} for {{app}}",
		VariableDefinitions: []domain.TransactionalVariable{
			{Name: "code", Secret: true, Required: true},
			{Name: "app", Required: true},
			{Name: "name"},
		},
		SampleVariables: map[string]string{"code": "123456", "app": "Sample App", "undeclared": "x"},
	}); err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	return f
}

func liveActor() TransactionalActor {
	key := uint(31)
	return TransactionalActor{OrganizationID: 10, UserID: txUser, APIKeyID: &key, Mode: domain.APIKeyModeLive}
}

func testActor() TransactionalActor {
	key := uint(32)
	return TransactionalActor{OrganizationID: 10, UserID: txUser, APIKeyID: &key, Mode: domain.APIKeyModeTest}
}

func codeInput(key, to, code string) TransactionalSendInput {
	return TransactionalSendInput{
		Template: "signup-code", To: to, IdempotencyKey: key,
		Variables: map[string]string{"code": code, "app": "Acme"},
	}
}

func (f *txFixture) send(t *testing.T, actor TransactionalActor, input TransactionalSendInput) (*domain.TransactionalMessage, bool) {
	t.Helper()
	message, created, err := f.service.SendTransactional(context.Background(), actor, input)
	if err != nil {
		t.Fatalf("SendTransactional(%s): %v", input.IdempotencyKey, err)
	}
	return message, created
}

func (f *txFixture) count(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := f.db.Raw(`SELECT count(*) FROM transactional_messages`).Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return count
}

func (f *txFixture) row(t *testing.T, id uint) TransactionalMessagePO {
	t.Helper()
	var po TransactionalMessagePO
	if err := f.db.Where("id = ?", id).First(&po).Error; err != nil {
		t.Fatalf("read message %d: %v", id, err)
	}
	return po
}

// attempted marks messages as sent by the provider now, so they fill the transactional lane.
func (f *txFixture) attempted(t *testing.T, ids ...uint) {
	t.Helper()
	if err := f.db.Exec(`UPDATE transactional_messages SET status = 'sent', payload_ciphertext = NULL, queued_reason = NULL,
		next_attempt_at = NULL, send_attempted_at = ?, attempts = 1 WHERE id IN ?`, f.clock, ids).Error; err != nil {
		t.Fatalf("mark attempted: %v", err)
	}
}

// The secret value exists only inside the ciphertext; every readable copy is masked.
func TestSecretNeverStoredInClear(t *testing.T) {
	f := newTxFixture(t, nil)
	// Hold the fast dispatch so the queued row can be read.
	f.hold()
	message, created := f.send(t, liveActor(), codeInput("signup-u_8812", "lilei@example.com", "482913"))
	if !created || message.Status != domain.TransactionalStatusQueued || message.QueuedReason != nil {
		t.Fatalf("message = %+v, created %v", message, created)
	}
	if message.SubjectMasked != "Acme verification code: ••••••" {
		t.Fatalf("subject_masked = %q", message.SubjectMasked)
	}
	if message.VariablesMasked["code"] != "••••••" || message.VariablesMasked["app"] != "Acme" {
		t.Fatalf("variables_masked = %v", message.VariablesMasked)
	}
	var clear string
	if err := f.db.Raw(`SELECT (to_jsonb(t) - 'payload_ciphertext')::text FROM transactional_messages t WHERE id = ?`, message.ID).
		Scan(&clear).Error; err != nil {
		t.Fatalf("read row: %v", err)
	}
	if strings.Contains(clear, "482913") {
		t.Fatalf("a column other than payload_ciphertext holds the secret: %s", clear)
	}
	stored := f.row(t, message.ID)
	if bytes.Contains(stored.PayloadCiphertext, []byte("482913")) {
		t.Fatal("payload_ciphertext holds the secret in clear")
	}
	plain, err := crypto.NewAESEncryptorFromString(testSecretKey).Decrypt(stored.PayloadCiphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	var payload transactionalPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Subject != "Acme verification code: 482913" || !strings.Contains(payload.Text, "your code is 482913 for Acme") ||
		!strings.Contains(payload.HTML, "Hi there") {
		t.Fatalf("payload = %+v", payload)
	}
	if !strings.HasSuffix(stored.RFCMessageID, "@mail.example") || stored.FromAddress != "no-reply@mail.example" ||
		stored.TemplateKey != "signup-code" || stored.APIKeyID == nil || *stored.APIKeyID != 31 || stored.CreatedBy != txUser {
		t.Fatalf("snapshot = %+v", stored)
	}
	// The fast dispatch is held: nothing reached the provider.
	if len(f.transport.sent) != 0 {
		t.Fatalf("provider calls = %d", len(f.transport.sent))
	}
}

func TestIdempotentReplayReturnsFirstMessage(t *testing.T) {
	f := newTxFixture(t, nil)
	first, created := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	again, createdAgain := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	if !created || createdAgain || again.ID != first.ID || f.count(t) != 1 {
		t.Fatalf("replay = %d (created %v) after %d (created %v), %d rows", again.ID, createdAgain, first.ID, created, f.count(t))
	}
	if again.Events == nil {
		t.Fatal("a replay lists its events")
	}
}

func TestIdempotencyConflictOnDifferentContent(t *testing.T) {
	f := newTxFixture(t, nil)
	f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	for name, input := range map[string]TransactionalSendInput{
		"value":     codeInput("k1", "lilei@example.com", "000000"),
		"recipient": codeInput("k1", "hanmei@example.com", "482913"),
	} {
		if _, _, err := f.service.SendTransactional(context.Background(), liveActor(), input); !errors.Is(err, ErrMailIdempotencyConflict) {
			t.Errorf("different %s error = %v; want a conflict", name, err)
		}
	}
	if f.count(t) != 1 {
		t.Fatalf("rows = %d", f.count(t))
	}
}

func TestExpiredKeyIsReleasedAndReused(t *testing.T) {
	f := newTxFixture(t, nil)
	first, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	f.clock = f.clock.Add(25 * time.Hour)
	second, created := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "111111"))
	if !created || second.ID == first.ID {
		t.Fatalf("after 24 hours the key named %d again (created %v)", second.ID, created)
	}
	if old := f.row(t, first.ID); old.IdempotencyKey != nil {
		t.Fatalf("old key = %q; want released", *old.IdempotencyKey)
	}
	if fresh := f.row(t, second.ID); fresh.IdempotencyKey == nil || *fresh.IdempotencyKey != "k1" {
		t.Fatalf("new key = %v", fresh.IdempotencyKey)
	}
}

// Review Focus 3: one Idempotency-Key in both modes names two messages, each readable only by
// its own mode.
func TestLiveAndTestKeysHaveSeparateIdempotency(t *testing.T) {
	f := newTxFixture(t, nil)
	live, liveCreated := f.send(t, liveActor(), codeInput("shared-key", "lilei@example.com", "482913"))
	test, testCreated := f.send(t, testActor(), codeInput("shared-key", "lilei@example.com", "482913"))
	if !liveCreated || !testCreated || live.ID == test.ID || f.count(t) != 2 {
		t.Fatalf("live %d (%v), test %d (%v), %d rows", live.ID, liveCreated, test.ID, testCreated, f.count(t))
	}
	if live.Mode != domain.APIKeyModeLive || test.Mode != domain.APIKeyModeTest || !test.Simulated || live.Simulated {
		t.Fatalf("modes = %s/%s, simulated %v/%v", live.Mode, test.Mode, live.Simulated, test.Simulated)
	}
}

func TestKeyReadsOnlyItsOwnMode(t *testing.T) {
	f := newTxFixture(t, nil)
	ctx := context.Background()
	live, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	test, _ := f.send(t, testActor(), codeInput("k1", "lilei@example.com", "482913"))
	if got, err := f.service.GetTransactional(ctx, 10, domain.APIKeyModeLive, live.ID); err != nil || got.ID != live.ID || got.Events == nil {
		t.Fatalf("live read = %+v, %v", got, err)
	}
	for name, read := range map[string]func() error{
		"test key reads live": func() error {
			_, err := f.service.GetTransactional(ctx, 10, domain.APIKeyModeTest, live.ID)
			return err
		},
		"live key reads test": func() error {
			_, err := f.service.GetTransactional(ctx, 10, domain.APIKeyModeLive, test.ID)
			return err
		},
		"another organization": func() error {
			_, err := f.service.GetTransactional(ctx, 11, domain.APIKeyModeLive, live.ID)
			return err
		},
		"unknown message": func() error {
			_, err := f.service.GetTransactional(ctx, 10, domain.APIKeyModeLive, test.ID+100)
			return err
		},
	} {
		if err := read(); !errors.Is(err, ErrTransactionalMessageNotFound) {
			t.Errorf("%s error = %v; want not found", name, err)
		}
	}
}

// Review Focus 1: a padded, mixed-case recipient is the lowercase address everywhere.
func TestRecipientNormalizedEverywhere(t *testing.T) {
	f := newTxFixture(t, nil)
	ctx := context.Background()
	if err := f.repo.AddSuppression(ctx, domain.MailSuppression{
		OrganizationID: 10, Email: "lilei@example.com", Reason: domain.MailSuppressionReasonBounced,
		Source: domain.MailSuppressionSourceTransactional, CreatedAt: f.clock,
	}); err != nil {
		t.Fatalf("AddSuppression: %v", err)
	}
	padded, _ := f.send(t, liveActor(), codeInput("k1", " LiLei@Example.COM ", "482913"))
	if padded.ToAddress != "lilei@example.com" || padded.Status != domain.TransactionalStatusSuppressed {
		t.Fatalf("padded send = %s, %s; want the suppressed lowercase address", padded.ToAddress, padded.Status)
	}
	// The same fingerprint: the lowercase repeat replays instead of conflicting.
	replay, created := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	if created || replay.ID != padded.ID {
		t.Fatalf("lowercase repeat = %d (created %v); want the replay of %d", replay.ID, created, padded.ID)
	}
	// One recipient bucket: two more spellings fill the allowance of three.
	f.send(t, liveActor(), codeInput("k2", "LILEI@example.com", "482913"))
	f.send(t, liveActor(), codeInput("k3", "lilei@EXAMPLE.com ", "482913"))
	_, _, err := f.service.SendTransactional(ctx, liveActor(), codeInput("k4", "lilei@example.com", "482913"))
	if !errors.Is(err, ErrMailRecipientRateLimited) {
		t.Fatalf("fourth spelling error = %v; want the recipient limit", err)
	}
	// A simulator address is read in lowercase too.
	simulated, _ := f.send(t, liveActor(), codeInput("k5", "Bounced+u1@TEST.zmail.im", "482913"))
	if simulated.ToAddress != "bounced+u1@test.zmail.im" || !simulated.Simulated {
		t.Fatalf("simulator send = %s, simulated %v", simulated.ToAddress, simulated.Simulated)
	}
}

// Review Focus 4: an empty required value, secret or not, is missing, and nothing is stored.
func TestEmptyRequiredValueIsMissing(t *testing.T) {
	f := newTxFixture(t, nil)
	for name, variables := range map[string]map[string]string{
		"empty secret":   {"code": "", "app": "Acme"},
		"empty required": {"code": "482913", "app": ""},
		"absent secret":  {"app": "Acme"},
	} {
		input := TransactionalSendInput{Template: "signup-code", To: "lilei@example.com", IdempotencyKey: strings.ReplaceAll(name, " ", "-"), Variables: variables}
		_, _, err := f.service.SendTransactional(context.Background(), liveActor(), input)
		var missing *MissingVariablesError
		if !errors.Is(err, ErrMailTemplateVariablesMissing) || !errors.As(err, &missing) || len(missing.Names) != 1 {
			t.Errorf("%s error = %v; want one missing variable", name, err)
		}
	}
	if f.count(t) != 0 {
		t.Fatalf("rows = %d; want none", f.count(t))
	}
}

func TestUndeclaredVariableRefused(t *testing.T) {
	f := newTxFixture(t, nil)
	input := codeInput("k1", "lilei@example.com", "482913")
	input.Variables["extra"] = "x"
	_, _, err := f.service.SendTransactional(context.Background(), liveActor(), input)
	var fields *FieldsError
	if !errors.Is(err, ErrMailMessageInvalid) || !errors.As(err, &fields) || fields.Fields["variables.extra"] == nil {
		t.Fatalf("error = %v; want MESSAGE_INVALID naming variables.extra", err)
	}
	if f.count(t) != 0 {
		t.Fatalf("rows = %d", f.count(t))
	}
}

func TestUnknownSimulatorLocalPartRefused(t *testing.T) {
	f := newTxFixture(t, nil)
	for i, to := range []string{"nobody@test.zmail.im", "Lilei <lilei@example.com>", "a@b.com, c@d.com"} {
		_, _, err := f.service.SendTransactional(context.Background(), liveActor(), codeInput(fmt.Sprintf("k%d", i), to, "482913"))
		if !errors.Is(err, ErrMailMessageInvalid) {
			t.Errorf("to %q error = %v; want MESSAGE_INVALID", to, err)
		}
	}
	if f.count(t) != 0 {
		t.Fatalf("rows = %d", f.count(t))
	}
}

func TestTemplateChecks(t *testing.T) {
	f := newTxFixture(t, nil)
	ctx := context.Background()
	if _, err := f.service.CreateTemplate(ctx, viewerOf(10, txUser, domain.OrganizationRoleOwner), TemplateInput{
		Name: "Plain", Subject: "Hello", Text: "Body",
	}); err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	var standardKey string
	if err := f.db.Raw(`SELECT coalesce(template_key, '') FROM mail_templates WHERE name = 'Plain'`).Scan(&standardKey).Error; err != nil {
		t.Fatalf("read key: %v", err)
	}
	input := codeInput("k1", "lilei@example.com", "1")
	input.Template = "missing"
	if _, _, err := f.service.SendTransactional(ctx, liveActor(), input); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Errorf("unknown template error = %v", err)
	}
	other := liveActor()
	other.OrganizationID = 11
	if _, _, err := f.service.SendTransactional(ctx, other, codeInput("k1", "lilei@example.com", "1")); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Errorf("another organization's template error = %v", err)
	}
	input.Template = "Not A Key"
	var validation *ValidationError
	if _, _, err := f.service.SendTransactional(ctx, liveActor(), input); !errors.As(err, &validation) {
		t.Errorf("malformed key error = %v; want a validation error", err)
	}
	for _, key := range []string{"", strings.Repeat("k", 256), "has space"} {
		if _, _, err := f.service.SendTransactional(ctx, liveActor(), codeInput(key, "lilei@example.com", "1")); !errors.Is(err, ErrMailIdempotencyKeyInvalid) {
			t.Errorf("Idempotency-Key %q error = %v", key, err)
		}
	}
}

func TestRecipientRateLimitFourthIn60Seconds(t *testing.T) {
	f := newTxFixture(t, nil)
	ctx := context.Background()
	f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "1"))
	f.send(t, liveActor(), codeInput("k2", "lilei@example.com", "2"))
	// Replays answer before the recipient limit and use none of it.
	for i := 0; i < 3; i++ {
		if _, created := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "1")); created {
			t.Fatal("a replay created a message")
		}
	}
	f.send(t, liveActor(), codeInput("k3", "lilei@example.com", "3"))
	_, _, err := f.service.SendTransactional(ctx, liveActor(), codeInput("k4", "lilei@example.com", "4"))
	var retry *RetryAfterError
	if !errors.Is(err, ErrMailRecipientRateLimited) || !errors.As(err, &retry) || retry.RetryAfter <= 0 || retry.RetryAfter > time.Minute {
		t.Fatalf("fourth error = %v; want the recipient limit with a retry time", err)
	}
	// Another mode, template, or recipient has its own bucket.
	if _, _, err := f.service.SendTransactional(ctx, testActor(), codeInput("k4", "lilei@example.com", "4")); err != nil {
		t.Fatalf("test mode: %v", err)
	}
	if _, _, err := f.service.SendTransactional(ctx, liveActor(), codeInput("k5", "hanmei@example.com", "4")); err != nil {
		t.Fatalf("another recipient: %v", err)
	}
	if f.count(t) != 5 {
		t.Fatalf("rows = %d; the refused send stores nothing", f.count(t))
	}
}

func TestKeyRateLimitTwentyFirstInOneSecond(t *testing.T) {
	f := newTxFixture(t, nil)
	ctx := context.Background()
	// Requests refused after the key limit still use it; an unknown template keeps them fast.
	input := codeInput("k1", "lilei@example.com", "1")
	input.Template = "missing"
	for i := 0; i < 20; i++ {
		if _, _, err := f.service.SendTransactional(ctx, liveActor(), input); !errors.Is(err, ErrMailTemplateNotFound) {
			t.Fatalf("request %d error = %v", i+1, err)
		}
	}
	_, _, err := f.service.SendTransactional(ctx, liveActor(), input)
	var retry *RetryAfterError
	if !errors.Is(err, ErrTransactionalRateLimited) || !errors.As(err, &retry) || retry.RetryAfter > time.Second {
		t.Fatalf("21st error = %v; want the key limit", err)
	}
	if _, _, err := f.service.SendTransactional(ctx, testActor(), input); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("another key error = %v; want its own budget", err)
	}
}

func TestSuppressedRecipientStoredWithoutCiphertext(t *testing.T) {
	f := newTxFixture(t, func(settings *Settings) { settings.DailyTransactionalLimit = 1 })
	f.hold()
	ctx := context.Background()
	if err := f.repo.AddSuppression(ctx, domain.MailSuppression{
		OrganizationID: 10, Email: "lilei@example.com", Reason: domain.MailSuppressionReasonComplained,
		Source: domain.MailSuppressionSourceBroadcast, CreatedAt: f.clock,
	}); err != nil {
		t.Fatalf("AddSuppression: %v", err)
	}
	filler, _ := f.send(t, liveActor(), codeInput("filler", "hanmei@example.com", "1"))
	f.attempted(t, filler.ID)
	before := transactionalOutcomeCount(t, "live", "suppressed")
	message, created := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913"))
	if !created || message.Status != domain.TransactionalStatusSuppressed || message.QueuedReason != nil {
		t.Fatalf("message = %+v; want suppressed, ignoring the full lane", message)
	}
	// Counted once at acceptance; a replay of the same request is not counted again.
	if _, replayed := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "482913")); replayed {
		t.Fatal("the replay created a second message")
	}
	if got := transactionalOutcomeCount(t, "live", "suppressed") - before; got != 1 {
		t.Fatalf("suppressed outcomes counted = %v; want 1", got)
	}
	stored := f.row(t, message.ID)
	if stored.PayloadCiphertext != nil || stored.NextAttemptAt != nil || stored.SendAttemptedAt != nil {
		t.Fatalf("stored = %+v; want no payload or queue fields", stored)
	}
	if len(f.notifier.transactional) != 1 || f.notifier.transactional[0].Type != "email.suppressed" ||
		f.notifier.transactional[0].Message.ID != message.ID {
		t.Fatalf("notifications = %+v", f.notifier.transactional)
	}
	if used, err := f.repo.CountTransactionalAttemptsSince(ctx, 10, f.clock.Add(-sendLimitWindow)); err != nil || used != 1 {
		t.Fatalf("lane = %d, %v; the suppressed message takes no room", used, err)
	}
}

func TestTransactionalLaneFullQueuesThenRefuses(t *testing.T) {
	f := newTxFixture(t, func(settings *Settings) { settings.DailyTransactionalLimit = 2 })
	ctx := context.Background()
	first, _ := f.send(t, liveActor(), codeInput("k1", "a@example.com", "1"))
	second, _ := f.send(t, liveActor(), codeInput("k2", "b@example.com", "1"))
	if first.QueuedReason != nil || second.QueuedReason != nil {
		t.Fatal("messages within the lane wait for nothing")
	}
	f.attempted(t, first.ID, second.ID)
	for _, key := range []string{"k3", "k4"} {
		waiting, created := f.send(t, liveActor(), codeInput(key, key+"@example.com", "1"))
		if !created || waiting.QueuedReason == nil || *waiting.QueuedReason != domain.TransactionalQueuedSendLimit {
			t.Fatalf("%s = %+v; want queued for the send limit", key, waiting)
		}
	}
	_, _, err := f.service.SendTransactional(ctx, liveActor(), codeInput("k5", "k5@example.com", "1"))
	if !errors.Is(err, ErrMailSendLimitReached) {
		t.Fatalf("fifth error = %v; want the send limit", err)
	}
	if f.count(t) != 4 {
		t.Fatalf("rows = %d; the refused send stores nothing", f.count(t))
	}
	// Test and simulated messages never wait for the lane.
	for name, send := range map[string]func() (*domain.TransactionalMessage, bool, error){
		"test key": func() (*domain.TransactionalMessage, bool, error) {
			return f.service.SendTransactional(ctx, testActor(), codeInput("t1", "c@example.com", "1"))
		},
		"simulator": func() (*domain.TransactionalMessage, bool, error) {
			return f.service.SendTransactional(ctx, liveActor(), codeInput("s1", "delivered@test.zmail.im", "1"))
		},
	} {
		message, _, err := send()
		if err != nil || message.QueuedReason != nil {
			t.Errorf("%s = %+v, %v; want sent past the full lane", name, message, err)
		}
	}
}

func TestBroadcastLaneFullDoesNotDelayTransactional(t *testing.T) {
	f := newTxFixture(t, func(settings *Settings) {
		settings.DailySendLimit, settings.DailyBroadcastLimit, settings.DailyTransactionalLimit = 1, 1, 2
	})
	ctx := context.Background()
	mailbox := &domain.Mailbox{OrganizationID: 10, CreatedBy: txUser, Address: "team@mail.example", Type: domain.MailboxTypeShared}
	if err := f.repo.CreateMailbox(ctx, mailbox, 50); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	for i, lane := range []domain.SendLane{domain.SendLaneBroadcast, domain.SendLanePersonal, domain.SendLaneBroadcast} {
		attempted := f.clock.Add(-time.Minute)
		if _, err := f.repo.CreateMessage(ctx, &domain.MailMessage{
			MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusSent,
			FromAddress: mailbox.Address, To: []string{fmt.Sprintf("list%d@example.com", i)}, Subject: "news",
			SendAttemptedAt: &attempted, SendLane: lane,
		}); err != nil {
			t.Fatalf("store attempt: %v", err)
		}
	}
	message, _ := f.send(t, liveActor(), codeInput("k1", "lilei@example.com", "1"))
	if message.QueuedReason != nil {
		t.Fatalf("queued_reason = %s; the other lanes are full, not the transactional one", *message.QueuedReason)
	}
}

func TestTestKeyNeverCallsProvider(t *testing.T) {
	f := newTxFixture(t, nil)
	ctx := context.Background()
	if err := f.repo.AddSuppression(ctx, domain.MailSuppression{
		OrganizationID: 10, Email: "lilei@example.com", Reason: domain.MailSuppressionReasonBounced,
		Source: domain.MailSuppressionSourceBroadcast, CreatedAt: f.clock,
	}); err != nil {
		t.Fatalf("AddSuppression: %v", err)
	}
	message, _ := f.send(t, testActor(), codeInput("k1", "lilei@example.com", "482913"))
	if message.Mode != domain.APIKeyModeTest || !message.Simulated || message.Status != domain.TransactionalStatusQueued {
		t.Fatalf("message = %+v; want a simulated test message that ignores the suppression list", message)
	}
	if len(f.transport.sent) != 0 {
		t.Fatalf("provider calls = %d", len(f.transport.sent))
	}
	if used, err := f.repo.CountTransactionalAttemptsSince(ctx, 10, f.clock.Add(-sendLimitWindow)); err != nil || used != 0 {
		t.Fatalf("lane = %d, %v", used, err)
	}
}

func TestTransactionalDisabled(t *testing.T) {
	for name, edit := range map[string]func(*Settings){
		"switched off":  func(settings *Settings) { settings.TransactionalEnabled = false },
		"no secret key": func(settings *Settings) { settings.SecretEncryptionKey = "" },
	} {
		f := newTxFixture(t, edit)
		if _, _, err := f.service.SendTransactional(context.Background(), liveActor(), codeInput("k1", "lilei@example.com", "1")); !errors.Is(err, ErrTransactionalDisabled) {
			t.Errorf("%s send error = %v", name, err)
		}
		if _, err := f.service.SendTransactionalTest(context.Background(), viewerOf(10, txUser, domain.OrganizationRoleMember),
			TransactionalTestInput{Template: "signup-code", Mode: domain.APIKeyModeTest}); !errors.Is(err, ErrTransactionalDisabled) {
			t.Errorf("%s test send error = %v", name, err)
		}
		if f.service.TransactionalPolicy().Enabled {
			t.Errorf("%s policy reports enabled", name)
		}
	}
}

func TestTransactionalTestSendGoesToTheCallersAccount(t *testing.T) {
	f := newTxFixture(t, nil)
	ctx := context.Background()
	viewer := viewerOf(10, txUser, domain.OrganizationRoleMember)
	message, err := f.service.SendTransactionalTest(ctx, viewer, TransactionalTestInput{
		Template: "signup-code", Mode: domain.APIKeyModeLive, Variables: map[string]string{"app": "Given"},
	})
	if err != nil {
		t.Fatalf("SendTransactionalTest: %v", err)
	}
	if message.ToAddress != "owner.account@example.com" || message.APIKeyID != nil || message.CreatedBy != txUser ||
		message.Mode != domain.APIKeyModeLive || message.Simulated {
		t.Fatalf("message = %+v", message)
	}
	if message.SubjectMasked != "Given verification code: ••••••" || message.VariablesMasked["app"] != "Given" {
		t.Fatalf("given values replace samples: subject %q, variables %v", message.SubjectMasked, message.VariablesMasked)
	}
	if _, undeclared := message.VariablesMasked["undeclared"]; undeclared {
		t.Fatal("a sample of an undeclared variable was sent")
	}
	if message.IdempotencyKey == nil || !strings.HasPrefix(*message.IdempotencyKey, fmt.Sprintf("test-send:%d:", txUser)) {
		t.Fatalf("idempotency key = %v", message.IdempotencyKey)
	}
	again, err := f.service.SendTransactionalTest(ctx, viewer, TransactionalTestInput{Template: "signup-code", Mode: domain.APIKeyModeTest})
	if err != nil || again.ID == message.ID || !again.Simulated {
		t.Fatalf("second test send = %+v, %v; want a new simulated message", again, err)
	}
	// The recipient limit applies from here on: a third live send to the same address passes,
	// the fourth does not.
	if _, err = f.service.SendTransactionalTest(ctx, viewer, TransactionalTestInput{Template: "signup-code", Mode: domain.APIKeyModeLive}); err != nil {
		t.Fatalf("third: %v", err)
	}
	if _, err = f.service.SendTransactionalTest(ctx, viewer, TransactionalTestInput{Template: "signup-code", Mode: domain.APIKeyModeLive}); err != nil {
		t.Fatalf("fourth: %v", err)
	}
	if _, err = f.service.SendTransactionalTest(ctx, viewer, TransactionalTestInput{Template: "signup-code", Mode: domain.APIKeyModeLive}); !errors.Is(err, ErrMailRecipientRateLimited) {
		t.Fatalf("fifth error = %v; want the recipient limit", err)
	}
	// Sixth in the minute: the per-user test send limit answers first.
	_, err = f.service.SendTransactionalTest(ctx, viewer, TransactionalTestInput{Template: "signup-code", Mode: domain.APIKeyModeTest})
	if !errors.Is(err, ErrTransactionalRateLimited) {
		t.Fatalf("sixth error = %v; want the test send limit", err)
	}
}

// transactionalOutcomeCount reads zmail_transactional_messages_total for one mode and outcome from
// the default registry the metrics package registers with.
func transactionalOutcomeCount(t *testing.T, mode, outcome string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "zmail_transactional_messages_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["mode"] == mode && labels["outcome"] == outcome {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}
