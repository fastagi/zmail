package mailbox

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// logPath builds a send log URL with the given query.
func logPath(query url.Values) string {
	if len(query) == 0 {
		return "/v1/transactional-mail-log"
	}
	return "/v1/transactional-mail-log?" + query.Encode()
}

// logItems lists a log page and returns its entries and meta, failing on any answer but 200.
func (f *txHandlerFixture) logItems(t *testing.T, query url.Values) ([]map[string]any, map[string]any) {
	t.Helper()
	recorder, body := f.request(http.MethodGet, logPath(query), "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", logPath(query), recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
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

func itemIDs(items []map[string]any) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, uint(item["id"].(float64)))
	}
	return ids
}

func sameIDs(got []uint, want ...uint) bool {
	if len(got) != len(want) {
		return false
	}
	sorted := append([]uint(nil), want...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] > sorted[j] })
	for i := range got {
		if got[i] != sorted[i] {
			return false
		}
	}
	return true
}

// seedLog stores a live message of organization 10 created at the fixture's clock, then applies
// the given SQL assignments, which are test constants, to it.
func (f *txHandlerFixture) seedLog(t *testing.T, edit func(*domain.TransactionalMessage), assignments string) *domain.TransactionalMessage {
	t.Helper()
	message := newTransactional(f.clock)
	if edit != nil {
		edit(message)
	}
	if _, err := f.repo.CreateTransactional(context.Background(), message); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if assignments != "" {
		if err := f.db.Exec(`UPDATE transactional_messages SET `+assignments+` WHERE id = ?`, message.ID).Error; err != nil {
			t.Fatalf("edit seed: %v", err)
		}
	}
	return message
}

const terminalAssignments = `payload_ciphertext = NULL, next_attempt_at = NULL, queued_reason = NULL, lease_until = NULL, `

