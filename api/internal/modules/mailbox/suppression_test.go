package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

func (f *txHandlerFixture) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	if err := f.db.Exec(statement, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

func (f *txHandlerFixture) captureAudit() *[]auditstarter.Change {
	audited := &[]auditstarter.Change{}
	f.service.recordAudit = func(_ context.Context, change auditstarter.Change) {
		*audited = append(*audited, change)
	}
	return audited
}

func (f *txHandlerFixture) suppress(t *testing.T, organizationID uint, email, reason string, at time.Time) uint {
	t.Helper()
	if err := f.repo.AddSuppression(context.Background(), domain.MailSuppression{
		OrganizationID: organizationID, Email: email, Reason: reason,
		Source: domain.MailSuppressionSourceTransactional, CreatedAt: at,
	}); err != nil {
		t.Fatalf("AddSuppression: %v", err)
	}
	var id uint
	if err := f.db.Raw(`SELECT id FROM mail_suppressions WHERE organization_id = ? AND email = ?`,
		organizationID, normalizeEmail(email)).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("find suppression: %v", err)
	}
	return id
}

func (f *txHandlerFixture) suppressionCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := f.db.Raw(`SELECT count(*) FROM mail_suppressions`).Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return count
}

func TestMemberCannotReadLaneUsage(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	id := f.suppress(t, 10, "gone@example.com", domain.MailSuppressionReasonBounced, f.clock)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v1/mail-send-lanes"},
		{http.MethodGet, "/v1/mail-suppressions"},
		{http.MethodDelete, fmt.Sprintf("/v1/mail-suppressions/%d", id)},
	} {
		recorder, body := f.request(route.method, route.path, "", "", "")
		if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
			t.Fatalf("%s %s = %d %s; want 403", route.method, route.path, recorder.Code, recorder.Body.String())
		}
	}
	member := viewerOf(10, txUser, domain.OrganizationRoleMember)
	if _, _, err := f.service.ListSendLanes(context.Background(), member); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("ListSendLanes(member) = %v", err)
	}
	if _, _, err := f.service.ListSuppressions(context.Background(), member, domain.MailSuppressionFilter{}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("ListSuppressions(member) = %v", err)
	}
	if err := f.service.RemoveSuppression(context.Background(), member, id); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("RemoveSuppression(member) = %v", err)
	}
	if f.suppressionCount(t) != 1 {
		t.Fatal("a member removed a suppression")
	}
}

