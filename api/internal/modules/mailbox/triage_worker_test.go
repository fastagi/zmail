package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// recordingTriageModel answers from a script and records every input it receives.
type recordingTriageModel struct {
	mu     sync.Mutex
	inputs []TriageInput
	drafts []DraftInput
	err    error
	output TriageOutput
	draft  DraftOutput
	calls  int
}

func (m *recordingTriageModel) Classify(_ context.Context, input TriageInput) (TriageOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.inputs = append(m.inputs, input)
	return m.output, m.err
}

func (m *recordingTriageModel) Draft(_ context.Context, input DraftInput) (DraftOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.drafts = append(m.drafts, input)
	return m.draft, m.err
}

// receiveText delivers one signed inbound message with the given subject and text.
func (f *accessFixture) receiveText(t *testing.T, mailbox *domain.Mailbox, messageID, subject, text string) *domain.MailMessage {
	t.Helper()
	raw := crlf(
		"From: Alice <alice@sender.test>",
		"To: "+mailbox.Address,
		"Subject: "+subject,
		"Message-ID: <"+messageID+">",
		"Content-Type: text/plain; charset=utf-8",
		"",
		text,
	)
	now := f.service.now()
	message, created, err := f.service.ReceiveInbound(context.Background(), InboundDelivery{
		Recipient: mailbox.Address, Raw: raw, Signature: signedHeader(now, mailbox.Address, raw),
	})
	if err != nil || !created {
		t.Fatalf("ReceiveInbound = %v, created %v", err, created)
	}
	return message
}

// fixClock pins the fixture's clock and returns a function that moves it.
func (f *accessFixture) fixClock() func(time.Duration) time.Time {
	clock := time.Now().UTC().Truncate(time.Second)
	f.service.now = func() time.Time { return clock }
	return func(step time.Duration) time.Time {
		clock = clock.Add(step)
		return clock
	}
}

func (f *accessFixture) dispatchTriage(t *testing.T) int {
	t.Helper()
	handled, err := f.service.DispatchTriage(context.Background(), 20)
	if err != nil {
		t.Fatalf("DispatchTriage: %v", err)
	}
	return handled
}

func TestTriageWorkerClassifiesPending(t *testing.T) {
	f := newTriageFixture(t)
	f.switchTriage(t, true, f.team)
	question := f.receiveText(t, f.team, "q@sender.test", "Deck", "Can you send the deck?")
	notice := f.receiveText(t, f.team, "n@sender.test", "Newsletter", "This week in review.")

	if handled := f.dispatchTriage(t); handled != 2 {
		t.Fatalf("handled %d, want 2", handled)
	}
	reply := f.triageOf(t, question.ID)
	if reply.Status != domain.MailTriageDone || reply.Category != domain.MailTriageReply || reply.Reason == "" ||
		reply.Source != domain.MailTriageSourceAI || reply.Model != config.AITriageProviderFake || reply.Attempts != 1 {
		t.Fatalf("question triage = %+v", reply)
	}
	if fyi := f.triageOf(t, notice.ID); fyi.Category != domain.MailTriageFYI {
		t.Fatalf("notice triage = %+v", fyi)
	}
	if handled := f.dispatchTriage(t); handled != 0 {
		t.Fatalf("a second pass handled %d, want 0", handled)
	}
}

