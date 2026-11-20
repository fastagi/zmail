package mailbox

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

const (
	// operatorMetricsMaxDays bounds the overview trend.
	operatorMetricsMaxDays = 31
	// operatorMetricsDefaultDays is the overview trend length when none is asked for.
	operatorMetricsDefaultDays = 7
	// operatorMetricsPerPage is the default page size of the organization list and the log.
	operatorMetricsPerPage  = 20
	operatorMetricsMaxQuery = 100
	operatorDateLayout      = "2006-01-02"
)

// OperatorMailCounts is one UTC day of counted outbound mail. Sent, Failed, and Queued count each
// message once under its current status; the receipt counts are a separate view of the same
// messages.
type OperatorMailCounts struct {
	Sent       int `json:"sent"`
	Failed     int `json:"failed"`
	Queued     int `json:"queued"`
	Delivered  int `json:"delivered"`
	Bounced    int `json:"bounced"`
	Complained int `json:"complained"`
}

// OperatorMailTrendDay is one day of the overview trend.
type OperatorMailTrendDay struct {
	Date   string `json:"date"`
	Sent   int    `json:"sent"`
	Failed int    `json:"failed"`
	Queued int    `json:"queued"`
}

// OperatorMailMetrics is the platform mail overview.
type OperatorMailMetrics struct {
	Date            string                 `json:"date"`
	Organizations   int64                  `json:"organizations"`
	Members         int64                  `json:"members"`
	DisabledMembers int64                  `json:"disabled_members"`
	Mailboxes       int64                  `json:"mailboxes"`
	Day             OperatorMailCounts     `json:"day"`
	Trend           []OperatorMailTrendDay `json:"trend"`
}

// OperatorMailOrganization is one organization's row for one day.
type OperatorMailOrganization struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Members   int64  `json:"members"`
	Mailboxes int64  `json:"mailboxes"`
	Sent      int    `json:"sent"`
	Failed    int    `json:"failed"`
	Queued    int    `json:"queued"`
}

// OperatorOutboundMessage is one outbound log row. It carries metadata only.
type OperatorOutboundMessage struct {
	ID               uint       `json:"id"`
	OrganizationID   uint       `json:"organization_id"`
	OrganizationName string     `json:"organization_name"`
	MailboxID        uint       `json:"mailbox_id"`
	MailboxAddress   string     `json:"mailbox_address"`
	FromAddress      string     `json:"from_address"`
	Subject          string     `json:"subject"`
	Status           string     `json:"status"`
	FailureCode      string     `json:"failure_code"`
	LastEvent        string     `json:"last_event"`
	CreatedAt        time.Time  `json:"created_at"`
	SendAttemptedAt  *time.Time `json:"send_attempted_at"`
	// BroadcastID names the organization broadcast that created the message, or is null. Only
	// such messages have a body an operator with mail_content:read may read.
	BroadcastID *uint `json:"broadcast_id"`
}

type platformMailStore interface {
	platformDailyCounts(ctx context.Context, from, to time.Time) ([]platformDayCounts, error)
	platformReceiptCounts(ctx context.Context, from, to time.Time) (platformReceiptCounts, error)
	platformOrganizationCounts(ctx context.Context, from, to time.Time) (map[uint]platformStatusCounts, error)
	platformMailboxCounts(ctx context.Context) (map[uint]int64, error)
	platformOutbound(ctx context.Context, filter outboundFilter, page, perPage int) ([]platformOutboundRow, int64, error)
	readBroadcastContent(ctx context.Context, request broadcastContentRequest) (*broadcastContentRow, error)
	contentAccessAudits(ctx context.Context, filter contentAuditFilter, page, perPage int) ([]MailContentAccessAuditPO, int64, error)
}

// OperatorMetricsHandler serves platform-wide mail metrics and the outbound log to the starter that
// owns operator authorization. It applies no membership check and must only be mounted behind it.
// Mail is aggregated here and memberships by the organization starter; the two results are joined
// by organization ID, so membership counts never multiply mail counts.
type OperatorMetricsHandler struct {
	store                  platformMailStore
	organizationsDirectory domain.PlatformOrganizationDirectory
	now                    func() time.Time
	// errors maps refused content reads; nil uses response.DefaultErrorMapper.
	errors *response.ErrorMapper
}

