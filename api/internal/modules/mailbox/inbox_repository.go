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

// The service takes the inbox seam from its repository by type assertion; this keeps the real
// repository from silently losing it.
var _ domain.MailInboxRepository = (*repository)(nil)

// CountUnreadByMailboxIDs counts the reader's unread Inbox mail of only the given mailboxes, with
// the predicate ListMailboxes uses; ids outside the organization count nothing. A mailbox without
// unread mail is absent.
func (r *repository) CountUnreadByMailboxIDs(
	ctx context.Context,
	organizationID, readerMembershipID uint,
	mailboxIDs []uint,
) (map[uint]int, error) {
	if len(mailboxIDs) == 0 {
		return map[uint]int{}, nil
	}
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return scanUnreadCounts(inboxUnreadCounts(r.conn(ctx), mailboxIDs, readerMembershipID).
		Where("mailboxes.organization_id = ? AND mailboxes.deleted_at IS NULL", organizationID))
}

// CountInboxViews counts the today, pending, and waiting mail of the given mailboxes for the
// membership in one statement. Today reads idx_mail_messages_inbox_time as a created_at range per
// mailbox, pending starts from the membership's stars, and waiting reads the partial
// idx_mail_messages_waiting. Sealed mail and the trash never count.
func (r *repository) CountInboxViews(
	ctx context.Context,
	membershipID uint,
	mailboxIDs []uint,
	dayStart, dayEnd time.Time,
) (domain.InboxViewCounts, error) {
	var counts domain.InboxViewCounts
	if len(mailboxIDs) == 0 {
		return counts, nil
	}
	if r.db == nil {
		return counts, domain.ErrServiceUnavailable
	}
	err := r.conn(ctx).Raw(`SELECT today.total AS today, today.unread AS today_unread,
		  (SELECT COUNT(*) FROM mail_message_stars AS star
		    JOIN mail_messages ON mail_messages.id = star.message_id
		    WHERE star.membership_id = @m AND mail_messages.mailbox_id IN @mailbox_ids
		      AND mail_messages.placement NOT IN @hidden AND `+unsealedMessage+`) AS pending,
		  (SELECT COUNT(*) FROM mail_messages
		    WHERE mail_messages.queued_reason = @waiting AND mail_messages.mailbox_id IN @mailbox_ids
		      AND `+unsealedMessage+`) AS sent_waiting
		FROM (SELECT COUNT(*) AS total, COUNT(*) FILTER (WHERE `+unreadByMember+`) AS unread
		  FROM mail_messages
		  WHERE mail_messages.mailbox_id IN @mailbox_ids AND mail_messages.direction = @inbound
		    AND mail_messages.placement <> @trash
		    AND mail_messages.created_at >= @day_start AND mail_messages.created_at < @day_end
		    AND `+unsealedMessage+`) AS today`, map[string]any{
		"m":           membershipID,
		"mailbox_ids": mailboxIDs,
		"hidden":      []string{string(domain.MailPlacementArchive), string(domain.MailPlacementTrash)},
		"waiting":     domain.MailQueuedForSendLimit,
		"inbound":     string(domain.MailDirectionInbound),
		"trash":       string(domain.MailPlacementTrash),
		"day_start":   dayStart,
		"day_end":     dayEnd,
	}).Scan(&counts).Error
	if err != nil {
		return domain.InboxViewCounts{}, err
	}
	return counts, nil
}

