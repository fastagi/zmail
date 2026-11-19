package mailbox

import (
	"context"
	"fmt"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// transactionalLogDefaultRange is the created_at window the log shows without since and until.
	transactionalLogDefaultRange = 7 * 24 * time.Hour
	// transactionalLogMaxRange is the longest created_at window one log request may ask for.
	transactionalLogMaxRange = 30 * 24 * time.Hour
	// transactionalLogMaxRecipient bounds the exact-recipient filter.
	transactionalLogMaxRecipient = 320
	// transactionalLogCountCap is where the 24-hour count stops.
	transactionalLogCountCap = 10000
	// transactionalLogCountWindow is the window of the log's total_24h.
	transactionalLogCountWindow = 24 * time.Hour
)

// transactionalLogEvents are the last_event values the log filters by; accepted is not one.
var transactionalLogEvents = map[domain.MailEventType]bool{
	domain.MailEventDelivered:  true,
	domain.MailEventDeferred:   true,
	domain.MailEventBounced:    true,
	domain.MailEventFailed:     true,
	domain.MailEventRejected:   true,
	domain.MailEventComplained: true,
}

// TransactionalLogPage is one keyset page of the console's transactional log.
type TransactionalLogPage struct {
	Items []*domain.TransactionalMessage
	// NextCursor is the id to pass as the cursor of the next, older page; nil on the last page.
	NextCursor *uint
	// Total24h counts the mode's messages created in the last 24 hours, up to 10000, of the
	// requested template when one is given, whatever the other filters.
	Total24h int64
}

// ListTransactionalLog lists an organization's transactional messages for the console, newest
// first. The filter is validated and defaulted here: live mode, the last 7 days, 50 per page.
func (s *service) ListTransactionalLog(ctx context.Context, organizationID uint, filter domain.TransactionalLogFilter) (TransactionalLogPage, error) {
	if s.transactional == nil {
		return TransactionalLogPage{}, domain.ErrServiceUnavailable
	}
	now := s.now().UTC()
	filter, err := normalizeTransactionalLogFilter(filter, now)
	if err != nil {
		return TransactionalLogPage{}, err
	}
	rows, err := s.transactional.ListTransactionalLog(ctx, organizationID, filter)
	if err != nil {
		return TransactionalLogPage{}, err
	}
	page := TransactionalLogPage{Items: rows}
	if len(rows) > filter.PerPage {
		page.Items = rows[:filter.PerPage]
		next := page.Items[len(page.Items)-1].ID
		page.NextCursor = &next
	}
	page.Total24h, err = s.transactional.CountTransactionalSince(ctx, organizationID, filter.Mode, filter.TemplateKey,
		now.Add(-transactionalLogCountWindow), transactionalLogCountCap)
	if err != nil {
		return TransactionalLogPage{}, err
	}
	return page, nil
}

// GetTransactionalLogEntry reads one message of the organization, of either mode, with its
// first events.
func (s *service) GetTransactionalLogEntry(ctx context.Context, organizationID, id uint) (*domain.TransactionalMessage, error) {
	if s.transactional == nil {
		return nil, ErrTransactionalMessageNotFound
	}
	message, err := s.transactional.FindTransactional(ctx, organizationID, id)
	if err != nil {
		return nil, err
	}
	return s.withTransactionalEvents(ctx, message)
}

// normalizeTransactionalLogFilter checks every filter and fills the defaults. A missing until
// leaves the window open at the top, so a message stored this instant is listed.
func normalizeTransactionalLogFilter(filter domain.TransactionalLogFilter, now time.Time) (domain.TransactionalLogFilter, error) {
	invalid := func(reason string) (domain.TransactionalLogFilter, error) {
		return domain.TransactionalLogFilter{}, fmt.Errorf("%w: %s", domain.ErrInvalidInput, reason)
	}
	switch filter.Mode {
	case "":
		filter.Mode = domain.APIKeyModeLive
	case domain.APIKeyModeLive, domain.APIKeyModeTest:
	default:
		return invalid("unknown mode")
	}
	if filter.Status != "" && !domain.TransactionalStatus(filter.Status).Valid() {
		return invalid("unknown status")
	}
	if filter.LastEvent != "" && !transactionalLogEvents[domain.MailEventType(filter.LastEvent)] {
		return invalid("unknown last event")
	}
	switch filter.Outcome {
	case "", domain.TransactionalOutcomeDelivered, domain.TransactionalOutcomeProblem:
	default:
		return invalid("unknown outcome")
	}
	if filter.TemplateKey != "" && !templateKeyPattern.MatchString(filter.TemplateKey) {
		return invalid("malformed template key")
	}
	filter.To = normalizeEmail(filter.To)
	if len(filter.To) > transactionalLogMaxRecipient {
		return invalid("recipient too long")
	}
	switch {
	case filter.PerPage == 0:
		filter.PerPage = transactionalLogDefaultPerPage
	case filter.PerPage < 1 || filter.PerPage > transactionalLogMaxPerPage:
		return invalid("per_page out of range")
	}
	end := now
	if !filter.Until.IsZero() {
		end = filter.Until
	}
	if filter.Since.IsZero() {
		filter.Since = end.Add(-transactionalLogDefaultRange)
	}
	if !filter.Until.IsZero() && !filter.Since.Before(filter.Until) {
		return invalid("since must be before until")
	}
	if end.Sub(filter.Since) > transactionalLogMaxRange {
		return invalid("range longer than 30 days")
	}
	return filter, nil
}
