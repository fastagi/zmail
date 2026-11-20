package mailbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

func TestTriageSettingNeedsOwnerOrAdmin(t *testing.T) {
	f := newTriageFixture(t)
	ctx := context.Background()
	input := TriageSettingInput{Enabled: true, DailyMessageCap: 200, DraftEnabled: true}

	if _, err := f.service.SaveTriageSetting(ctx, f.b, input); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("member save err = %v, want permission denied", err)
	}
	saved, err := f.service.SaveTriageSetting(ctx, f.admin, input)
	if err != nil {
		t.Fatalf("admin save: %v", err)
	}
	if !saved.Enabled || saved.DailyMessageCap != 200 || !saved.DraftEnabled || !saved.Available || saved.InstanceDailyCap != 100 {
		t.Fatalf("saved = %+v", saved)
	}
	read, err := f.service.TriageSetting(ctx, f.b)
	if err != nil || !read.Enabled || read.DailyMessageCap != 200 {
		t.Fatalf("member read = %+v, %v", read, err)
	}
	for _, limit := range []int{0, 100001} {
		_, err := f.service.SaveTriageSetting(ctx, f.admin, TriageSettingInput{Enabled: true, DailyMessageCap: limit})
		if _, ok := asValidationError(err); !ok {
			t.Fatalf("cap %d err = %v, want a validation error", limit, err)
		}
	}
}

func TestTriageSettingUnavailableWithoutProvider(t *testing.T) {
	f := newTriageFixture(t)
	ctx := context.Background()
	f.service.settings.Triage = config.AITriageConfig{}

	read, err := f.service.TriageSetting(ctx, f.admin)
	if err != nil || read.Available || read.Enabled {
		t.Fatalf("read = %+v, %v; want unavailable and off", read, err)
	}
	if _, err := f.service.SaveTriageSetting(ctx, f.admin, TriageSettingInput{Enabled: true, DailyMessageCap: 10}); !errors.Is(err, ErrTriageUnavailable) {
		t.Fatalf("enabling without a provider err = %v", err)
	}
	if _, err := f.service.SaveTriageSetting(ctx, f.admin, TriageSettingInput{DailyMessageCap: 10}); err != nil {
		t.Fatalf("turning off without a provider: %v", err)
	}
	if _, err := f.service.SetMailboxTriage(ctx, f.admin, f.team.ID, true); !errors.Is(err, ErrTriageUnavailable) {
		t.Fatalf("mailbox switch without a provider err = %v", err)
	}
}

// TestMailboxTriageSwitchPermissions: a personal mailbox's switch belongs to its owner alone; a
// shared mailbox's to its manage members and the organization's owners and admins.
func TestMailboxTriageSwitchPermissions(t *testing.T) {
	f := newTriageFixture(t)
	ctx := context.Background()

	if _, err := f.service.SetMailboxTriage(ctx, f.admin, f.personal.ID, true); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("admin on a personal mailbox err = %v", err)
	}
	mailbox, err := f.service.SetMailboxTriage(ctx, f.b, f.personal.ID, true)
	if err != nil || !mailbox.AITriageEnabled {
		t.Fatalf("owner = %+v, %v", mailbox, err)
	}
	for name, viewer := range map[string]domain.OrganizationContext{"send member": f.b, "view member": f.c} {
		if _, err := f.service.SetMailboxTriage(ctx, viewer, f.team.ID, true); !errors.Is(err, domain.ErrPermissionDenied) {
			t.Fatalf("%s on team@ err = %v", name, err)
		}
	}
	if _, err := f.service.SetMailboxTriage(ctx, f.d, f.team.ID, true); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("outsider err = %v", err)
	}
	mailbox, err = f.service.SetMailboxTriage(ctx, f.admin, f.team.ID, true)
	if err != nil || !mailbox.AITriageEnabled {
		t.Fatalf("admin on team@ = %+v, %v", mailbox, err)
	}
	mailbox, err = f.service.SetMailboxTriage(ctx, f.admin, f.team.ID, false)
	if err != nil || mailbox.AITriageEnabled {
		t.Fatalf("admin turning team@ off = %+v, %v", mailbox, err)
	}
}

