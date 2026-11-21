package mailbox

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestBroadcastVariablesFillEveryRecipient(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	template, err := fixture.service.CreateTemplate(ctx, as(1), TemplateInput{
		Name:    "Sale",
		Subject: "{{first_name|Hi}}, use {{code}}",
		HTML:    "<p>Code {{code}} until {{ends}} for {{plan}}.</p>",
		Text:    "Code {{code}} until {{ends}} for {{plan}}.",
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	fixture.contact(t, "ann@example.com", "Ann", domain.MailContactSubscribed, true)
	// Bob carries his own code, which beats the shared one.
	if _, err = fixture.service.CreateContact(ctx, 1, ContactInput{
		Email: "bob@example.com", FirstName: "Bob", ListIDs: []uint{fixture.list.ID},
		Properties: map[string]string{"code": "BOB50", "plan": "team"},
	}); err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	input := BroadcastInput{
		Name: "Sale", MailboxID: fixture.mailbox.ID, TemplateID: template.ID, ListID: fixture.list.ID,
		Variables: map[string]string{"code": "NOV20", "ends": "Friday", "plan": "everyone"},
	}
	broadcast, err := fixture.service.CreateBroadcast(ctx, as(1), input)
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	stored, err := fixture.service.GetBroadcast(ctx, as(1), broadcast.ID)
	if err != nil || stored.Variables["code"] != "NOV20" || len(stored.Variables) != 3 {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	input.Variables = map[string]string{"code": "NOV25", "ends": "Sunday"}
	updated, err := fixture.service.UpdateBroadcast(ctx, as(1), broadcast.ID, input)
	if err != nil || updated.Variables["code"] != "NOV25" || len(updated.Variables) != 2 {
		t.Fatalf("UpdateBroadcast = %+v, %v", updated, err)
	}

	if _, err = fixture.service.SendBroadcast(ctx, as(1), broadcast.ID, nil); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}
	processed, err := fixture.service.DispatchBroadcasts(ctx, 10)
	if err != nil || processed != 2 {
		t.Fatalf("DispatchBroadcasts = %d, %v", processed, err)
	}
	if len(fixture.transport.sent) != 2 {
		t.Fatalf("provider calls = %d; want 2", len(fixture.transport.sent))
	}
	got := map[string]string{}
	for _, message := range fixture.transport.sent {
		got[message.To[0]] = message.Subject + " | " + message.TextBody
	}
	if !strings.HasPrefix(got["ann@example.com"], "Ann, use NOV25 | Code NOV25 until Sunday for pro.") {
		t.Fatalf("ann = %q", got["ann@example.com"])
	}
	if !strings.HasPrefix(got["bob@example.com"], "Bob, use BOB50 | Code BOB50 until Sunday for team.") {
		t.Fatalf("bob = %q", got["bob@example.com"])
	}
}

func TestBroadcastVariablesAreChecked(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	tooMany := map[string]string{}
	for i := range maxTemplateVariables + 1 {
		tooMany["v"+strconv.Itoa(i)] = "1"
	}
	for _, variables := range []map[string]string{
		{"first_name": "Ann"},
		{"email": "a@example.com"},
		{"unsubscribe_url": "https://evil.example"},
		{"1code": "x"},
		{"code": strings.Repeat("x", maxTemplateVariableValue+1)},
		tooMany,
	} {
		input := BroadcastInput{
			Name: "Sale", MailboxID: fixture.mailbox.ID, TemplateID: fixture.template.ID, ListID: fixture.list.ID,
			Variables: variables,
		}
		if _, err := fixture.service.CreateBroadcast(context.Background(), as(1), input); !errors.Is(err, ErrMailBroadcastInvalid) {
			t.Fatalf("CreateBroadcast(%d variables) = %v; want invalid", len(variables), err)
		}
	}
	// No variables reads back as an empty object.
	broadcast := fixture.draft(t)
	if broadcast.Variables == nil || len(broadcast.Variables) != 0 {
		t.Fatalf("variables = %#v; want empty", broadcast.Variables)
	}
}

// Issue 78: a variable no recipient can fill is refused when sending starts, naming it, and the
// draft stays a draft.
func TestBroadcastStartRefusesVariablesNoRecipientHas(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	template, err := fixture.service.CreateTemplate(ctx, as(1), TemplateInput{
		Name:    "Welcome",
		Subject: "Hi {{name}}",
		HTML:    "<p>Hello {{name}}, plan {{plan}}, code {{code}}, from {{city|anywhere}}, {{first_name}}.</p>",
		Text:    "Hello {{name}}, plan {{plan}}, code {{code}}.",
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	// Every fixture contact has the plan property; none has name.
	fixture.contact(t, "ann@example.com", "Ann", domain.MailContactSubscribed, true)
	fixture.contact(t, "bob@example.com", "", domain.MailContactSubscribed, true)
	broadcast, err := fixture.service.CreateBroadcast(ctx, as(1), BroadcastInput{
		Name: "Welcome", MailboxID: fixture.mailbox.ID, TemplateID: template.ID, ListID: fixture.list.ID,
		Variables: map[string]string{"code": "NOV20"},
	})
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}

	_, err = fixture.service.SendBroadcast(ctx, as(1), broadcast.ID, nil)
	var missing *MissingVariablesError
	if !errors.As(err, &missing) || strings.Join(missing.Names, ",") != "name" {
		t.Fatalf("SendBroadcast = %v; want the missing variable name", err)
	}
	stored, err := fixture.service.GetBroadcast(ctx, as(1), broadcast.ID)
	if err != nil || stored.Status != domain.MailBroadcastDraft || stored.Counts.Total != 0 {
		t.Fatalf("stored = %+v, %v; want an untouched draft", stored, err)
	}
	if len(fixture.transport.sent) != 0 {
		t.Fatalf("provider calls = %d; want none", len(fixture.transport.sent))
	}

	// Once the broadcast supplies it, sending starts.
	if _, err = fixture.service.UpdateBroadcast(ctx, as(1), broadcast.ID, BroadcastInput{
		Name: "Welcome", MailboxID: fixture.mailbox.ID, TemplateID: template.ID, ListID: fixture.list.ID,
		Variables: map[string]string{"code": "NOV20", "name": "friend"},
	}); err != nil {
		t.Fatalf("UpdateBroadcast: %v", err)
	}
	if _, err = fixture.service.SendBroadcast(ctx, as(1), broadcast.ID, nil); err != nil {
		t.Fatalf("SendBroadcast with the variable: %v", err)
	}
}

// Issue 78: a broadcast in which every attempted recipient failed does not end as sent.
func TestBroadcastWhereEveryRecipientFailedIsNotSent(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	fixture.contact(t, "ann@example.com", "Ann", domain.MailContactSubscribed, true)
	fixture.contact(t, "bob@example.com", "Bob", domain.MailContactSubscribed, true)
	fixture.transport.bounces["ann@example.com"] = true
	fixture.transport.bounces["bob@example.com"] = true
	broadcast := fixture.draft(t)
	if _, err := fixture.service.SendBroadcast(ctx, as(1), broadcast.ID, nil); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	stored, err := fixture.service.GetBroadcast(ctx, as(1), broadcast.ID)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	if stored.Counts.Failed != 2 || stored.Counts.Sent != 0 || stored.Counts.Pending != 0 {
		t.Fatalf("counts = %+v; want two failed", stored.Counts)
	}
	if stored.Status != domain.MailBroadcastCanceled || stored.StopReason != stopReasonAllFailed || stored.FinishedAt == nil {
		t.Fatalf("status = %s %q %v; want canceled %q with finished_at", stored.Status, stored.StopReason, stored.FinishedAt, stopReasonAllFailed)
	}
}
