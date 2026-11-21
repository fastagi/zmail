package mailbox

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// countedOutbound selects the messages platform mail metrics count: outbound mail sent through
// Zmail (not synced Gmail or Outlook copies) in a mailbox that still exists and belongs to an
// organization. Each row is one message, so counting rows never fans out. The direction and
// source are SQL literals, not bound values, so every plan, generic ones included, can read the
// partial index idx_mail_messages_outbound_time.
func (r *repository) countedOutbound(ctx context.Context) (*gorm.DB, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Table("mail_messages").
		Joins(`JOIN mailboxes ON mailboxes.id = mail_messages.mailbox_id
			AND mailboxes.deleted_at IS NULL AND mailboxes.organization_id IS NOT NULL`).
		Where(countedOutboundCondition), nil
}

// countedOutboundCondition is the counted direction and source as literals.
const countedOutboundCondition = "mail_messages.direction = '" + string(domain.MailDirectionOutbound) +
	"' AND mail_messages.source = '" + string(domain.MailSourceZmail) + "'"

// statusCountColumns counts one row per message under its current status.
const statusCountColumns = `COUNT(*) FILTER (WHERE mail_messages.status = 'sent') AS sent,
	COUNT(*) FILTER (WHERE mail_messages.status = 'failed') AS failed,
	COUNT(*) FILTER (WHERE mail_messages.status = 'queued') AS queued`

type platformStatusCounts struct {
	Sent   int
	Failed int
	Queued int
}

type platformDayCounts struct {
	Day    time.Time
	Counts platformStatusCounts
}

// platformDailyCounts counts messages per UTC creation day in [from, to).
func (r *repository) platformDailyCounts(ctx context.Context, from, to time.Time) ([]platformDayCounts, error) {
	scope, err := r.countedOutbound(ctx)
	if err != nil {
		return nil, err
	}
	var scanned []struct {
		Day    time.Time
		Sent   int
		Failed int
		Queued int
	}
	if err := scope.
		Select(`date_trunc('day', mail_messages.created_at AT TIME ZONE 'UTC') AS day, `+statusCountColumns).
		Where("mail_messages.created_at >= ? AND mail_messages.created_at < ?", from, to).
		Group("day").
		Order("day ASC").
		Scan(&scanned).Error; err != nil {
		return nil, fmt.Errorf("count platform outbound mail per day: %w", err)
	}
	rows := make([]platformDayCounts, len(scanned))
	for index, row := range scanned {
		rows[index] = platformDayCounts{
			Day:    time.Date(row.Day.Year(), row.Day.Month(), row.Day.Day(), 0, 0, 0, 0, time.UTC),
			Counts: platformStatusCounts{Sent: row.Sent, Failed: row.Failed, Queued: row.Queued},
		}
	}
	return rows, nil
}

type platformReceiptCounts struct {
	Delivered  int
	Bounced    int
	Complained int
}

// platformReceiptCounts counts the messages created in [from, to) that have at least one receipt
// of each kind. EXISTS keeps a duplicate receipt from counting a message twice.
func (r *repository) platformReceiptCounts(ctx context.Context, from, to time.Time) (platformReceiptCounts, error) {
	scope, err := r.countedOutbound(ctx)
	if err != nil {
		return platformReceiptCounts{}, err
	}
	receipt := func(eventType domain.MailEventType) string {
		return fmt.Sprintf(`COUNT(*) FILTER (WHERE EXISTS (SELECT 1 FROM mail_message_events AS receipt
			WHERE receipt.message_id = mail_messages.id AND receipt.type = '%s'))`, eventType)
	}
	var counts platformReceiptCounts
	if err := scope.
		Select(receipt(domain.MailEventDelivered)+` AS delivered, `+
			receipt(domain.MailEventBounced)+` AS bounced, `+
			receipt(domain.MailEventComplained)+` AS complained`).
		Where("mail_messages.created_at >= ? AND mail_messages.created_at < ?", from, to).
		Scan(&counts).Error; err != nil {
		return platformReceiptCounts{}, fmt.Errorf("count platform delivery receipts: %w", err)
	}
	return counts, nil
}