// ListMailboxAccessRows is bounded by the organization's mailbox limit.
func (r *repository) ListMailboxAccessRows(ctx context.Context, organizationID, membershipID uint) ([]domain.MailboxAccessRow, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var boxes []MailboxPO
	err := r.conn(ctx).
		Where("organization_id = ?", organizationID).
		Order("id ASC").
		Limit(domain.MailboxesPerOrganizationCeiling).
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

// ListInbox serves every view of the unified inbox, newest first by (created_at, id) after
// query.After. All, sent, and today read each mailbox's newest query.Limit rows from
// idx_mail_messages_inbox_time and merge them, so the cost follows the page size and the number of
// mailboxes, not the stored mail. Pending starts from the membership's stars; a search runs within
// the search time limit.
func (r *repository) ListInbox(ctx context.Context, query domain.InboxQuery) ([]*domain.MailMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if err := checkInboxQuery(query); err != nil {
		return nil, err
	}
	if len(query.MailboxIDs) == 0 || query.Limit < 1 {
		return []*domain.MailMessage{}, nil
	}
	db := r.conn(ctx)

	var (
		heads []inboxHead
		err   error
	)
	switch {
	case query.Query != "":
		err = r.search(ctx, func(tx *gorm.DB) error {
			heads, err = inboxSearchHeads(tx, query)
			return err
		})
	case query.View == domain.InboxViewPending:
		heads, err = inboxPendingHeads(db, query)
	default:
		heads, err = inboxMailboxHeads(db, query)
	}
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
		threads, err = inboxThreadInfos(db, query.MembershipID, inboxDirection(query.View), heads)
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

// checkInboxQuery refuses the queries no view serves: an unknown view, a today view without its
// day, a search outside all, and thread grouping outside the unfiltered all and sent views.
func checkInboxQuery(query domain.InboxQuery) error {
	if !query.View.Valid() {
		return fmt.Errorf("%w: inbox view %q", domain.ErrInvalidInput, query.View)
	}
	if query.View == domain.InboxViewToday && !query.DayEnd.After(query.DayStart) {
		return fmt.Errorf("%w: inbox today view without its day", domain.ErrInvalidInput)
	}
	if query.Query != "" && query.View != domain.InboxViewAll {
		return fmt.Errorf("%w: inbox search outside the all view", domain.ErrInvalidInput)
	}
	if len(query.Triage) > 0 && ((query.View != domain.InboxViewAll && query.View != domain.InboxViewToday) ||
		query.GroupThreads || query.Query != "") {
		return fmt.Errorf("%w: inbox triage filter", domain.ErrInvalidInput)
	}
	if query.GroupThreads && (inboxDirection(query.View) == "" || query.View == domain.InboxViewToday ||
		query.Unread != nil || query.Query != "") {
		return fmt.Errorf("%w: inbox thread grouping", domain.ErrInvalidInput)
	}
	return nil
}

// inboxDirection is the direction a view lists; pending lists both and has none.
func inboxDirection(view domain.InboxView) domain.MailDirection {
	switch view {
	case domain.InboxViewAll, domain.InboxViewToday:
		return domain.MailDirectionInbound
	case domain.InboxViewSent:
		return domain.MailDirectionOutbound
	case domain.InboxViewPending:
	}
	return ""
}

// inboxArgs are the named arguments every inbox head query binds.
func inboxArgs(query domain.InboxQuery) map[string]any {
	return map[string]any{
		"mailbox_ids": query.MailboxIDs,
		"m":           query.MembershipID,
		"limit":       query.Limit,
		"trash":       string(domain.MailPlacementTrash),
	}
}

// inboxFilters is the cursor and unread conditions on mail_messages, binding their arguments.
func inboxFilters(query domain.InboxQuery, args map[string]any) string {
	filters := ""
	if query.After != nil {
		filters += " AND (mail_messages.created_at, mail_messages.id) < (@cursor_at, @cursor_id)"
		args["cursor_at"], args["cursor_id"] = query.After.CreatedAt, query.After.ID
	}
	if query.Unread != nil {
		filters += " AND " + negatedIf(unreadByMember, !*query.Unread)
	}
	return filters
}

// inboxTriageFilter keeps the messages of box with a visible triage result of one of the query's
// categories; checkInboxQuery allows it only in views inboxMailboxHeads serves. It reads the
// mailbox's results from idx_mail_triage_results and joins them to the mailbox's messages, so a
// mailbox without matching results costs one index probe. A per-message EXISTS instead walks every
// Inbox message of a mailbox whose results are rare or absent: 42 ms against 9 ms for triage=reply
// across 200 mailboxes and 100,000 messages (docs/benchmarks/v2-hot-paths.md).
func inboxTriageFilter(query domain.InboxQuery, args map[string]any) string {
	if len(query.Triage) == 0 {
		return ""
	}
	categories := make([]string, 0, len(query.Triage))
	for _, category := range query.Triage {
		categories = append(categories, string(category))
	}
	args["triage_visible"], args["triage_categories"] = visibleTriageStatuses(), categories
	return ` AND mail_messages.id IN (SELECT mail_triage.message_id FROM mail_triage
		    WHERE mail_triage.mailbox_id = box.id
		      AND mail_triage.status IN @triage_visible AND mail_triage.category IN @triage_categories)`
}

// inboxOrder is the inbox order on mail_messages, newest first.
const inboxOrder = "ORDER BY mail_messages.created_at DESC, mail_messages.id DESC LIMIT @limit"

// inboxMailboxHeads serves all, sent, and today: each mailbox contributes at most query.Limit rows,
// read newest first from idx_mail_messages_inbox_time, and the merge keeps the newest query.Limit.
func inboxMailboxHeads(db *gorm.DB, query domain.InboxQuery) ([]inboxHead, error) {
	args := inboxArgs(query)
	args["direction"] = string(inboxDirection(query.View))
	view := "AND mail_messages.placement = @placement"
	args["placement"] = string(domain.MailPlacementDefault)
	if query.View == domain.InboxViewToday {
		view = `AND mail_messages.placement <> @trash
		    AND mail_messages.created_at >= @day_start AND mail_messages.created_at < @day_end`
		args["day_start"], args["day_end"] = query.DayStart, query.DayEnd
	}
	// The newest message of its thread in the view, within its mailbox. Newer messages need no
	// seal check: they are newer than an unsealed message, so they are unsealed too. The seal
	// condition itself reads the mailbox row the lateral join already holds, which is
	// unsealedMessage without a second mailboxes lookup.
	threadHead := ""
	if query.GroupThreads {
		threadHead = `AND NOT EXISTS (SELECT 1 FROM mail_messages newer
			WHERE newer.mailbox_id = mail_messages.mailbox_id AND newer.placement = mail_messages.placement
			  AND newer.direction = mail_messages.direction AND newer.thread_id = mail_messages.thread_id
			  AND (newer.created_at, newer.id) > (mail_messages.created_at, mail_messages.id))`
	}
	var heads []inboxHead
	err := db.Raw(`SELECT page.id, page.mailbox_id, page.thread_id, page.created_at
		FROM mailboxes AS box
		CROSS JOIN LATERAL (
		  SELECT mail_messages.id, mail_messages.mailbox_id, mail_messages.thread_id, mail_messages.created_at
		  FROM mail_messages
		  WHERE mail_messages.mailbox_id = box.id AND mail_messages.direction = @direction
		    `+view+`
		    AND (box.sealed_before IS NULL OR mail_messages.created_at >= box.sealed_before)
		    `+inboxFilters(query, args)+inboxTriageFilter(query, args)+`
		    `+threadHead+`
		  `+inboxOrder+`) AS page
		WHERE box.id IN @mailbox_ids
		ORDER BY page.created_at DESC, page.id DESC
		LIMIT @limit`, args).Scan(&heads).Error
	return heads, err
}

// inboxPendingHeads serves pending: the membership's stars, a small set, outside the archive and
// the trash, in either direction.
func inboxPendingHeads(db *gorm.DB, query domain.InboxQuery) ([]inboxHead, error) {
	args := inboxArgs(query)
	args["hidden"] = []string{string(domain.MailPlacementArchive), string(domain.MailPlacementTrash)}
	var heads []inboxHead
	err := db.Raw(`SELECT mail_messages.id, mail_messages.mailbox_id, mail_messages.thread_id, mail_messages.created_at
		FROM mail_message_stars AS star
		JOIN mail_messages ON mail_messages.id = star.message_id
		WHERE star.membership_id = @m AND mail_messages.mailbox_id IN @mailbox_ids
		  AND mail_messages.placement NOT IN @hidden AND `+unsealedMessage+`
		  `+inboxFilters(query, args)+`
		`+inboxOrder, args).Scan(&heads).Error
	return heads, err
}

// inboxSearchHeads serves a search: either direction, every placement but the trash, matched by
// searchCondition. The caller runs it within the search time limit.
func inboxSearchHeads(db *gorm.DB, query domain.InboxQuery) ([]inboxHead, error) {
	args := inboxArgs(query)
	args["p"] = searchPattern(query.Query)
	var heads []inboxHead
	err := db.Raw(`SELECT mail_messages.id, mail_messages.mailbox_id, mail_messages.thread_id, mail_messages.created_at
		FROM mail_messages
		WHERE mail_messages.mailbox_id IN @mailbox_ids AND mail_messages.placement <> @trash
		  AND `+unsealedMessage+` AND `+searchCondition+`
		  `+inboxFilters(query, args)+`
		`+inboxOrder, args).Scan(&heads).Error
	return heads, err
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
