package mailbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
)

// fakePlatformDirectory stands in for the organization starter's aggregates.
type fakePlatformDirectory struct {
	totals domain.PlatformMemberTotals
	rows   []domain.PlatformOrganizationRow
	err    error
}

func (f *fakePlatformDirectory) PlatformMemberTotals(context.Context) (domain.PlatformMemberTotals, error) {
	return f.totals, f.err
}

func (f *fakePlatformDirectory) PlatformOrganizations(_ context.Context, query string) ([]domain.PlatformOrganizationRow, error) {
	var rows []domain.PlatformOrganizationRow
	for _, row := range f.rows {
		if query == "" || strings.Contains(strings.ToLower(row.Name), strings.ToLower(query)) {
			rows = append(rows, row)
		}
	}
	return rows, f.err
}

func (f *fakePlatformDirectory) PlatformOrganizationNames(_ context.Context, ids []uint) (map[uint]string, error) {
	names := map[uint]string{}
	for _, row := range f.rows {
		for _, id := range ids {
			if row.ID == id {
				names[id] = row.Name
			}
		}
	}
	return names, f.err
}

var metricsDay = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

type metricsFixture struct {
	handler *OperatorMetricsHandler
	repo    *repository
	sentA   uint
	sentB   uint
	failed  uint
	north   *domain.Mailbox
	south   *domain.Mailbox
}

func storePlatformMessage(t *testing.T, repo *repository, mailboxID uint, direction, status, subject string, at time.Time) uint {
	t.Helper()
	row := &MailMessagePO{
		MailboxID: mailboxID, Direction: direction, Status: status, FromAddress: "team@mail.example",
		Subject: subject, TextBody: "secret body " + subject, HTMLBody: "<p>secret</p>",
		ToAddresses: `["customer@example.com"]`, CreatedAt: at, UpdatedAt: at,
	}
	require.NoError(t, repo.db.Create(row).Error)
	return row.ID
}

// newMetricsFixture builds spec acceptance row 2: on one day organization 1 sends two messages,
// fails one, and receives one; organization 2 sends nothing.
func newMetricsFixture(t *testing.T) *metricsFixture {
	t.Helper()
	repo, db := openRepository(t)
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	createOwner(t, db, 3)
	north := createMailbox(t, repo, "north@mail.example")
	south := &domain.Mailbox{OrganizationID: 2, Address: "south@mail.example", Type: domain.MailboxTypeShared}
	require.NoError(t, repo.CreateMailbox(context.Background(), south, domain.MailboxesPerOrganizationCeiling))

	f := &metricsFixture{repo: repo, north: north, south: south}
	f.sentA = storePlatformMessage(t, repo, north.ID, "outbound", "sent", "Launch", metricsDay.Add(9*time.Hour))
	f.sentB = storePlatformMessage(t, repo, north.ID, "outbound", "sent", "Follow-up", metricsDay.Add(15*time.Hour))
	f.failed = storePlatformMessage(t, repo, north.ID, "outbound", "failed", "Bounce me", metricsDay.Add(10*time.Hour))
	storePlatformMessage(t, repo, north.ID, "inbound", "received", "Hello", metricsDay.Add(11*time.Hour))

	directory := &fakePlatformDirectory{
		totals: domain.PlatformMemberTotals{Organizations: 3, Members: 4, DisabledMembers: 1},
		rows: []domain.PlatformOrganizationRow{
			{ID: 1, Name: "North", Members: 2},
			{ID: 2, Name: "South", Members: 3},
			{ID: 3, Name: "Quiet", Members: 1},
		},
	}
	f.handler = NewOperatorMetricsHandler(repo, directory)
	f.handler.now = func() time.Time { return metricsDay.Add(20 * time.Hour) }
	return f
}