func TestLogMasksRecipientAndOmitsVariables(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	sent, _ := f.send(t, liveActor(), codeInput("log-mask", " LiLei@Example.COM ", "482913"))

	recorder, _ := f.request(http.MethodGet, logPath(nil), "", "", "")
	if strings.Contains(recorder.Body.String(), "lilei@example.com") || strings.Contains(recorder.Body.String(), "482913") {
		t.Fatalf("the log leaks the recipient or a secret: %s", recorder.Body.String())
	}
	items, meta := f.logItems(t, nil)
	if !sameIDs(itemIDs(items), sent.ID) {
		t.Fatalf("items = %v; want [%d]", itemIDs(items), sent.ID)
	}
	entry := items[0]
	wantKeys := []string{"id", "mode", "simulated", "template", "to_masked", "subject", "status", "queued_reason",
		"failure_code", "last_event", "idempotency_key", "created_at", "sent_at"}
	if len(entry) != len(wantKeys) {
		t.Fatalf("entry keys = %v; want exactly %v", entry, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := entry[key]; !ok {
			t.Fatalf("entry lacks %s: %v", key, entry)
		}
	}
	if entry["to_masked"] != "li••@example.com" || entry["subject"] != "Acme verification code: ••••••" ||
		entry["mode"] != "live" || entry["template"] != "signup-code" || entry["idempotency_key"] != "log-mask" {
		t.Fatalf("entry = %v", entry)
	}
	if meta["next_cursor"] != nil || meta["total_24h"] != float64(1) || len(meta) != 2 {
		t.Fatalf("meta = %v", meta)
	}

	detail, body := f.request(http.MethodGet, "/v1/transactional-mail-log/"+strconv.Itoa(int(sent.ID)), "", "", "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
	if strings.Contains(detail.Body.String(), "lilei@example.com") || strings.Contains(detail.Body.String(), "482913") {
		t.Fatalf("the detail leaks the recipient or a secret: %s", detail.Body.String())
	}
	data, _ := body["data"].(map[string]any)
	if _, ok := data["to"]; ok {
		t.Fatalf("detail has the full recipient: %v", data)
	}
	if len(data) != len(wantKeys)+3 {
		t.Fatalf("detail keys = %v", data)
	}
	variables, _ := data["variables"].(map[string]any)
	if variables["code"] != "••••••" || variables["app"] != "Acme" {
		t.Fatalf("variables = %v", data["variables"])
	}
	if from, _ := data["from"].(map[string]any); from["address"] != "no-reply@mail.example" {
		t.Fatalf("from = %v", data["from"])
	}
	if _, ok := data["events"].([]any); !ok {
		t.Fatalf("events = %v", data["events"])
	}
}

func TestLogFiltersByExactRecipientCaseInsensitive(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	lilei, _ := f.send(t, liveActor(), codeInput("to-1", " LiLei@Example.COM ", "111111"))
	f.send(t, liveActor(), codeInput("to-2", "lilei@example.co", "222222"))
	simulated, _ := f.send(t, liveActor(), codeInput("to-3", "Bounced+u1@TEST.zmail.im", "333333"))

	cases := []struct {
		to   string
		want []uint
	}{
		{" LILEI@example.COM ", []uint{lilei.ID}},
		{"lilei@example.com", []uint{lilei.ID}},
		{"lilei@example", nil},
		{"BOUNCED+u1@test.ZMAIL.im", []uint{simulated.ID}},
	}
	for _, tc := range cases {
		items, _ := f.logItems(t, url.Values{"to": {tc.to}})
		if !sameIDs(itemIDs(items), tc.want...) {
			t.Errorf("to=%q: items = %v; want %v", tc.to, itemIDs(items), tc.want)
		}
	}
}

func TestLogOutcomeProblemIntersectsStatus(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	bounced := f.seedLog(t, nil, terminalAssignments+`status = 'sent', last_event = 'bounced'`)
	failed := f.seedLog(t, nil, terminalAssignments+`status = 'failed', failure_code = 'transport.unknown_outcome'`)
	suppressed := f.seedLog(t, nil, terminalAssignments+`status = 'suppressed'`)
	delivered := f.seedLog(t, nil, terminalAssignments+`status = 'sent', last_event = 'delivered'`)
	queued := f.seedLog(t, nil, "")

	cases := []struct {
		query url.Values
		want  []uint
	}{
		{url.Values{}, []uint{bounced.ID, failed.ID, suppressed.ID, delivered.ID, queued.ID}},
		{url.Values{"outcome": {"problem"}}, []uint{bounced.ID, failed.ID, suppressed.ID}},
		{url.Values{"outcome": {"delivered"}}, []uint{delivered.ID}},
		{url.Values{"outcome": {"problem"}, "status": {"failed"}}, []uint{failed.ID}},
		{url.Values{"outcome": {"problem"}, "status": {"sent"}}, []uint{bounced.ID}},
		{url.Values{"outcome": {"problem"}, "last_event": {"bounced"}}, []uint{bounced.ID}},
		{url.Values{"outcome": {"delivered"}, "status": {"failed"}}, nil},
		{url.Values{"outcome": {"problem"}, "last_event": {"delivered"}}, nil},
		{url.Values{"status": {"queued"}}, []uint{queued.ID}},
	}
	for _, tc := range cases {
		items, _ := f.logItems(t, tc.query)
		if !sameIDs(itemIDs(items), tc.want...) {
			t.Errorf("%s: items = %v; want %v", tc.query.Encode(), itemIDs(items), tc.want)
		}
	}
}

func TestLogRangeOverThirtyDaysRefused(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	now := f.clock
	recent := f.seedLog(t, nil, "")
	old := f.seedLog(t, func(m *domain.TransactionalMessage) { m.CreatedAt = now.Add(-8 * 24 * time.Hour) }, "")
	test := f.seedLog(t, func(m *domain.TransactionalMessage) { m.Mode = domain.APIKeyModeTest }, "")

	// The default range is the last 7 days, live mode.
	if items, _ := f.logItems(t, nil); !sameIDs(itemIDs(items), recent.ID) {
		t.Fatalf("default items = %v; want [%d]", itemIDs(items), recent.ID)
	}
	if items, _ := f.logItems(t, url.Values{"mode": {"test"}}); !sameIDs(itemIDs(items), test.ID) {
		t.Fatalf("test items = %v; want [%d]", itemIDs(items), test.ID)
	}
	stamp := func(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }
	// Exactly 30 days is allowed.
	thirty := url.Values{"since": {stamp(now.Add(-30*24*time.Hour + time.Second))}, "until": {stamp(now.Add(time.Second))}}
	if items, _ := f.logItems(t, thirty); !sameIDs(itemIDs(items), recent.ID, old.ID) {
		t.Fatalf("30-day items = %v; want [%d %d]", itemIDs(items), recent.ID, old.ID)
	}
	// since is inclusive and until exclusive.
	exact := url.Values{"since": {stamp(old.CreatedAt)}, "until": {stamp(recent.CreatedAt)}}
	if items, _ := f.logItems(t, exact); !sameIDs(itemIDs(items), old.ID) {
		t.Fatalf("bounded items = %v; want [%d]", itemIDs(items), old.ID)
	}

	long := strings.Repeat("a", 316) + "@x.io"
	refused := []url.Values{
		{"since": {stamp(now.Add(-31 * 24 * time.Hour))}, "until": {stamp(now)}},
		{"since": {stamp(now.Add(-31 * 24 * time.Hour))}},
		{"until": {stamp(now.Add(-40 * 24 * time.Hour))}, "since": {stamp(now.Add(-71 * 24 * time.Hour))}},
		{"since": {stamp(now)}, "until": {stamp(now.Add(-time.Hour))}},
		{"since": {"yesterday"}},
		{"until": {"2026-13-01T00:00:00Z"}},
		{"mode": {"sandbox"}},
		{"status": {"delivered"}},
		{"last_event": {"accepted"}},
		{"outcome": {"bounced"}},
		{"template": {"Signup Code"}},
		{"to": {long}},
		{"per_page": {"0"}},
		{"per_page": {"101"}},
		{"per_page": {"ten"}},
		{"cursor": {"abc"}},
		{"cursor": {"0"}},
		{"cursor": {"-3"}},
	}
	for _, query := range refused {
		recorder, body := f.request(http.MethodGet, logPath(query), "", "", "")
		if recorder.Code != http.StatusBadRequest || body["error_code"] != domain.CodeInvalidInput {
			t.Errorf("%s = %d %v; want 400 %s", query.Encode(), recorder.Code, body["error_code"], domain.CodeInvalidInput)
		}
	}
}

func TestLogPagesWithCursor(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	first := f.seedLog(t, nil, "")
	second := f.seedLog(t, nil, "")
	third := f.seedLog(t, nil, "")

	items, meta := f.logItems(t, url.Values{"per_page": {"2"}})
	if !sameIDs(itemIDs(items), third.ID, second.ID) {
		t.Fatalf("page 1 = %v", itemIDs(items))
	}
	cursor, ok := meta["next_cursor"].(string)
	if !ok || cursor != strconv.Itoa(int(second.ID)) {
		t.Fatalf("next_cursor = %#v", meta["next_cursor"])
	}
	items, meta = f.logItems(t, url.Values{"per_page": {"2"}, "cursor": {cursor}})
	if !sameIDs(itemIDs(items), first.ID) || meta["next_cursor"] != nil {
		t.Fatalf("page 2 = %v, meta %v", itemIDs(items), meta)
	}
	// An empty cursor asks for the newest page.
	if items, _ = f.logItems(t, url.Values{"cursor": {""}}); len(items) != 3 {
		t.Fatalf("empty cursor = %v", itemIDs(items))
	}
}

func TestLogTotal24hCapped(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	seed := f.seedLog(t, func(m *domain.TransactionalMessage) { m.CreatedAt = f.clock.Add(-time.Hour) },
		terminalAssignments+`status = 'sent'`)
	f.seedLog(t, func(m *domain.TransactionalMessage) { m.CreatedAt = f.clock.Add(-25 * time.Hour) }, "")
	f.seedLog(t, func(m *domain.TransactionalMessage) { m.Mode = domain.APIKeyModeTest }, "")

	if _, meta := f.logItems(t, url.Values{"per_page": {"1"}}); meta["total_24h"] != float64(1) {
		t.Fatalf("total_24h = %v; want 1", meta["total_24h"])
	}
	if err := f.db.Exec(`INSERT INTO transactional_messages (organization_id, mode, simulated, created_by, template_key,
			from_address, from_name, to_address, subject_masked, variables_masked, idempotency_fingerprint, status,
			attempts, rfc_message_id, failure_code, created_at, updated_at)
		SELECT organization_id, mode, simulated, created_by, template_key, from_address, from_name, to_address,
			subject_masked, variables_masked, idempotency_fingerprint, status, attempts, 'cap' || g || '@acme.test',
			failure_code, created_at, updated_at
		FROM transactional_messages, generate_series(1, 10000) AS g WHERE id = ?`, seed.ID).Error; err != nil {
		t.Fatalf("bulk seed: %v", err)
	}
	// The count ignores every filter but the mode and template, and stops at 10000.
	items, meta := f.logItems(t, url.Values{"per_page": {"1"}, "status": {"failed"}})
	if len(items) != 0 || meta["total_24h"] != float64(10000) {
		t.Fatalf("items %d, total_24h = %v; want 0 and 10000", len(items), meta["total_24h"])
	}
	if _, meta = f.logItems(t, url.Values{"per_page": {"1"}, "mode": {"test"}}); meta["total_24h"] != float64(1) {
		t.Fatalf("test total_24h = %v; want 1", meta["total_24h"])
	}
}

func TestLogTotal24hHonorsTemplateOnly(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	f.seedLog(t, nil, "")
	f.seedLog(t, nil, terminalAssignments+`status = 'failed'`)
	f.seedLog(t, func(m *domain.TransactionalMessage) { m.TemplateKey = "welcome" }, "")
	f.seedLog(t, func(m *domain.TransactionalMessage) {
		m.TemplateKey = "welcome"
		m.Mode = domain.APIKeyModeTest
	}, "")

	if _, meta := f.logItems(t, url.Values{"per_page": {"1"}}); meta["total_24h"] != float64(3) {
		t.Fatalf("mode total_24h = %v; want 3", meta["total_24h"])
	}
	// The template narrows the count; status still does not.
	items, meta := f.logItems(t, url.Values{"per_page": {"1"}, "template": {"welcome"}, "status": {"failed"}})
	if len(items) != 0 || meta["total_24h"] != float64(1) {
		t.Fatalf("welcome items %d, total_24h = %v; want 0 and 1", len(items), meta["total_24h"])
	}
	if _, meta = f.logItems(t, url.Values{"per_page": {"1"}, "template": {"signup-code"}}); meta["total_24h"] != float64(2) {
		t.Fatalf("signup-code total_24h = %v; want 2", meta["total_24h"])
	}
	if _, meta = f.logItems(t, url.Values{"mode": {"test"}, "template": {"welcome"}}); meta["total_24h"] != float64(1) {
		t.Fatalf("test welcome total_24h = %v; want 1", meta["total_24h"])
	}
}

func TestLogEntryOtherOrganizationNotFound(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	other := f.seedLog(t, func(m *domain.TransactionalMessage) { m.OrganizationID = 11 }, "")
	test := f.seedLog(t, func(m *domain.TransactionalMessage) { m.Mode = domain.APIKeyModeTest }, "")

	recorder, body := f.request(http.MethodGet, "/v1/transactional-mail-log/"+strconv.Itoa(int(other.ID)), "", "", "")
	if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailTransactionalMessageNotFound {
		t.Fatalf("other organization = %d %v", recorder.Code, body["error_code"])
	}
	recorder, body = f.request(http.MethodGet, "/v1/transactional-mail-log/999999", "", "", "")
	if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailTransactionalMessageNotFound {
		t.Fatalf("unknown = %d %v", recorder.Code, body["error_code"])
	}
	if items, _ := f.logItems(t, nil); len(items) != 0 {
		t.Fatalf("the log lists another organization's mail: %v", itemIDs(items))
	}
	// The console reads either mode.
	if recorder, _ = f.request(http.MethodGet, "/v1/transactional-mail-log/"+strconv.Itoa(int(test.ID)), "", "", ""); recorder.Code != http.StatusOK {
		t.Fatalf("test-mode entry = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestTransactionalMailStaysOutOfMailboxLogAndStats(t *testing.T) {
	f := newTxHandlerFixture(t, nil)
	read := func(path string) string {
		recorder, _ := f.request(http.MethodGet, path, "", "", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}
	logBefore, statsBefore := read("/v1/mail-messages"), read("/v1/mail-message-stats")
	f.send(t, liveActor(), codeInput("stay-out", "lilei@example.com", "482913"))
	if after := read("/v1/mail-messages"); after != logBefore {
		t.Fatalf("mail log changed: %s -> %s", logBefore, after)
	}
	if after := read("/v1/mail-message-stats"); after != statsBefore {
		t.Fatalf("mail stats changed: %s -> %s", statsBefore, after)
	}
	var stored int64
	if err := f.db.Raw(`SELECT count(*) FROM mail_messages`).Scan(&stored).Error; err != nil || stored != 0 {
		t.Fatalf("mail_messages = %d, %v; want 0", stored, err)
	}
}