func TestLaneUsageCountsEachLane(t *testing.T) {
	f := newTxHandlerFixture(t, func(settings *Settings) {
		settings.DailySendLimit = 50
		settings.DailyBroadcastLimit = 70
		settings.DailyTransactionalLimit = 90
	})
	inWindow := f.clock.Add(-time.Hour)
	outOfWindow := f.clock.Add(-25 * time.Hour)

	// Transactional: two attempts in the window, one before it, one waiting for room, and one
	// attempt of another organization.
	sent := terminalAssignments + `status = 'sent', send_attempted_at = '` + inWindow.Format(time.RFC3339Nano) + `'`
	f.seedLog(t, nil, sent)
	f.seedLog(t, nil, sent)
	f.seedLog(t, nil, terminalAssignments+`status = 'failed', send_attempted_at = '`+outOfWindow.Format(time.RFC3339Nano)+`'`)
	f.seedLog(t, nil, `queued_reason = 'send_limit'`)
	f.seedLog(t, func(message *domain.TransactionalMessage) { message.OrganizationID = 11 }, sent)

	// Personal and broadcast mail of organization 10's mailbox, and one message of organization 11.
	f.exec(t, `INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at)
		VALUES (501, 10, 901, 'team@mail.example', now(), now()), (502, 11, 901, 'other@mail.example', now(), now())`)
	message := func(mailbox uint, lane, status string, attempted *time.Time, queued string) {
		var queuedReason any
		if queued != "" {
			queuedReason = queued
		}
		f.exec(t, `INSERT INTO mail_messages (mailbox_id, direction, status, from_address, send_lane, send_attempted_at, queued_reason, created_at, updated_at)
			VALUES (?, 'outbound', ?, 'team@mail.example', ?, ?, ?, now(), now())`, mailbox, status, lane, attempted, queuedReason)
	}
	message(501, "personal", "sent", &inWindow, "")
	message(501, "personal", "failed", &inWindow, "")
	message(501, "personal", "sent", &outOfWindow, "")
	message(501, "personal", "queued", nil, "send_limit")
	message(501, "broadcast", "sent", &inWindow, "")
	message(502, "personal", "sent", &inWindow, "")

	// Broadcast waiting: pending recipients of broadcasts paused by the lane only.
	broadcast := func(id, organization uint, stopReason string) {
		f.exec(t, `INSERT INTO mail_broadcasts (id, organization_id, name, mailbox_id, template_id, list_id, status, stop_reason, created_by, created_at, updated_at)
			VALUES (?, ?, 'b', 501, 1, 1, 'sending', ?, 901, now(), now())`, id, organization, stopReason)
	}
	recipient := func(broadcastID, contactID uint, status string) {
		f.exec(t, `INSERT INTO mail_broadcast_recipients (broadcast_id, contact_id, email, status, created_at, updated_at)
			VALUES (?, ?, 'r@example.com', ?, now(), now())`, broadcastID, contactID, status)
	}
	broadcast(601, 10, "send_limit_reached")
	recipient(601, 1, "pending")
	recipient(601, 2, "pending")
	recipient(601, 3, "pending")
	recipient(601, 4, "sent")
	broadcast(602, 10, "")
	recipient(602, 1, "pending")
	broadcast(603, 11, "send_limit_reached")
	recipient(603, 1, "pending")

	f.role = domain.OrganizationRoleAdmin
	recorder, body := f.request(http.MethodGet, "/v1/mail-send-lanes", "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET lanes = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	data, _ := body["data"].(map[string]any)
	if data["window_hours"] != float64(24) {
		t.Errorf("window_hours = %v", data["window_hours"])
	}
	if computed, _ := data["computed_at"].(string); computed != f.clock.UTC().Format(time.RFC3339Nano) {
		t.Errorf("computed_at = %v; want %s", data["computed_at"], f.clock.UTC().Format(time.RFC3339Nano))
	}
	lanes, _ := data["lanes"].([]any)
	want := []map[string]any{
		{"lane": "transactional", "limit": float64(90), "used": float64(2), "waiting": float64(1)},
		{"lane": "broadcast", "limit": float64(70), "used": float64(1), "waiting": float64(3)},
		{"lane": "personal", "limit": float64(50), "used": float64(2), "waiting": float64(1)},
	}
	if len(lanes) != len(want) {
		t.Fatalf("lanes = %v", data["lanes"])
	}
	for i, expected := range want {
		lane, _ := lanes[i].(map[string]any)
		if len(lane) != len(expected) {
			t.Errorf("lane %d = %v; want exactly %v", i, lane, expected)
		}
		for field, value := range expected {
			if lane[field] != value {
				t.Errorf("lane %d %s = %v; want %v", i, field, lane[field], value)
			}
		}
	}
}

func TestListSuppressionsFiltersAndPages(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	f.role = domain.OrganizationRoleOwner
	first := f.suppress(t, 10, "a@example.com", domain.MailSuppressionReasonBounced, f.clock.Add(-3*time.Minute))
	second := f.suppress(t, 10, "b@example.com", domain.MailSuppressionReasonComplained, f.clock.Add(-2*time.Minute))
	third := f.suppress(t, 10, "c@example.com", domain.MailSuppressionReasonBounced, f.clock.Add(-time.Minute))
	f.suppress(t, 11, "a@example.com", domain.MailSuppressionReasonBounced, f.clock)

	list := func(query string) ([]map[string]any, map[string]any) {
		t.Helper()
		recorder, body := f.request(http.MethodGet, "/v1/mail-suppressions"+query, "", "", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", query, recorder.Code, recorder.Body.String())
		}
		raw, _ := body["data"].([]any)
		items := make([]map[string]any, 0, len(raw))
		for _, item := range raw {
			entry, _ := item.(map[string]any)
			items = append(items, entry)
		}
		meta, _ := body["meta"].(map[string]any)
		return items, meta
	}

	items, meta := list("?per_page=2")
	if !sameIDs(itemIDs(items), third, second) || meta["next_cursor"] != strconv.Itoa(int(second)) || len(meta) != 1 {
		t.Fatalf("page 1 = %v %v", itemIDs(items), meta)
	}
	if entry := items[0]; entry["email"] != "c@example.com" || entry["reason"] != "bounced" ||
		entry["source"] != "transactional" || entry["created_at"] == nil || len(entry) != 5 {
		t.Fatalf("entry = %v", entry)
	}
	items, meta = list("?per_page=2&cursor=" + meta["next_cursor"].(string))
	if !sameIDs(itemIDs(items), first) || meta["next_cursor"] != nil {
		t.Fatalf("page 2 = %v %v", itemIDs(items), meta)
	}
	if items, _ = list("?email=%20A@Example.COM%20"); !sameIDs(itemIDs(items), first) {
		t.Fatalf("email filter = %v", itemIDs(items))
	}
	if items, _ = list("?reason=complained"); !sameIDs(itemIDs(items), second) {
		t.Fatalf("reason filter = %v", itemIDs(items))
	}
	for _, query := range []string{"?reason=rejected", "?per_page=101", "?per_page=0", "?cursor=x", "?email=" + strings.Repeat("a", 321)} {
		recorder, body := f.request(http.MethodGet, "/v1/mail-suppressions"+query, "", "", "")
		if recorder.Code != http.StatusBadRequest || body["error_code"] != domain.CodeInvalidInput {
			t.Errorf("GET %s = %d %s; want 400", query, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRemoveSuppressionIsIdempotentAndAudited(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	f.role = domain.OrganizationRoleAdmin
	audited := f.captureAudit()
	id := f.suppress(t, 10, "gone@example.com", domain.MailSuppressionReasonComplained, f.clock)
	path := fmt.Sprintf("/v1/mail-suppressions/%d", id)
	for attempt := 1; attempt <= 2; attempt++ {
		recorder, _ := f.request(http.MethodDelete, path, "", "", "")
		if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
			t.Fatalf("delete %d = %d %s", attempt, recorder.Code, recorder.Body.String())
		}
	}
	if f.suppressionCount(t) != 0 {
		t.Fatal("the suppression is still listed")
	}
	if len(*audited) != 1 {
		t.Fatalf("audited = %d records; want 1", len(*audited))
	}
	change := (*audited)[0]
	if change.Action != "mail_suppression.removed" || change.TargetType != "mail_suppression" ||
		change.TargetID != strconv.Itoa(int(id)) || change.Result != domain.AuditResultSuccess {
		t.Fatalf("audit = %+v", change)
	}
	if reason := change.Changes["reason"]; reason.Before != "complained" || reason.After != nil {
		t.Fatalf("audit reason = %+v", change.Changes)
	}
	for field, value := range change.Changes {
		if value.Before == "gone@example.com" || value.After == "gone@example.com" {
			t.Fatalf("audit %s holds the address", field)
		}
	}
}

func TestRemoveSuppressionOtherOrganizationDiscloseNothing(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	f.role = domain.OrganizationRoleOwner
	audited := f.captureAudit()
	other := f.suppress(t, 11, "gone@example.com", domain.MailSuppressionReasonBounced, f.clock)
	for _, id := range []uint{other, other + 1000} {
		recorder, _ := f.request(http.MethodDelete, fmt.Sprintf("/v1/mail-suppressions/%d", id), "", "", "")
		if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
			t.Fatalf("delete %d = %d %s", id, recorder.Code, recorder.Body.String())
		}
	}
	if f.suppressionCount(t) != 1 {
		t.Fatal("another organization's suppression was removed")
	}
	if len(*audited) != 0 {
		t.Fatalf("audited = %+v; a removal that deleted nothing records nothing", *audited)
	}
}

func TestRemovedAddressCanReceiveTransactionalAgain(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	id := f.suppress(t, 10, "lilei@example.com", domain.MailSuppressionReasonBounced, f.clock)
	f.exec(t, `INSERT INTO mail_contacts (organization_id, email, status, status_reason, created_at, updated_at)
		VALUES (10, 'lilei@example.com', 'suppressed', 'bounced', now(), now())`)

	blocked, _ := f.send(t, liveActor(), codeInput("before", " LiLei@Example.COM ", "111111"))
	if blocked.Status != domain.TransactionalStatusSuppressed {
		t.Fatalf("status before removal = %s; want suppressed", blocked.Status)
	}
	if err := f.service.RemoveSuppression(context.Background(), viewerOf(10, txUser, domain.OrganizationRoleOwner), id); err != nil {
		t.Fatalf("RemoveSuppression: %v", err)
	}
	again, _ := f.send(t, liveActor(), codeInput("after", "lilei@example.com", "222222"))
	if again.Status == domain.TransactionalStatusSuppressed {
		t.Fatalf("status after removal = %s", again.Status)
	}
	var status string
	if err := f.db.Raw(`SELECT status FROM mail_contacts WHERE organization_id = 10 AND email = 'lilei@example.com'`).Scan(&status).Error; err != nil {
		t.Fatalf("contact: %v", err)
	}
	if status != string(domain.MailContactSuppressed) {
		t.Fatalf("contact status = %s; removal must leave it unchanged", status)
	}
}

func TestLaneUsageReportsTheNewOrganizationCap(t *testing.T) {
	f := newTxHandlerFixture(t, func(settings *Settings) {
		settings.DailySendLimit = 50
		settings.NewOrganizationDailySendLimit = 5
		settings.NewOrganizationPeriod = 7 * 24 * time.Hour
	})
	f.exec(t, `UPDATE organizations SET created_at = ? WHERE id = 10`, f.clock.Add(-time.Hour))
	f.role = domain.OrganizationRoleAdmin
	limits := func() []float64 {
		recorder, body := f.request(http.MethodGet, "/v1/mail-send-lanes", "", "", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET lanes = %d %s", recorder.Code, recorder.Body.String())
		}
		data, _ := body["data"].(map[string]any)
		lanes, _ := data["lanes"].([]any)
		out := []float64{}
		for _, lane := range lanes {
			entry, _ := lane.(map[string]any)
			limit, _ := entry["limit"].(float64)
			out = append(out, limit)
		}
		return out
	}
	if got := limits(); len(got) != 3 || got[0] != 5 || got[1] != 5 || got[2] != 5 {
		t.Fatalf("limits of a new organization = %v; want the cap 5 in every lane", got)
	}
	f.exec(t, `UPDATE organizations SET created_at = ? WHERE id = 10`, f.clock.Add(-8*24*time.Hour))
	if got := limits(); len(got) != 3 || got[0] != 50 || got[1] != 50 || got[2] != 50 {
		t.Fatalf("limits after the new-organization period = %v; want the lane limits", got)
	}
}