func TestOperatorMetricsCountsOneDayOfOutboundMail(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()

	overview, err := f.handler.overview(ctx, metricsDay, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(3), overview.Organizations)
	assert.Equal(t, int64(4), overview.Members)
	assert.Equal(t, int64(1), overview.DisabledMembers)
	assert.Equal(t, int64(2), overview.Mailboxes)
	assert.Equal(t, OperatorMailCounts{Sent: 2, Failed: 1}, overview.Day, "inbound mail is never counted")
	require.Len(t, overview.Trend, 7)
	assert.Equal(t, "2026-11-12", overview.Trend[0].Date)
	assert.Equal(t, OperatorMailTrendDay{Date: "2026-11-18", Sent: 2, Failed: 1}, overview.Trend[6])
	assert.Equal(t, OperatorMailTrendDay{Date: "2026-11-17"}, overview.Trend[5], "empty days are listed")

	rows, total, err := f.handler.organizations(ctx, metricsDay, "", 1, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Equal(t, []OperatorMailOrganization{
		{ID: 1, Name: "North", Members: 2, Mailboxes: 1, Sent: 2, Failed: 1},
		{ID: 2, Name: "South", Members: 3, Mailboxes: 1},
		{ID: 3, Name: "Quiet", Members: 1},
	}, rows, "zero-send organizations are kept, after senders, by ID")

	sent, total, err := f.handler.outbound(ctx, outboundFilter{Day: metricsDay, Status: "sent"}, 1, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, sent, 2)
	assert.Equal(t, f.sentB, sent[0].ID, "newest first")
	assert.Equal(t, f.sentA, sent[1].ID)
	assert.Equal(t, "North", sent[0].OrganizationName)
	assert.Equal(t, "north@mail.example", sent[0].MailboxAddress)
	assert.Equal(t, "Follow-up", sent[0].Subject)

	all, total, err := f.handler.outbound(ctx, outboundFilter{Day: metricsDay, OrganizationID: 1}, 1, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total, "the log lists outbound mail only")
	assert.Len(t, all, 3)
	none, total, err := f.handler.outbound(ctx, outboundFilter{Day: metricsDay, MailboxID: f.south.ID}, 1, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, none)
}

// Spec acceptance row 3 (count part): two delivered receipts for one message change no count.
func TestOperatorMetricsIgnoreDuplicateReceipts(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()
	for _, id := range []string{"evt-1", "evt-2"} {
		providerID := id
		require.NoError(t, f.repo.db.Create(&MailMessageEventPO{
			MessageID: f.sentA, Type: "delivered", Recipient: "customer@example.com",
			ProviderEventID: &providerID, OccurredAt: metricsDay.Add(9 * time.Hour),
		}).Error)
	}
	require.NoError(t, f.repo.db.Create(&MailMessageEventPO{
		MessageID: f.failed, Type: "bounced", OccurredAt: metricsDay.Add(10 * time.Hour),
	}).Error)

	overview, err := f.handler.overview(ctx, metricsDay, 1)
	require.NoError(t, err)
	assert.Equal(t, OperatorMailCounts{Sent: 2, Failed: 1, Delivered: 1, Bounced: 1}, overview.Day)
	require.Len(t, overview.Trend, 1)
	assert.Equal(t, 2, overview.Trend[0].Sent)
	rows, _, err := f.handler.organizations(ctx, metricsDay, "north", 1, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 2, rows[0].Sent)
}

// Spec acceptance row 5 (count part): one message on each side of UTC midnight lands on two days.
func TestOperatorMetricsSplitDaysAtUTCMidnight(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()
	next := metricsDay.Add(24 * time.Hour)
	storePlatformMessage(t, f.repo, f.south.ID, "outbound", "sent", "Before midnight", next.Add(-time.Second))
	storePlatformMessage(t, f.repo, f.south.ID, "outbound", "queued", "After midnight", next)
	f.handler.now = func() time.Time { return next.Add(time.Hour) }

	overview, err := f.handler.overview(ctx, next, 2)
	require.NoError(t, err)
	assert.Equal(t, OperatorMailCounts{Queued: 1}, overview.Day)
	assert.Equal(t, []OperatorMailTrendDay{
		{Date: "2026-11-18", Sent: 3, Failed: 1},
		{Date: "2026-11-19", Queued: 1},
	}, overview.Trend)
	rows, _, err := f.handler.organizations(ctx, next, "", 1, 20)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, uint(1), rows[0].ID, "with no sent mail the order falls back to organization ID")
	assert.Equal(t, OperatorMailOrganization{ID: 2, Name: "South", Members: 3, Mailboxes: 1, Queued: 1}, rows[1])
}