// NewOperatorMetricsHandler creates the operator-facing mail metrics handler.
func NewOperatorMetricsHandler(repo *repository, organizations domain.PlatformOrganizationDirectory) *OperatorMetricsHandler {
	return &OperatorMetricsHandler{store: repo, organizationsDirectory: organizations, now: time.Now}
}

// Overview returns the platform totals, one day's counts, and the daily trend ending on that day.
func (h *OperatorMetricsHandler) Overview(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	day, ok := h.bindDay(c)
	if !ok {
		return
	}
	days := operatorMetricsDefaultDays
	if raw := c.Query("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > operatorMetricsMaxDays {
			invalidMetricsInput(c)
			return
		}
		days = parsed
	}
	overview, err := h.overview(c.Request.Context(), day, days)
	if err != nil {
		response.HandleError(c, "Failed to load mail metrics", err)
		return
	}
	response.Success(c, overview)
}

// Organizations lists every organization matching q with its counts for one day.
func (h *OperatorMetricsHandler) Organizations(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	day, ok := h.bindDay(c)
	if !ok {
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	page, perPage, pageOK := bindMetricsPage(c)
	if !pageOK || len([]rune(query)) > operatorMetricsMaxQuery {
		invalidMetricsInput(c)
		return
	}
	rows, total, err := h.organizations(c.Request.Context(), day, query, page, perPage)
	if err != nil {
		response.HandleError(c, "Failed to list organization mail metrics", err)
		return
	}
	paginator := pagination.NewPaginator(rows, total, page, perPage)
	paginator.SetPath(c.Request.URL.Path).WithQuery(c.Request.URL.Query())
	response.Success(c, paginator)
}

// OutboundMessages lists one day of the platform outbound log, newest first.
func (h *OperatorMetricsHandler) OutboundMessages(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	day, ok := h.bindDay(c)
	if !ok {
		return
	}
	filter := outboundFilter{Day: day, Status: c.Query("status")}
	switch domain.MailStatus(filter.Status) {
	case "", domain.MailStatusSent, domain.MailStatusFailed, domain.MailStatusQueued:
	default:
		invalidMetricsInput(c)
		return
	}
	var idsOK bool
	if filter.OrganizationID, idsOK = optionalPositiveID(c.Query("organization_id")); !idsOK {
		invalidMetricsInput(c)
		return
	}
	if filter.MailboxID, idsOK = optionalPositiveID(c.Query("mailbox_id")); !idsOK {
		invalidMetricsInput(c)
		return
	}
	page, perPage, pageOK := bindMetricsPage(c)
	if !pageOK {
		invalidMetricsInput(c)
		return
	}
	rows, total, err := h.outbound(c.Request.Context(), filter, page, perPage)
	if err != nil {
		response.HandleError(c, "Failed to list the outbound log", err)
		return
	}
	paginator := pagination.NewPaginator(rows, total, page, perPage)
	paginator.SetPath(c.Request.URL.Path).WithQuery(c.Request.URL.Query())
	response.Success(c, paginator)
}

func (h *OperatorMetricsHandler) overview(ctx context.Context, day time.Time, days int) (*OperatorMailMetrics, error) {
	totals, err := h.organizationsDirectory.PlatformMemberTotals(ctx)
	if err != nil {
		return nil, err
	}
	mailboxes, err := h.store.platformMailboxCounts(ctx)
	if err != nil {
		return nil, err
	}
	first := day.AddDate(0, 0, -(days - 1))
	end := day.AddDate(0, 0, 1)
	daily, err := h.store.platformDailyCounts(ctx, first, end)
	if err != nil {
		return nil, err
	}
	receipts, err := h.store.platformReceiptCounts(ctx, day, end)
	if err != nil {
		return nil, err
	}
	byDay := make(map[time.Time]platformStatusCounts, len(daily))
	for _, row := range daily {
		byDay[row.Day] = row.Counts
	}
	overview := &OperatorMailMetrics{
		Date:            day.Format(operatorDateLayout),
		Organizations:   totals.Organizations,
		Members:         totals.Members,
		DisabledMembers: totals.DisabledMembers,
		Trend:           make([]OperatorMailTrendDay, 0, days),
	}
	for _, count := range mailboxes {
		overview.Mailboxes += count
	}
	for current := first; current.Before(end); current = current.AddDate(0, 0, 1) {
		counts := byDay[current]
		overview.Trend = append(overview.Trend, OperatorMailTrendDay{
			Date: current.Format(operatorDateLayout), Sent: counts.Sent, Failed: counts.Failed, Queued: counts.Queued,
		})
	}
	selected := byDay[day]
	overview.Day = OperatorMailCounts{
		Sent: selected.Sent, Failed: selected.Failed, Queued: selected.Queued,
		Delivered: receipts.Delivered, Bounced: receipts.Bounced, Complained: receipts.Complained,
	}
	return overview, nil
}

func (h *OperatorMetricsHandler) organizations(
	ctx context.Context,
	day time.Time,
	query string,
	page, perPage int,
) ([]OperatorMailOrganization, int64, error) {
	directory, err := h.organizationsDirectory.PlatformOrganizations(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	mail, err := h.store.platformOrganizationCounts(ctx, day, day.AddDate(0, 0, 1))
	if err != nil {
		return nil, 0, err
	}
	mailboxes, err := h.store.platformMailboxCounts(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows := make([]OperatorMailOrganization, len(directory))
	for index, organization := range directory {
		counts := mail[organization.ID]
		rows[index] = OperatorMailOrganization{
			ID: organization.ID, Name: organization.Name, Members: organization.Members,
			Mailboxes: mailboxes[organization.ID], Sent: counts.Sent, Failed: counts.Failed, Queued: counts.Queued,
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Sent != rows[j].Sent {
			return rows[i].Sent > rows[j].Sent
		}
		return rows[i].ID < rows[j].ID
	})
	total := int64(len(rows))
	start := (page - 1) * perPage
	if start >= len(rows) {
		return []OperatorMailOrganization{}, total, nil
	}
	return rows[start:min(start+perPage, len(rows))], total, nil
}

func (h *OperatorMetricsHandler) outbound(
	ctx context.Context,
	filter outboundFilter,
	page, perPage int,
) ([]OperatorOutboundMessage, int64, error) {
	rows, total, err := h.store.platformOutbound(ctx, filter, page, perPage)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]uint, 0, len(rows))
	seen := make(map[uint]bool, len(rows))
	for _, row := range rows {
		if !seen[row.OrganizationID] {
			seen[row.OrganizationID] = true
			ids = append(ids, row.OrganizationID)
		}
	}
	names, err := h.organizationsDirectory.PlatformOrganizationNames(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	messages := make([]OperatorOutboundMessage, len(rows))
	for index, row := range rows {
		messages[index] = OperatorOutboundMessage{
			ID: row.ID, OrganizationID: row.OrganizationID, OrganizationName: names[row.OrganizationID],
			MailboxID: row.MailboxID, MailboxAddress: row.MailboxAddress, FromAddress: row.FromAddress,
			Subject: row.Subject, Status: row.Status, FailureCode: row.FailureCode, LastEvent: row.LastEvent,
			CreatedAt: row.CreatedAt.UTC(), SendAttemptedAt: utcPointer(row.SendAttemptedAt),
			BroadcastID: row.BroadcastID,
		}
	}
	return messages, total, nil
}

// bindDay reads the UTC day from `date`, defaulting to today. A future day is invalid.
func (h *OperatorMetricsHandler) bindDay(c *gin.Context) (time.Time, bool) {
	now := h.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	raw := c.Query("date")
	if raw == "" {
		return today, true
	}
	day, err := time.ParseInLocation(operatorDateLayout, raw, time.UTC)
	if err != nil || day.After(today) {
		invalidMetricsInput(c)
		return time.Time{}, false
	}
	return day, true
}

func bindMetricsPage(c *gin.Context) (page, perPage int, ok bool) {
	page, perPage = 1, operatorMetricsPerPage
	if raw := c.Query("page"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, 0, false
		}
		page = parsed
	}
	if raw := c.Query("per_page"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > pagination.MaxPerPage {
			return 0, 0, false
		}
		perPage = parsed
	}
	return page, perPage, true
}

func optionalPositiveID(raw string) (uint, bool) {
	if raw == "" {
		return 0, true
	}
	parsed, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || parsed == 0 {
		return 0, false
	}
	return uint(parsed), true
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func invalidMetricsInput(c *gin.Context) {
	response.AbortWithCode(c, http.StatusBadRequest, response.ErrorCodeInvalidInput, "Invalid input")
}
