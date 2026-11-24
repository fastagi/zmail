package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func newAssignmentFixture(t *testing.T) *accessFixture {
	t.Helper()
	repo, db := openRepository(t)
	createOwner(t, db, accessOrg)
	transport := &fakeTransport{}
	memberships := newAccessMemberships()
	svc := NewService(repo, memberships, repo, repo, repo, repo,
		nil, nil, nil, transport, &fakeNotifier{}, accessSettings(), nil, nil, nil, nil)
	return seedAccessFixture(t, svc, repo, transport, memberships)
}

func TestSharedThreadAssignmentNeedsSendAndASendingAssignee(t *testing.T) {
	f := newAssignmentFixture(t)
	ctx := context.Background()
	thread := f.teamMessage.ThreadID
	if thread == 0 {
		t.Fatal("team message has no thread")
	}

	fresh, err := f.service.MailThreadAssignment(ctx, f.c, f.team.ID, thread)
	if err != nil || fresh.Status != domain.MailThreadOpen || fresh.AssigneeUserID != nil || fresh.UpdatedAt != nil {
		t.Fatalf("untouched = %+v, %v", fresh, err)
	}
	if len(fresh.AssignableUserIDs) != 1 || fresh.AssignableUserIDs[0] != f.b.UserID {
		t.Fatalf("assignable = %v; want only B, who may send", fresh.AssignableUserIDs)
	}

	b := f.b.UserID
	if _, err = f.service.SaveMailThreadAssignment(ctx, f.c, f.team.ID, thread,
		MailThreadAssignmentInput{AssigneeUserID: &b, Status: domain.MailThreadOpen}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("viewer assigns = %v; want permission denied", err)
	}
	c := f.c.UserID
	var invalid *ValidationError
	if _, err = f.service.SaveMailThreadAssignment(ctx, f.b, f.team.ID, thread,
		MailThreadAssignmentInput{AssigneeUserID: &c, Status: "snoozed"}); !errors.As(err, &invalid) || len(invalid.Fields) != 2 {
		t.Fatalf("view-only assignee and bad status = %v; want two field errors", err)
	}

	saved, err := f.service.SaveMailThreadAssignment(ctx, f.b, f.team.ID, thread,
		MailThreadAssignmentInput{AssigneeUserID: &b, Status: domain.MailThreadClosed})
	if err != nil || saved.AssigneeUserID == nil || *saved.AssigneeUserID != b || saved.Status != domain.MailThreadClosed ||
		saved.UpdatedBy == nil || *saved.UpdatedBy != b || saved.UpdatedAt == nil {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	cleared, err := f.service.SaveMailThreadAssignment(ctx, f.b, f.team.ID, thread,
		MailThreadAssignmentInput{Status: domain.MailThreadOpen})
	if err != nil || cleared.AssigneeUserID != nil || cleared.Status != domain.MailThreadOpen {
		t.Fatalf("unassign = %+v, %v", cleared, err)
	}
	read, err := f.service.MailThreadAssignment(ctx, f.c, f.team.ID, thread)
	if err != nil || read.AssigneeUserID != nil || read.Status != domain.MailThreadOpen || read.UpdatedAt == nil {
		t.Fatalf("read back = %+v, %v", read, err)
	}

	// C may only view, so C cannot be newly assigned; a thread already on C still changes status.
	if _, err = f.service.assignments.SaveMailThreadAssignment(ctx, domain.MailThreadAssignment{
		MailboxID: f.team.ID, ThreadID: thread, AssigneeUserID: &c, Status: domain.MailThreadOpen,
	}, f.service.now()); err != nil {
		t.Fatalf("seed former assignee: %v", err)
	}
	if kept, keepErr := f.service.SaveMailThreadAssignment(ctx, f.b, f.team.ID, thread,
		MailThreadAssignmentInput{AssigneeUserID: &c, Status: domain.MailThreadClosed}); keepErr != nil ||
		kept.AssigneeUserID == nil || *kept.AssigneeUserID != c || kept.Status != domain.MailThreadClosed {
		t.Fatalf("close a thread kept on a former assignee = %+v, %v", kept, keepErr)
	}

	if _, err = f.service.MailThreadAssignment(ctx, f.b, f.team.ID, thread+1000); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("missing thread = %v; want not found", err)
	}
	if _, err = f.service.MailThreadAssignment(ctx, f.b, f.personal.ID, f.personalMessage.ThreadID); !errors.Is(err, ErrMailThreadAssignmentUnavailable) {
		t.Fatalf("personal mailbox = %v; want unavailable", err)
	}
	if _, err = f.service.MailThreadAssignment(ctx, f.d, f.team.ID, thread); err == nil {
		t.Fatal("outsider read an assignment")
	}
}

func TestUpdateMailThreadAssignmentRequestTellsNullFromOmitted(t *testing.T) {
	cases := map[string]struct {
		body  string
		set   bool
		value uint
	}{
		"omitted": {body: `{"status":"open"}`},
		"null":    {body: `{"assignee_user_id":null,"status":"open"}`, set: true},
		"id":      {body: `{"assignee_user_id":7,"status":"open"}`, set: true, value: 7},
	}
	for name, tc := range cases {
		var req UpdateMailThreadAssignmentRequest
		if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := uint(0)
		if req.AssigneeUserID.Value != nil {
			got = *req.AssigneeUserID.Value
		}
		if req.AssigneeUserID.Set != tc.set || got != tc.value {
			t.Fatalf("%s = %+v; want set=%v value=%d", name, req.AssigneeUserID, tc.set, tc.value)
		}
	}
	var req UpdateMailThreadAssignmentRequest
	if err := json.Unmarshal([]byte(`{"assignee_user_id":"7","status":"open"}`), &req); err == nil {
		t.Fatal("a string assignee was accepted")
	}
}
