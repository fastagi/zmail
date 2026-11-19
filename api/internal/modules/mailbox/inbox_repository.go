package mailbox

import (
	"context"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// inboxSnippetSource is how many characters of the text body a snippet is cut from. substr on a
// TOASTed value decompresses only the slice it returns, so the full body is never read.
const inboxSnippetSource = 400

// ListMailboxAccessRows is bounded by the organization's mailbox limit.
func (r *repository) ListMailboxAccessRows(ctx context.Context, organizationID, membershipID uint) ([]domain.MailboxAccessRow, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var boxes []MailboxPO
	err := r.conn(ctx).
		Where("organization_id = ?", organizationID).
		Order("id ASC").
		Limit(domain.MaxMailboxesPerOrganization).
		Find(&boxes).Error
	if err != nil {
		return nil, err
	}
	members, err := r.ListMembershipMailboxMembers(ctx, organizationID, membershipID)
	if err != nil {
		return nil, err
	}
	memberOf := make(map[uint]*domain.MailboxMember, len(members))
	for _, member := range members {
		memberOf[member.MailboxID] = member
	}
	rows := make([]domain.MailboxAccessRow, 0, len(boxes))
	for i := range boxes {
		rows = append(rows, domain.MailboxAccessRow{Mailbox: boxes[i].toDomain(), Member: memberOf[boxes[i].ID]})
	}
	return rows, nil
}

// inboxHead is one row of an inbox page before its message is loaded.
type inboxHead struct {
	ID        uint
	MailboxID uint
	ThreadID  *uint
	CreatedAt time.Time
}

// ListInbox serves the all and sent views. Each mailbox contributes at most query.Limit rows, read
// newest first from idx_mail_messages_inbox_time, and the merge keeps the newest query.Limit of
// those, so the cost follows the page size and the number of mailboxes, not the stored mail.
func (r *repository) ListInbox(ctx context.Context, query domain.InboxQuery) ([]*domain.MailMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var direction domain.MailDirection
	switch query.View {
	case domain.InboxViewAll:
		direction = domain.MailDirectionInbound
	case domain.InboxViewSent:
		direction = domain.MailDirectionOutbound
	default:
		return nil, fmt.Errorf("%w: inbox view %q", domain.ErrInvalidInput, query.View)
	}
	if query.Unread != nil || query.Query != "" {
		return nil, fmt.Errorf("%w: inbox unread and search filters", domain.ErrInvalidInput)
	}
	if len(query.MailboxIDs) == 0 || query.Limit < 1 {
		return []*domain.MailMessage{}, nil
	}
	db := r.conn(ctx)

	args := map[string]any{
		"mailbox_ids": query.MailboxIDs,
		"direction":   string(direction),
		"placement":   string(domain.MailPlacementDefault),
		"limit":       query.Limit,
	}
	cursor := ""
	if query.After != nil {
		cursor = "AND (m.created_at, m.id) < (@cursor_at, @cursor_id)"
		args["cursor_at"], args["cursor_id"] = query.After.CreatedAt, query.After.ID
	}
	// The newest message of its thread in the view, within its mailbox. Newer messages need no
	// seal check: they are newer than an unsealed message, so they are unsealed too. The seal
	// condition itself reads the mailbox row the lateral join already holds (unsealedMessage).
	threadHead := ""
	if query.GroupThreads {
		threadHead = `AND NOT EXISTS (SELECT 1 FROM mail_messages newer
			WHERE newer.mailbox_id = m.mailbox_id AND newer.placement = m.placement
			  AND newer.direction = m.direction AND newer.thread_id = m.thread_id
			  AND (newer.created_at, newer.id) > (m.created_at, m.id))`
	}
	var heads []inboxHead
	err := db.Raw(`SELECT page.id, page.mailbox_id, page.thread_id, page.created_at
		FROM mailboxes AS box
		CROSS JOIN LATERAL (
		  SELECT m.id, m.mailbox_id, m.thread_id, m.created_at
		  FROM mail_messages m
		  WHERE m.mailbox_id = box.id AND m.direction = @direction AND m.placement = @placement
		    AND (box.sealed_before IS NULL OR m.created_at >= box.sealed_before)
		    `+cursor+`
		    `+threadHead+`
		  ORDER BY m.created_at DESC, m.id DESC
		  LIMIT @limit) AS page
		WHERE box.id IN @mailbox_ids
		ORDER BY page.created_at DESC, page.id DESC
		LIMIT @limit`, args).Scan(&heads).Error
	if err != nil {
		return nil, err
	}
	if len(heads) == 0 {
		return []*domain.MailMessage{}, nil
	}

	ids := make([]uint, 0, len(heads))
	for _, head := range heads {
		ids = append(ids, head.ID)
	}
	var rows []MailMessagePO
	// A listing never needs the bodies; the detail endpoint returns them.
	if err = db.Model(&MailMessagePO{}).Omit("text_body", "html_body").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]*domain.MailMessage, len(rows))
	for i := range rows {
		byID[rows[i].ID] = rows[i].toDomain()
	}
	addresses, err := mailboxAddresses(db, query.MailboxIDs)
	if err != nil {
		return nil, err
	}

	var threads map[uint]map[uint]domain.MailThreadInfo
	if query.GroupThreads {
		threads, err = inboxThreadInfos(db, query.MembershipID, direction, heads)
		if err != nil {
			return nil, err
		}
	}

	messages := make([]*domain.MailMessage, 0, len(heads))
	for _, head := range heads {
		message, ok := byID[head.ID]
		if !ok {
			// Deleted between the two reads; the next page load settles it.
			continue
		}
		message.MailboxAddress = addresses[message.MailboxID]
		if query.GroupThreads {
			info, ok := threads[message.MailboxID][message.ThreadID]
			if !ok {
				info = domain.MailThreadInfo{MessageIDs: []uint{}, LabelIDs: []uint{}}
			}
			message.Thread = &info
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// inboxThreadInfos summarizes the threads of the page heads in their mailbox's view, one mailbox
// at a time: threads never cross mailboxes.
func inboxThreadInfos(
	db *gorm.DB,
	membershipID uint,
	direction domain.MailDirection,
	heads []inboxHead,
) (map[uint]map[uint]domain.MailThreadInfo, error) {
	threadsOf := map[uint][]uint{}
	mailboxIDs := []uint{}
	for _, head := range heads {
		if head.ThreadID == nil {
			continue
		}
		if _, seen := threadsOf[head.MailboxID]; !seen {
			mailboxIDs = append(mailboxIDs, head.MailboxID)
		}
		if !slices.Contains(threadsOf[head.MailboxID], *head.ThreadID) {
			threadsOf[head.MailboxID] = append(threadsOf[head.MailboxID], *head.ThreadID)
		}
	}
	view := domain.MailMessageFilter{Placement: domain.MailPlacementDefault, Direction: direction}
	infos := make(map[uint]map[uint]domain.MailThreadInfo, len(mailboxIDs))
	for _, mailboxID := range mailboxIDs {
		info, err := threadInfos(db, mailboxID, membershipID, view, threadsOf[mailboxID])
		if err != nil {
			return nil, err
		}
		infos[mailboxID] = info
	}
	return infos, nil
}

// mailboxAddresses returns the address of each given mailbox.
func mailboxAddresses(db *gorm.DB, ids []uint) (map[uint]string, error) {
	var rows []struct {
		ID      uint
		Address string
	}
	if err := db.Model(&MailboxPO{}).Unscoped().Select("id, address").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	addresses := make(map[uint]string, len(rows))
	for _, row := range rows {
		addresses[row.ID] = row.Address
	}
	return addresses, nil
}

// MessageSnippets returns the first inboxSnippetSource characters of each message's text body; a
// message without one, such as HTML-only mail, has "". Unknown ids are absent.
func (r *repository) MessageSnippets(ctx context.Context, ids []uint) (map[uint]string, error) {
	snippets := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return snippets, nil
	}
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []struct {
		ID      uint
		Snippet string
	}
	err := r.conn(ctx).Model(&MailMessagePO{}).
		Select("id, substr(text_body, 1, ?) AS snippet", inboxSnippetSource).
		Where("id IN ?", ids).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		snippets[row.ID] = row.Snippet
	}
	return snippets, nil
}
