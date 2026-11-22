package mailbox

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

const (
	// maxInboxCursorLength bounds a cursor token; an encoded cursor is at most about 56 characters.
	maxInboxCursorLength = 128
	// maxInboxTimeZoneLength bounds the tz parameter.
	maxInboxTimeZoneLength = 64
)

// inboxParameters are the parameters of the unified inbox; each may appear at most once.
var inboxParameters = []string{"view", "role_id", "mailbox_id", "unread", "has_attachments", "q", "group", "tz", "cursor", "per_page", "triage", "assignment", "since", "until"}

// ListInboxMessages returns one cursor page of the caller's mail across every mailbox they read.
func (h *Handler) ListInboxMessages(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	// URL.Query drops a pair it cannot decode, which would turn a malformed cursor into none.
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		response.HandleError(c, "Invalid inbox query", fmt.Errorf("%w: malformed query string", domain.ErrInvalidInput))
		return
	}
	request, err := parseInboxRequest(query)
	if err != nil {
		response.HandleError(c, "Invalid inbox query", err)
		return
	}
	started := time.Now()
	page, err := h.service.ListInbox(c.Request.Context(), viewer, request)
	if err != nil {
		if errors.Is(err, ErrMailSearchTooBroad) {
			// Only the query's length reaches the log, never the query.
			slog.InfoContext(c.Request.Context(), "mail.inbox.search_too_broad",
				slog.String("request_id", c.GetString("request_id")),
				slog.Uint64("organization_id", uint64(viewer.OrganizationID)),
				slog.Int("query_length", utf8.RuneCountInString(request.Query)),
				slog.Int("readable_mailboxes", page.ReadableMailboxes),
			)
		}
		response.HandleError(c, "Failed to list the inbox", err)
		return
	}
	// The query, subjects, addresses, and snippets never reach the log.
	slog.InfoContext(c.Request.Context(), "mail.inbox.list",
		slog.String("request_id", c.GetString("request_id")),
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)),
		slog.Uint64("membership_id", uint64(viewer.MembershipID)),
		slog.String("view", string(request.View)),
		slog.Bool("grouped", request.GroupThreads),
		slog.Bool("has_query", request.Query != ""),
		slog.Int("readable_mailboxes", page.ReadableMailboxes),
		slog.Int("result_count", len(page.Items)),
		slog.Int64("duration_ms", time.Since(started).Milliseconds()),
	)
	next := ""
	if page.Next != nil {
		next = encodeInboxCursor(*page.Next)
	}
	privateResponse(c)
	response.SuccessCursorPage(c, toInboxItemResponses(page.Items), request.PerPage, next)
}

// parseInboxRequest applies the contract's parameter rules. Every refusal wraps
// domain.ErrInvalidInput, so a malformed request is 400 and never reaches the service.
func parseInboxRequest(query url.Values) (InboxRequest, error) {
	invalid := func(reason string) (InboxRequest, error) {
		return InboxRequest{}, fmt.Errorf("%w: %s", domain.ErrInvalidInput, reason)
	}
	if _, present := query["page"]; present {
		return invalid("page is not supported; use cursor")
	}
	for _, name := range inboxParameters {
		if len(query[name]) > 1 {
			return invalid(name + " is repeated")
		}
	}
	value := func(name string) (string, bool) {
		values, present := query[name]
		if !present || len(values) == 0 {
			return "", false
		}
		return values[0], true
	}
	positiveID := func(raw string) (uint, bool) {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 || uint64(uint(id)) != id {
			return 0, false
		}
		return uint(id), true
	}

	request := InboxRequest{View: domain.InboxViewAll, Location: time.UTC, PerPage: pagination.DefaultPerPage}
	if raw, present := value("view"); present {
		request.View = domain.InboxView(raw)
		if !request.View.Valid() {
			return invalid("view")
		}
	}
	if raw, present := value("role_id"); present {
		if raw == "none" {
			request.Ungrouped = true
		} else {
			var ok bool
			if request.RoleID, ok = positiveID(raw); !ok {
				return invalid("role_id")
			}
		}
	}
	if raw, present := value("mailbox_id"); present {
		var ok bool
		if request.MailboxID, ok = positiveID(raw); !ok {
			return invalid("mailbox_id")
		}
	}
	if raw, present := value("unread"); present {
		switch raw {
		case "true":
			request.Unread = new(true)
		case "false":
			request.Unread = new(false)
		default:
			return invalid("unread")
		}
	}
	if raw, present := value("has_attachments"); present {
		switch raw {
		case "true":
			request.HasAttachments = new(true)
		case "false":
			request.HasAttachments = new(false)
		default:
			return invalid("has_attachments")
		}
	}
	if raw, present := value("q"); present {
		// Like the single-mailbox search: the length counts as sent, the rest after trimming.
		request.Query = strings.TrimSpace(raw)
		if request.Query == "" || !utf8.ValidString(raw) || utf8.RuneCountInString(raw) > domain.MaxMailSearchLength ||
			strings.IndexFunc(request.Query, unicode.IsControl) >= 0 {
			return invalid("q")
		}
	}
	if raw, present := value("group"); present {
		if raw != "thread" {
			return invalid("group")
		}
		request.GroupThreads = true
	}
	if raw, present := value("tz"); present {
		location, err := parseInboxTimeZone(raw)
		if err != nil {
			return InboxRequest{}, err
		}
		request.Location = location
	}
	if raw, present := value("cursor"); present {
		cursor, err := decodeInboxCursor(raw)
		if err != nil {
			return InboxRequest{}, err
		}
		request.After = cursor
	}
	if raw, present := value("triage"); present {
		categories, ok := parseTriageCategories(raw)
		if !ok {
			return invalid("triage")
		}
		request.Triage = categories
	}
	if raw, present := value("assignment"); present {
		request.Assignment = domain.InboxAssignment(raw)
		if !request.Assignment.Valid() {
			return invalid("assignment")
		}
	}
	for _, bound := range []struct {
		name   string
		target **time.Time
	}{{"since", &request.SinceDate}, {"until", &request.UntilDate}} {
		if raw, present := value(bound.name); present {
			date, err := time.Parse(time.DateOnly, raw)
			if err != nil || date.Year() < 1970 || date.Year() > 9999 {
				return invalid(bound.name)
			}
			*bound.target = &date
		}
	}
	if raw, present := value("per_page"); present {
		perPage, err := strconv.Atoi(raw)
		if errors.Is(err, strconv.ErrRange) {
			// Still an integer, only too large to hold: clamp it like any other.
			perPage, err = pagination.MaxPerPage, nil
			if strings.HasPrefix(raw, "-") {
				perPage = 1
			}
		}
		if err != nil {
			return invalid("per_page")
		}
		// An integer out of range is clamped, like every other per_page.
		request.PerPage = min(max(perPage, 1), pagination.MaxPerPage)
	}

	switch {
	case request.Query != "" && request.View != domain.InboxViewAll:
		return invalid("q is only allowed with view all")
	case request.GroupThreads && request.View != domain.InboxViewAll && request.View != domain.InboxViewSent:
		return invalid("group needs view all or sent")
	case request.GroupThreads && (request.Unread != nil || request.HasAttachments != nil || request.Query != "" ||
		request.SinceDate != nil || request.UntilDate != nil):
		return invalid("group cannot be combined with unread, has_attachments, q, since, or until")
	case request.SinceDate != nil && request.UntilDate != nil && request.UntilDate.Before(*request.SinceDate):
		return invalid("until is before since")
	case len(request.Triage) > 0 && request.View != domain.InboxViewAll && request.View != domain.InboxViewToday:
		return invalid("triage needs view all or today")
	case len(request.Triage) > 0 && (request.GroupThreads || request.Query != ""):
		return invalid("triage cannot be combined with group or q")
	}
	return request, nil
}

