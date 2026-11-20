package mailbox

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestParseInboxRequestTriage(t *testing.T) {
	request, err := parseInboxRequest(url.Values{"triage": {"reply,review"}})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !slices.Equal(request.Triage, []domain.MailTriageCategory{domain.MailTriageReply, domain.MailTriageReview}) {
		t.Fatalf("triage = %v", request.Triage)
	}
	if request, err = parseInboxRequest(url.Values{"triage": {"deadline"}, "view": {"today"}}); err != nil || len(request.Triage) != 1 {
		t.Fatalf("today with triage = %+v, %v", request, err)
	}
	for name, query := range map[string]url.Values{
		"unknown":     {"triage": {"spam"}},
		"empty":       {"triage": {""}},
		"repeated":    {"triage": {"reply,reply"}},
		"twice":       {"triage": {"reply", "fyi"}},
		"with group":  {"triage": {"reply"}, "group": {"thread"}},
		"sent view":   {"triage": {"reply"}, "view": {"sent"}},
		"empty item":  {"triage": {"reply,"}},
		"with search": {"triage": {"reply"}, "q": {"x"}},
	} {
		if _, err := parseInboxRequest(query); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want invalid input", name, err)
		}
	}
}

// triagedInbox switches triage on for team@, delivers a question and a notice, and classifies them.
func triagedInbox(t *testing.T) (*accessFixture, *domain.MailMessage, *domain.MailMessage) {
	t.Helper()
	f := newTriageFixture(t)
	f.switchTriage(t, true, f.team)
	question := f.receiveText(t, f.team, "inbox-q@sender.test", "Deck", "Can you send the deck?")
	notice := f.receiveText(t, f.team, "inbox-n@sender.test", "Newsletter", "This week in review.")
	f.dispatchTriage(t, 20)
	return f, question, notice
}

func triageInboxIDs(t *testing.T, f *accessFixture, viewer domain.OrganizationContext, triage ...domain.MailTriageCategory) ([]uint, map[uint]*domain.MailTriage) {
	t.Helper()
	page, err := f.service.ListInbox(context.Background(), viewer, InboxRequest{View: domain.InboxViewAll, PerPage: 50, Triage: triage})
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	ids := []uint{}
	results := map[uint]*domain.MailTriage{}
	for _, item := range page.Items {
		ids = append(ids, item.Message.ID)
		results[item.Message.ID] = item.Triage
	}
	return ids, results
}

func TestInboxTriageFilterFieldAndCounts(t *testing.T) {
	f, question, notice := triagedInbox(t)
	ctx := context.Background()

	ids, results := triageInboxIDs(t, f, f.b)
	if results[question.ID] == nil || results[question.ID].Category != domain.MailTriageReply || results[question.ID].Reason == "" {
		t.Fatalf("question row triage = %+v", results[question.ID])
	}
	if results[f.teamMessage.ID] != nil {
		t.Fatal("a message never triaged shows a result")
	}
	if len(ids) < 3 {
		t.Fatalf("unfiltered inbox = %v", ids)
	}
	if ids, _ = triageInboxIDs(t, f, f.b, domain.MailTriageReply); !slices.Equal(ids, []uint{question.ID}) {
		t.Fatalf("triage=reply = %v, want only the question", ids)
	}
	if ids, _ = triageInboxIDs(t, f, f.b, domain.MailTriageReply, domain.MailTriageFYI); len(ids) != 2 {
		t.Fatalf("triage=reply,fyi = %v", ids)
	}

	counts, err := f.service.InboxCounts(ctx, f.b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Triage == nil || *counts.Triage != (domain.MailTriageCounts{Reply: 1, FYI: 1}) {
		t.Fatalf("counts = %+v", counts.Triage)
	}
	// Counts are the viewer's unread mail: B reading the question leaves C's count alone.
	if _, err := f.service.SetMessageRead(ctx, f.b, f.team.ID, question.ID, true); err != nil {
		t.Fatal(err)
	}
	if counts, _ = f.service.InboxCounts(ctx, f.b, nil); counts.Triage.Reply != 0 {
		t.Fatalf("B's reply count after reading = %d", counts.Triage.Reply)
	}
	if counts, _ = f.service.InboxCounts(ctx, f.c, nil); counts.Triage.Reply != 1 {
		t.Fatalf("C's reply count = %d", counts.Triage.Reply)
	}

	// Results are shared by every reader: C's override is what B sees.
	if _, err := f.service.OverrideTriage(ctx, f.c, f.team.ID, notice.ID, TriageOverrideInput{Category: domain.MailTriageReply}); err != nil {
		t.Fatal(err)
	}
	if _, results = triageInboxIDs(t, f, f.b); results[notice.ID].Category != domain.MailTriageReply || results[notice.ID].Source != domain.MailTriageSourceManual {
		t.Fatalf("B sees %+v after C's override", results[notice.ID])
	}

	// D reads nothing of team@, so none of its triage.
	if ids, _ = triageInboxIDs(t, f, f.d, domain.MailTriageReply); len(ids) != 0 {
		t.Fatalf("D's triage=reply = %v", ids)
	}
}

func TestInboxTriageHiddenWhenOrganizationOff(t *testing.T) {
	f, question, _ := triagedInbox(t)
	f.switchTriage(t, false)

	_, results := triageInboxIDs(t, f, f.b)
	if results[question.ID] != nil {
		t.Fatalf("triage shown with the organization off: %+v", results[question.ID])
	}
	if ids, _ := triageInboxIDs(t, f, f.b, domain.MailTriageReply); len(ids) != 0 {
		t.Fatalf("triage filter with the organization off = %v", ids)
	}
	counts, err := f.service.InboxCounts(context.Background(), f.b, nil)
	if err != nil || counts.Triage != nil {
		t.Fatalf("counts.Triage = %+v, %v; want nil", counts.Triage, err)
	}
}
