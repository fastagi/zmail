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
	template, err := fixture.service.CreateTemplate(ctx, 1, testCreator, TemplateInput{
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
	broadcast, err := fixture.service.CreateBroadcast(ctx, 1, testCreator, input)
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	stored, err := fixture.service.GetBroadcast(ctx, 1, broadcast.ID)
	if err != nil || stored.Variables["code"] != "NOV20" || len(stored.Variables) != 3 {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	input.Variables = map[string]string{"code": "NOV25", "ends": "Sunday"}
	updated, err := fixture.service.UpdateBroadcast(ctx, 1, broadcast.ID, input)
	if err != nil || updated.Variables["code"] != "NOV25" || len(updated.Variables) != 2 {
		t.Fatalf("UpdateBroadcast = %+v, %v", updated, err)
	}

	if _, err = fixture.service.SendBroadcast(ctx, 1, broadcast.ID, nil); err != nil {
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
		if _, err := fixture.service.CreateBroadcast(context.Background(), 1, testCreator, input); !errors.Is(err, ErrMailBroadcastInvalid) {
			t.Fatalf("CreateBroadcast(%d variables) = %v; want invalid", len(variables), err)
		}
	}
	// No variables reads back as an empty object.
	broadcast := fixture.draft(t)
	if broadcast.Variables == nil || len(broadcast.Variables) != 0 {
		t.Fatalf("variables = %#v; want empty", broadcast.Variables)
	}
}