// InboxMessageCounts returns the caller's counts of the unified inbox views, mail roles, and
// readable mailboxes. Only tz is accepted, at most once.
func (h *Handler) InboxMessageCounts(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		response.HandleError(c, "Invalid inbox counts query", fmt.Errorf("%w: malformed query string", domain.ErrInvalidInput))
		return
	}
	location := time.UTC
	for name, values := range query {
		if name != "tz" || len(values) != 1 {
			response.HandleError(c, "Invalid inbox counts query", fmt.Errorf("%w: only one tz is accepted", domain.ErrInvalidInput))
			return
		}
		if location, err = parseInboxTimeZone(values[0]); err != nil {
			response.HandleError(c, "Invalid inbox counts query", err)
			return
		}
	}
	counts, err := h.service.InboxCounts(c.Request.Context(), viewer, location)
	if err != nil {
		response.HandleError(c, "Failed to count the inbox", err)
		return
	}
	privateResponse(c)
	response.Success(c, toInboxCountsResponse(counts))
}

// parseInboxTimeZone reads the tz parameter as an IANA time zone. time.LoadLocation reads ""
// as UTC and "Local" as the server's zone; neither is a viewer's zone, so both are refused.
func parseInboxTimeZone(raw string) (*time.Location, error) {
	invalid := fmt.Errorf("%w: tz", domain.ErrInvalidInput)
	if raw == "" || raw == "Local" || len(raw) > maxInboxTimeZoneLength {
		return nil, invalid
	}
	location, err := time.LoadLocation(raw)
	if err != nil {
		return nil, invalid
	}
	return location, nil
}

// encodeInboxCursor writes a cursor as base64url("<unix nanos>.<id>") without padding.
func encodeInboxCursor(cursor domain.InboxCursor) string {
	raw := strconv.FormatInt(cursor.CreatedAt.UnixNano(), 10) + "." + strconv.FormatUint(uint64(cursor.ID), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeInboxCursor reads a token from encodeInboxCursor. Anything else, including a token that
// decodes but is not exactly what encodeInboxCursor would write, is domain.ErrInvalidInput.
func decodeInboxCursor(token string) (*domain.InboxCursor, error) {
	malformed := fmt.Errorf("%w: cursor is malformed", domain.ErrInvalidInput)
	if token == "" || len(token) > maxInboxCursorLength {
		return nil, malformed
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		return nil, malformed
	}
	nanosText, idText, ok := strings.Cut(string(raw), ".")
	if !ok {
		return nil, malformed
	}
	nanos, nanosErr := strconv.ParseInt(nanosText, 10, 64)
	id, idErr := strconv.ParseUint(idText, 10, 64)
	if nanosErr != nil || idErr != nil || id == 0 || uint64(uint(id)) != id {
		return nil, malformed
	}
	cursor := &domain.InboxCursor{CreatedAt: time.Unix(0, nanos).UTC(), ID: uint(id)}
	// Only the canonical spelling is accepted: no sign, leading zeros, or padding variants.
	if encodeInboxCursor(*cursor) != token {
		return nil, malformed
	}
	return cursor, nil
}