func TestTriageOverridePermissionsAndValidation(t *testing.T) {
	f := newTriageFixture(t)
	ctx := context.Background()
	message := f.teamMessage

	if _, err := f.service.OverrideTriage(ctx, f.c, f.team.ID, message.ID, TriageOverrideInput{Category: "reply"}); !errors.Is(err, ErrTriageUnavailable) {
		t.Fatalf("override with the organization off err = %v", err)
	}
	f.switchTriage(t, true)

	triage, err := f.service.OverrideTriage(ctx, f.c, f.team.ID, message.ID, TriageOverrideInput{Category: "reply"})
	if err != nil {
		t.Fatalf("view member override: %v", err)
	}
	if triage.Category != domain.MailTriageReply || triage.Source != domain.MailTriageSourceManual ||
		triage.DecidedBy == nil || *triage.DecidedBy != accessC || triage.Status != domain.MailTriageDone {
		t.Fatalf("override = %+v", triage)
	}
	due := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	triage, err = f.service.OverrideTriage(ctx, f.c, f.team.ID, message.ID, TriageOverrideInput{Category: "deadline", DueAt: &due})
	if err != nil || triage.DueAt == nil || !triage.DueAt.Equal(due) {
		t.Fatalf("deadline override = %+v, %v", triage, err)
	}
	if _, err := f.service.OverrideTriage(ctx, f.d, f.team.ID, message.ID, TriageOverrideInput{Category: "fyi"}); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("outsider err = %v", err)
	}
	if _, err := f.service.OverrideTriage(ctx, f.c, f.personal.ID, f.personalMessage.ID, TriageOverrideInput{Category: "fyi"}); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("C on B's personal mailbox err = %v", err)
	}
	for name, input := range map[string]TriageOverrideInput{
		"review":                {Category: "review"},
		"unknown":               {Category: "spam"},
		"due on fyi":            {Category: "fyi", DueAt: &due},
		"due far in the future": {Category: "deadline", DueAt: new(due.AddDate(10, 0, 0))},
	} {
		if _, err := f.service.OverrideTriage(ctx, f.c, f.team.ID, message.ID, input); func() bool { _, ok := asValidationError(err); return !ok }() {
			t.Fatalf("%s err = %v, want a validation error", name, err)
		}
	}
	sent, _, err := f.service.SendMessage(ctx, f.b, f.team.ID, sendInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.OverrideTriage(ctx, f.b, f.team.ID, sent.ID, TriageOverrideInput{Category: "fyi"}); func() bool { _, ok := asValidationError(err); return !ok }() {
		t.Fatalf("outbound override err = %v, want a validation error", err)
	}
}

// TestReplyDraftNeedsSendAndIsNotStored: a draft needs send on the mailbox and the draft switch,
// reads only masked mail, and stores nothing.
func TestReplyDraftNeedsSendAndIsNotStored(t *testing.T) {
	const code = "264810"
	f := newTriageFixture(t)
	ctx := context.Background()
	model := &recordingTriageModel{draft: DraftOutput{Body: "Thanks!", Basis: []DraftBasis{{Kind: "message", ID: 1 << 30}}}}
	f.service.triageModel = model
	message := f.receiveText(t, f.team, "draft@sender.test", "Can we meet?", "Your code is "+code+". Can we meet on Friday?")
	model.draft.Basis = append(model.draft.Basis, DraftBasis{Kind: "message", ID: message.ID})

	if _, err := f.service.DraftReply(ctx, f.b, f.team.ID, message.ID); !errors.Is(err, ErrTriageUnavailable) {
		t.Fatalf("draft with the organization off err = %v", err)
	}
	f.switchTriage(t, true)
	if _, err := f.service.DraftReply(ctx, f.c, f.team.ID, message.ID); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("view-only draft err = %v", err)
	}
	db := f.repo.(*repository).db
	var messagesBefore, triageBefore int64
	db.Table("mail_messages").Count(&messagesBefore)
	db.Table("mail_triage").Count(&triageBefore)

	draft, err := f.service.DraftReply(ctx, f.b, f.team.ID, message.ID)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if draft.Body != "Thanks!" || len(draft.Basis) != 1 || draft.Basis[0].ID != message.ID || draft.Basis[0].Label != "Can we meet?" {
		t.Fatalf("draft = %+v", draft)
	}
	if len(model.drafts) != 1 || len(model.drafts[0].Messages) == 0 {
		t.Fatalf("model got %+v", model.drafts)
	}
	assertNoSecret(t, model.drafts[0], code)

	var messagesAfter, triageAfter int64
	db.Table("mail_messages").Count(&messagesAfter)
	db.Table("mail_triage").Count(&triageAfter)
	if messagesAfter != messagesBefore || triageAfter != triageBefore {
		t.Fatalf("a draft stored something: messages %d -> %d, triage %d -> %d", messagesBefore, messagesAfter, triageBefore, triageAfter)
	}

	if _, err := f.triageRepo().SaveTriageSettings(ctx, domain.MailTriageSettings{
		OrganizationID: accessOrg, Enabled: true, DailyMessageCap: 10, DraftEnabled: false,
	}, f.service.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DraftReply(ctx, f.b, f.team.ID, message.ID); !errors.Is(err, ErrTriageUnavailable) {
		t.Fatalf("draft with drafts off err = %v", err)
	}
	model.err = errors.New("provider down")
	model.draft.Body = strings.Repeat(" ", 3)
	f.switchTriage(t, true)
	if _, err := f.service.DraftReply(ctx, f.b, f.team.ID, message.ID); !errors.Is(err, ErrTriageModelFailed) {
		t.Fatalf("draft with a failing model err = %v", err)
	}
}