// TestTriageWorkerSkipsLockedRows: a row another worker holds is skipped, not waited for.
func TestTriageWorkerSkipsLockedRows(t *testing.T) {
	f := newTriageFixture(t)
	f.switchTriage(t, true, f.team)
	held := f.receiveText(t, f.team, "held@sender.test", "One", "Text")
	free := f.receiveText(t, f.team, "free@sender.test", "Two", "Text")

	db := f.repo.(*repository).db
	tx := db.Begin()
	defer tx.Rollback()
	if err := tx.Exec(`SELECT 1 FROM mail_triage WHERE message_id = ? FOR UPDATE`, held.ID).Error; err != nil {
		t.Fatalf("lock: %v", err)
	}
	done := make(chan int, 1)
	go func() { done <- f.dispatchTriage(t) }()
	select {
	case handled := <-done:
		if handled != 1 {
			t.Fatalf("handled %d, want only the free row", handled)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the worker waited for a locked row")
	}
	if got := f.triageOf(t, free.ID); got.Status != domain.MailTriageDone {
		t.Fatalf("free row = %+v", got)
	}
	tx.Rollback()
	if got := f.triageOf(t, held.ID); got.Status != domain.MailTriagePending {
		t.Fatalf("held row = %+v, want still pending", got)
	}
}

// TestTriageWorkerBackoffThenFailed: failures retry after 1 and 5 minutes; the third failure is
// final and shows as review.
func TestTriageWorkerBackoffThenFailed(t *testing.T) {
	f := newTriageFixture(t)
	advance := f.fixClock()
	model := &recordingTriageModel{err: errors.New("provider timeout")}
	f.service.triageModel = model
	f.switchTriage(t, true, f.team)
	message := f.receiveText(t, f.team, "fail@sender.test", "Hello", "Text")

	f.dispatchTriage(t)
	row := f.triageOf(t, message.ID)
	if row.Status != domain.MailTriagePending || row.Attempts != 1 || !row.NextAttemptAt.Equal(f.service.now().Add(time.Minute)) {
		t.Fatalf("after the 1st failure: %+v", row)
	}
	advance(30 * time.Second)
	if handled := f.dispatchTriage(t); handled != 0 {
		t.Fatalf("retried %d before the backoff ended", handled)
	}
	advance(30 * time.Second)
	f.dispatchTriage(t)
	row = f.triageOf(t, message.ID)
	if row.Attempts != 2 || !row.NextAttemptAt.Equal(f.service.now().Add(5*time.Minute)) {
		t.Fatalf("after the 2nd failure: %+v", row)
	}
	advance(5 * time.Minute)
	f.dispatchTriage(t)
	row = f.triageOf(t, message.ID)
	if row.Status != domain.MailTriageFailed || row.Category != domain.MailTriageReview || row.Attempts != 3 || row.LastErrorCode == "" {
		t.Fatalf("after the 3rd failure: %+v", row)
	}
	if model.calls != 3 {
		t.Fatalf("model called %d times, want 3", model.calls)
	}
}

// TestTriageWorkerDailyCap: calls beyond the organization's cap wait for the next UTC day
// without using an attempt.
func TestTriageWorkerDailyCap(t *testing.T) {
	f := newTriageFixture(t)
	advance := f.fixClock()
	f.switchTriage(t, true, f.team)
	if _, err := f.triageRepo().SaveTriageSettings(context.Background(), domain.MailTriageSettings{
		OrganizationID: accessOrg, Enabled: true, DailyMessageCap: 1,
	}, f.service.now()); err != nil {
		t.Fatal(err)
	}
	first := f.receiveText(t, f.team, "cap-1@sender.test", "One", "Text")
	second := f.receiveText(t, f.team, "cap-2@sender.test", "Two", "Text")

	f.dispatchTriage(t)
	if got := f.triageOf(t, first.ID); got.Status != domain.MailTriageDone {
		t.Fatalf("first = %+v", got)
	}
	now := f.service.now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	waiting := f.triageOf(t, second.ID)
	if waiting.Status != domain.MailTriagePending || waiting.Attempts != 0 || !waiting.NextAttemptAt.Equal(midnight) {
		t.Fatalf("second = %+v, want pending until %s", waiting, midnight)
	}

	// The instance cap bounds an organization's own cap too.
	advance(midnight.Sub(now))
	f.service.settings.Triage.DailyCap = 1
	if _, err := f.triageRepo().SaveTriageSettings(context.Background(), domain.MailTriageSettings{
		OrganizationID: accessOrg, Enabled: true, DailyMessageCap: 50,
	}, f.service.now()); err != nil {
		t.Fatal(err)
	}
	third := f.receiveText(t, f.team, "cap-3@sender.test", "Three", "Text")
	f.dispatchTriage(t)
	if got := f.triageOf(t, second.ID); got.Status != domain.MailTriageDone {
		t.Fatalf("second on the next day = %+v", got)
	}
	if got := f.triageOf(t, third.ID); got.Status != domain.MailTriagePending {
		t.Fatalf("third = %+v, want held by the instance cap", got)
	}
}

// TestTriageWorkerDefersWhenUnavailable: without a model the row waits 30 minutes and keeps its
// attempts.
func TestTriageWorkerDefersWhenUnavailable(t *testing.T) {
	f := newTriageFixture(t)
	f.fixClock()
	f.switchTriage(t, true, f.team)
	message := f.receiveText(t, f.team, "off@sender.test", "Hello", "Text")
	f.service.triageModel = disabledTriageModel{}

	f.dispatchTriage(t)
	row := f.triageOf(t, message.ID)
	if row.Status != domain.MailTriagePending || row.Attempts != 0 || !row.NextAttemptAt.Equal(f.service.now().Add(30*time.Minute)) {
		t.Fatalf("row = %+v, want pending for 30 minutes", row)
	}

}

// TestTriageWorkerSkipsSwitchedOff: a row whose mailbox switch was turned off is skipped without
// a model call.
func TestTriageWorkerSkipsSwitchedOff(t *testing.T) {
	f := newTriageFixture(t)
	model := &recordingTriageModel{output: TriageOutput{Category: "fyi", Reason: "x"}}
	f.service.triageModel = model
	f.switchTriage(t, true, f.team)
	message := f.receiveText(t, f.team, "skip@sender.test", "Hello", "Text")
	if err := f.triageRepo().SetMailboxTriage(context.Background(), f.team.ID, false); err != nil {
		t.Fatal(err)
	}
	f.dispatchTriage(t)
	if got := f.triageOf(t, message.ID); got.Status != domain.MailTriageSkipped {
		t.Fatalf("row = %+v, want skipped", got)
	}
	if model.calls != 0 {
		t.Fatalf("model called %d times", model.calls)
	}
}

// TestTriageWorkerSendsOnlyMaskedInput: the code in the message and in an override example never
// reaches the model; the stored reason is masked too.
func TestTriageWorkerSendsOnlyMaskedInput(t *testing.T) {
	const code = "731946"
	f := newTriageFixture(t)
	model := &recordingTriageModel{output: TriageOutput{Category: "fyi", Reason: "Login code " + code}}
	f.service.triageModel = model
	f.switchTriage(t, true, f.team)
	earlier := f.receiveText(t, f.team, "earlier@sender.test", "Your code is "+code, "Old")
	if _, err := f.triageRepo().OverrideTriage(context.Background(), domain.MailTriageOverride{
		MessageID: earlier.ID, MailboxID: f.team.ID, Category: domain.MailTriageFYI, DecidedBy: accessB, At: f.service.now(),
	}); err != nil {
		t.Fatal(err)
	}
	message := f.receiveText(t, f.team, "code@sender.test", "Verification code "+code,
		"Your verification code is "+code+". Or open https://id.example.com/verify?token=zzz")

	f.dispatchTriage(t)
	if len(model.inputs) != 1 {
		t.Fatalf("model got %d inputs", len(model.inputs))
	}
	input := model.inputs[0]
	if len(input.Examples) != 1 {
		t.Fatalf("examples = %+v, want the override", input.Examples)
	}
	raw, _ := json.Marshal(input)
	for _, text := range []string{string(raw), input.Prompt()} {
		if strings.Contains(text, code) || strings.Contains(text, "token=zzz") {
			t.Fatalf("model input contains a secret: %s", text)
		}
	}
	if got := f.triageOf(t, message.ID); strings.Contains(got.Reason, code) {
		t.Fatalf("stored reason = %q", got.Reason)
	}
}