func TestOperatorMetricsExcludeDeletedMailboxesAndSyncedCopies(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()
	synced := storePlatformMessage(t, f.repo, f.south.ID, "outbound", "sent", "Synced", metricsDay.Add(12*time.Hour))
	require.NoError(t, f.repo.db.Model(&MailMessagePO{}).Where("id = ?", synced).Update("source", "gmail").Error)
	gone := &domain.Mailbox{OrganizationID: 2, Address: "gone@mail.example", Type: domain.MailboxTypeShared}
	require.NoError(t, f.repo.CreateMailbox(ctx, gone, domain.MailboxesPerOrganizationCeiling))
	storePlatformMessage(t, f.repo, gone.ID, "outbound", "sent", "Gone", metricsDay.Add(12*time.Hour))
	require.NoError(t, f.repo.DeleteMailbox(ctx, 2, gone.ID))
	// A member's connected account is not one of the organization's mailboxes.
	owner := uint(1)
	connected := &domain.Mailbox{OrganizationID: 2, Address: "member@gmail.example", Type: domain.MailboxTypePersonal,
		Origin: domain.MailboxOriginConnected, OwnerUserID: &owner}
	require.NoError(t, f.repo.CreateMailbox(ctx, connected, domain.MailboxesPerOrganizationCeiling))

	overview, err := f.handler.overview(ctx, metricsDay, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, overview.Day.Sent)
	assert.Equal(t, int64(2), overview.Mailboxes)
	counts, err := f.repo.platformMailboxCounts(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts[2], "the connected mailbox is not counted")
}

func TestOperatorMetricsHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newMetricsFixture(t)
	engine := gin.New()
	engine.GET("/overview", f.handler.Overview)
	engine.GET("/organizations", f.handler.Organizations)
	engine.GET("/messages", f.handler.OutboundMessages)
	get := func(path string) (int, map[string]any) {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		return recorder.Code, body
	}

	code, body := get("/overview")
	require.Equal(t, http.StatusOK, code)
	data := body["data"].(map[string]any)
	assert.Equal(t, "2026-11-18", data["date"], "the default day is today in UTC")
	assert.Len(t, data["trend"], 7)

	code, body = get("/messages?date=2026-11-18&status=sent&per_page=1")
	require.Equal(t, http.StatusOK, code)
	rows := body["data"].([]any)
	require.Len(t, rows, 1)
	row := rows[0].(map[string]any)
	for _, field := range []string{"text_body", "html_body", "to_addresses", "body", "text", "html"} {
		assert.NotContains(t, row, field, "the log never returns content")
	}
	assert.EqualValues(t, 2, body["meta"].(map[string]any)["total"])

	code, body = get("/organizations?date=2026-11-18")
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 20, body["meta"].(map[string]any)["per_page"], "pages hold 20 rows by default")

	for _, path := range []string{
		"/overview?date=2026-11-19",   // future
		"/overview?date=18-11-2026",   // not a date
		"/overview?days=32",           // over 31
		"/overview?days=0",            //
		"/messages?status=received",   // inbound is not in the log
		"/messages?organization_id=0", //
		"/organizations?q=" + strings.Repeat("x", 101),
	} {
		code, body = get(path)
		assert.Equal(t, http.StatusBadRequest, code, path)
		assert.Equal(t, "COMMON.INVALID_INPUT", body["error_code"], path)
	}
	assert.Equal(t, "private, no-store", func() string {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/overview", nil))
		return recorder.Header().Get("Cache-Control")
	}())
}

func TestOperatorMetricsReportDirectoryFailure(t *testing.T) {
	f := newMetricsFixture(t)
	f.handler.organizationsDirectory = &fakePlatformDirectory{err: domain.ErrServiceUnavailable}
	_, err := f.handler.overview(context.Background(), metricsDay, 7)
	require.ErrorIs(t, err, domain.ErrServiceUnavailable, "a failed aggregate is an error, never a zero")
}
