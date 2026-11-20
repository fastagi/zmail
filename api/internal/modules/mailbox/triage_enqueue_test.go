package mailbox

import (
	"context"
	"fmt"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// TestTriageEnqueueNeedsBothSwitches: an inbound message is queued for triage only when a model
// is configured and both the organization switch and the mailbox switch are on.
func TestTriageEnqueueNeedsBothSwitches(t *testing.T) {
	f := newTriageFixture(t)
	next := 0
	deliver := func() *domain.MailMessage {
		next++
		return f.receiveSigned(t, f.team, fmt.Sprintf("enqueue-%d@sender.test", next))
	}

	if f.triageOf(t, deliver().ID) != nil {
		t.Fatal("queued with every switch off")
	}
	f.switchTriage(t, true)
	if f.triageOf(t, deliver().ID) != nil {
		t.Fatal("queued with the mailbox switch off")
	}
	f.switchTriage(t, false, f.team)
	if f.triageOf(t, deliver().ID) != nil {
		t.Fatal("queued with the organization switch off")
	}
	f.switchTriage(t, true, f.team)
	queued := f.triageOf(t, deliver().ID)
	if queued == nil || queued.Status != domain.MailTriagePending || queued.MailboxID != f.team.ID || queued.Category != "" {
		t.Fatalf("queued = %+v, want a pending row of team@", queued)
	}

	// Without a configured model nothing is queued, whatever the switches say.
	f.service.settings.Triage = config.AITriageConfig{}
	if f.triageOf(t, deliver().ID) != nil {
		t.Fatal("queued without a model")
	}
}

// TestTriageEnqueueSkipsFormerOwnersMailbox: the owner's consent covers a personal mailbox only
// while the owner is a member.
func TestTriageEnqueueSkipsFormerOwnersMailbox(t *testing.T) {
	f := newTriageFixture(t)
	f.switchTriage(t, true, f.personal)
	if f.triageOf(t, f.receiveSigned(t, f.personal, "owner-1@sender.test").ID) == nil {
		t.Fatal("the owner's mailbox with both switches on was not queued")
	}
	f.leave(t, f.b, tick())
	if f.triageOf(t, f.receiveSigned(t, f.personal, "owner-2@sender.test").ID) != nil {
		t.Fatal("queued for a personal mailbox whose owner left")
	}
}

// TestHandoverTurnsTriageOff: the new shared mailbox does not inherit the previous owner's
// consent.
func TestHandoverTurnsTriageOff(t *testing.T) {
	f := newTriageFixture(t)
	f.switchTriage(t, true, f.personal)
	f.leave(t, f.b, tick())
	handed := f.handOver(t, f.personal.ID)
	if handed.AITriageEnabled {
		t.Fatal("the handed-over mailbox kept AI triage on")
	}
	mailbox, err := f.repo.FindMailbox(context.Background(), accessOrg, f.personal.ID)
	if err != nil || mailbox.AITriageEnabled {
		t.Fatalf("stored switch = %v, err %v", mailbox != nil && mailbox.AITriageEnabled, err)
	}
}