// platformOrganizationCounts counts messages created in [from, to) per organization. Organizations
// without mail are absent.
func (r *repository) platformOrganizationCounts(ctx context.Context, from, to time.Time) (map[uint]platformStatusCounts, error) {
	scope, err := r.countedOutbound(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		OrganizationID uint
		Sent           int
		Failed         int
		Queued         int
	}
	if err := scope.
		Select(`mailboxes.organization_id AS organization_id, `+statusCountColumns).
		Where("mail_messages.created_at >= ? AND mail_messages.created_at < ?", from, to).
		Group("mailboxes.organization_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count platform outbound mail per organization: %w", err)
	}
	counts := make(map[uint]platformStatusCounts, len(rows))
	for _, row := range rows {
		counts[row.OrganizationID] = platformStatusCounts{Sent: row.Sent, Failed: row.Failed, Queued: row.Queued}
	}
	return counts, nil
}

// platformMailboxCounts counts each organization's live Zmail mailboxes, the figure its mailbox
// limit bounds; members' connected accounts are neither counted nor revealed.
func (r *repository) platformMailboxCounts(ctx context.Context) (map[uint]int64, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []struct {
		OrganizationID uint
		Mailboxes      int64
	}
	if err := r.conn(ctx).Model(&MailboxPO{}).
		Select("organization_id, COUNT(*) AS mailboxes").
		Where("organization_id IS NOT NULL AND origin = ?", string(domain.MailboxOriginZmail)).
		Group("organization_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count platform mailboxes: %w", err)
	}
	counts := make(map[uint]int64, len(rows))
	for _, row := range rows {
		counts[row.OrganizationID] = row.Mailboxes
	}
	return counts, nil
}

type outboundFilter struct {
	Day            time.Time
	OrganizationID uint
	MailboxID      uint
	Status         string
}

type platformOutboundRow struct {
	ID              uint
	OrganizationID  uint
	MailboxID       uint
	MailboxAddress  string
	FromAddress     string
	Subject         string
	Status          string
	FailureCode     string
	LastEvent       string
	CreatedAt       time.Time
	SendAttemptedAt *time.Time
	BroadcastID     *uint
}

// platformOutbound lists one UTC day of counted messages, newest first. It selects metadata only:
// never a body, recipients, or attachments. broadcast_id marks the messages whose body an operator
// with mail_content:read may read.
func (r *repository) platformOutbound(
	ctx context.Context,
	filter outboundFilter,
	page, perPage int,
) ([]platformOutboundRow, int64, error) {
	scope, err := r.countedOutbound(ctx)
	if err != nil {
		return nil, 0, err
	}
	scope = scope.Where("mail_messages.created_at >= ? AND mail_messages.created_at < ?",
		filter.Day, filter.Day.AddDate(0, 0, 1))
	if filter.OrganizationID != 0 {
		scope = scope.Where("mailboxes.organization_id = ?", filter.OrganizationID)
	}
	if filter.MailboxID != 0 {
		scope = scope.Where("mail_messages.mailbox_id = ?", filter.MailboxID)
	}
	if filter.Status != "" {
		scope = scope.Where("mail_messages.status = ?", filter.Status)
	}
	var total int64
	if err := scope.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count platform outbound log: %w", err)
	}
	var rows []platformOutboundRow
	if err := scope.
		Select(`mail_messages.id, mailboxes.organization_id, mail_messages.mailbox_id,
			mailboxes.address AS mailbox_address, mail_messages.from_address, mail_messages.subject,
			mail_messages.status, mail_messages.failure_code, mail_messages.last_event,
			mail_messages.created_at, mail_messages.send_attempted_at, ` + broadcastOfMessage + ` AS broadcast_id`).
		Order("mail_messages.created_at DESC, mail_messages.id DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list platform outbound log: %w", err)
	}
	return rows, total, nil
}
