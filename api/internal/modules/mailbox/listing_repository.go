package mailbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailSearchTimeout bounds one search statement. A search that needs longer, as a one- or
// two-character text can in a large mailbox, is refused as too broad instead of loading the
// database.
const MailSearchTimeout = 2 * time.Second

var searchEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// searchPattern turns search text into an ILIKE pattern that matches it literally anywhere: the
// escape character and both wildcards match themselves.
func searchPattern(query string) string {
	return "%" + searchEscaper.Replace(query) + "%"
}

// searchCondition matches the subject, the sender address and name, the to and cc addresses, and
// the text body; an empty cc list never matches. Each column has a trigram index, so the planner
// can combine them for a text of three characters or more.
const searchCondition = `(subject ILIKE @p ESCAPE '\' OR from_address ILIKE @p ESCAPE '\' OR from_name ILIKE @p ESCAPE '\'
	OR to_addresses ILIKE @p ESCAPE '\' OR (cc_addresses <> '[]' AND cc_addresses ILIKE @p ESCAPE '\') OR text_body ILIKE @p ESCAPE '\')`

const unreadByMember = `mail_messages.direction = 'inbound' AND NOT EXISTS (SELECT 1 FROM mail_message_reads
	WHERE mail_message_reads.message_id = mail_messages.id AND mail_message_reads.membership_id = @m)`

const starredByMember = `EXISTS (SELECT 1 FROM mail_message_stars
	WHERE mail_message_stars.message_id = mail_messages.id AND mail_message_stars.membership_id = @m)`

// messageView selects the unsealed messages of the mailbox that filter keeps. Without a
// placement every placement except trash is kept.
func messageView(db *gorm.DB, mailboxID, membershipID uint, filter domain.MailMessageFilter) *gorm.DB {
	query := db.Model(&MailMessagePO{}).Where("mail_messages.mailbox_id = ?", mailboxID).Where(unsealedMessage)
	if filter.Placement == "" {
		query = query.Where("mail_messages.placement <> ?", string(domain.MailPlacementTrash))
	} else {
		query = query.Where("mail_messages.placement = ?", string(filter.Placement))
	}
	if filter.FolderID != 0 {
		query = query.Where("mail_messages.folder_id = ?", filter.FolderID)
	}
	if filter.Direction != "" {
		query = query.Where("mail_messages.direction = ?", string(filter.Direction))
	}
	if filter.LabelID != 0 {
		query = query.Where(`EXISTS (SELECT 1 FROM mail_message_labels
			WHERE mail_message_labels.message_id = mail_messages.id AND mail_message_labels.label_id = ?)`, filter.LabelID)
	}
	member := map[string]any{"m": membershipID}
	if filter.Starred != nil {
		query = query.Where(negatedIf(starredByMember, !*filter.Starred), member)
	}
	if filter.Unread != nil {
		query = query.Where(negatedIf(unreadByMember, !*filter.Unread), member)
	}
	if filter.HasAttachments != nil {
		if *filter.HasAttachments {
			query = query.Where("mail_messages.attachment_count > 0")
		} else {
			query = query.Where("mail_messages.attachment_count = 0")
		}
	}
	if filter.Query != "" {
		query = query.Where(searchCondition, map[string]any{"p": searchPattern(filter.Query)})
	}
	return query
}

func negatedIf(condition string, negate bool) string {
	if negate {
		return "NOT (" + condition + ")"
	}
	return condition
}

func pageOf(filter domain.MailMessageFilter) (offset, limit int) {
	page, perPage := filter.Page, filter.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 15
	}
	return (page - 1) * perPage, perPage
}

func (r *repository) ListMessages(
	ctx context.Context,
	mailboxID, membershipID uint,
	filter domain.MailMessageFilter,
) ([]*domain.MailMessage, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	offset, limit := pageOf(filter)
	var (
		total int64
		rows  []MailMessagePO
	)
	list := func(db *gorm.DB) error {
		if err := messageView(db, mailboxID, membershipID, filter).Count(&total).Error; err != nil {
			return err
		}
		order := "mail_messages.id DESC"
		if filter.Placement == domain.MailPlacementTrash {
			order = "mail_messages.trashed_at DESC, mail_messages.id DESC"
		}
		// A listing never needs the bodies; the detail endpoint returns them.
		return messageView(db, mailboxID, membershipID, filter).
			Omit("text_body", "html_body").
			Order(order).
			Offset(offset).
			Limit(limit).
			Find(&rows).Error
	}
	var err error
	if filter.Query == "" {
		err = list(r.conn(ctx))
	} else {
		err = r.search(ctx, list)
	}
	if err != nil {
		return nil, 0, err
	}
	messages := make([]*domain.MailMessage, 0, len(rows))
	for i := range rows {
		messages = append(messages, rows[i].toDomain())
	}
	return messages, total, nil
}

// search runs fn in a transaction whose statements may take at most searchTimeout; a statement
// canceled by that limit is ErrMailSearchTooBroad.
func (r *repository) search(ctx context.Context, fn func(db *gorm.DB) error) error {
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		timeout := fmt.Sprintf("SET LOCAL statement_timeout = %d", r.searchTimeout.Milliseconds())
		if err := tx.Exec(timeout).Error; err != nil {
			return err
		}
		return fn(tx)
	})
	if err != nil && ctx.Err() == nil && isQueryCanceled(err) {
		return ErrMailSearchTooBroad
	}
	return err
}

// isQueryCanceled reports PostgreSQL's query_canceled, which a statement timeout raises.
func isQueryCanceled(err error) bool {
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "57014"
}

// threadRow is one thread of a page: its newest message in the view.
type threadRow struct {
	ThreadID uint
	ID       uint
	Total    int64
}

// threadStats is one thread of a page summarized over the view.
type threadStats struct {
	ThreadID     uint
	MessageCount int
	Unread       bool
	Starred      bool
	IDs          string
}

func (r *repository) ListThreads(
	ctx context.Context,
	mailboxID, membershipID uint,
	filter domain.MailMessageFilter,
) ([]domain.MailThreadSummary, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	if filter.Placement == "" || filter.Placement == domain.MailPlacementTrash {
		return nil, 0, fmt.Errorf("%w: threads are listed per placement view", domain.ErrInvalidInput)
	}
	// Only the placement view groups: placement, folder, and direction.
	view := domain.MailMessageFilter{Placement: filter.Placement, FolderID: filter.FolderID, Direction: filter.Direction}
	offset, limit := pageOf(filter)
	db := r.conn(ctx)

	// The newest message of every thread in the view, read in (thread_id, id DESC) order from the
	// threads index, then the page of them newest first. The window counts every thread.
	latest := messageView(db, mailboxID, membershipID, view).
		Select("DISTINCT ON (mail_messages.thread_id) mail_messages.thread_id, mail_messages.id").
		Order("mail_messages.thread_id, mail_messages.id DESC")
	var rows []threadRow
	err := db.Table("(?) AS latest", latest).
		Select("latest.thread_id, latest.id, COUNT(*) OVER () AS total").
		Order("latest.id DESC").
		Offset(offset).
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		var total int64
		if offset > 0 {
			err = messageView(db, mailboxID, membershipID, view).Distinct("mail_messages.thread_id").Count(&total).Error
		}
		return []domain.MailThreadSummary{}, total, err
	}
	total := rows[0].Total
	threadIDs := make([]uint, 0, len(rows))
	latestIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		threadIDs = append(threadIDs, row.ThreadID)
		latestIDs = append(latestIDs, row.ID)
	}

	var messages []MailMessagePO
	err = db.Model(&MailMessagePO{}).Omit("text_body", "html_body").Where("id IN ?", latestIDs).Find(&messages).Error
	if err != nil {
		return nil, 0, err
	}
	byID := make(map[uint]*domain.MailMessage, len(messages))
	for i := range messages {
		byID[messages[i].ID] = messages[i].toDomain()
	}

	var stats []threadStats
	err = messageView(db, mailboxID, membershipID, view).
		Select(`mail_messages.thread_id, COUNT(*) AS message_count,
			bool_or(`+unreadByMember+`) AS unread,
			bool_or(`+starredByMember+`) AS starred,
			array_to_string((array_agg(mail_messages.id ORDER BY mail_messages.id DESC))[1:`+strconv.Itoa(domain.MaxMailThreadMessageIDs)+`], ',') AS ids`,
			map[string]any{"m": membershipID}).
		Where("mail_messages.thread_id IN ?", threadIDs).
		Group("mail_messages.thread_id").
		Scan(&stats).Error
	if err != nil {
		return nil, 0, err
	}
	byThread := make(map[uint]threadStats, len(stats))
	for _, stat := range stats {
		byThread[stat.ThreadID] = stat
	}

	var labels []struct {
		ThreadID uint
		LabelID  uint
	}
	err = messageView(db, mailboxID, membershipID, view).
		Joins("JOIN mail_message_labels ON mail_message_labels.message_id = mail_messages.id").
		Distinct("mail_messages.thread_id", "mail_message_labels.label_id").
		Where("mail_messages.thread_id IN ?", threadIDs).
		Order("mail_message_labels.label_id").
		Scan(&labels).Error
	if err != nil {
		return nil, 0, err
	}
	labelsOf := make(map[uint][]uint, len(threadIDs))
	for _, label := range labels {
		labelsOf[label.ThreadID] = append(labelsOf[label.ThreadID], label.LabelID)
	}

	summaries := make([]domain.MailThreadSummary, 0, len(rows))
	for _, row := range rows {
		message, ok := byID[row.ID]
		if !ok {
			// Deleted between the two reads; the next page load settles it.
			continue
		}
		stat := byThread[row.ThreadID]
		ids, err := parseIDList(stat.IDs)
		if err != nil {
			return nil, 0, err
		}
		slices.Sort(ids)
		labelIDs := labelsOf[row.ThreadID]
		if labelIDs == nil {
			labelIDs = []uint{}
		}
		summaries = append(summaries, domain.MailThreadSummary{
			Latest: message,
			MailThreadInfo: domain.MailThreadInfo{
				MessageCount: stat.MessageCount, MessageIDs: ids,
				Unread: stat.Unread, Starred: stat.Starred, LabelIDs: labelIDs,
			},
		})
	}
	return summaries, total, nil
}

func parseIDList(raw string) ([]uint, error) {
	if raw == "" {
		return []uint{}, nil
	}
	parts := strings.Split(raw, ",")
	ids := make([]uint, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("thread message id %q: %w", part, err)
		}
		ids = append(ids, uint(id))
	}
	return ids, nil
}

func (r *repository) ThreadMessages(ctx context.Context, mailboxID, threadID, membershipID uint) ([]*domain.MailMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailMessagePO
	err := messageView(r.conn(ctx), mailboxID, membershipID, domain.MailMessageFilter{}).
		Where("mail_messages.thread_id = ?", threadID).
		Order("mail_messages.id DESC").
		Limit(domain.MaxMailThreadMessageIDs).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	messages := make([]*domain.MailMessage, 0, len(rows))
	ids := make([]uint, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		messages = append(messages, rows[i].toDomain())
		ids = append(ids, rows[i].ID)
	}
	read, err := r.ReadMessages(ctx, membershipID, ids)
	if err != nil {
		return nil, err
	}
	starred, err := r.StarredMessages(ctx, membershipID, ids)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		message.Read = message.Direction == domain.MailDirectionOutbound || read[message.ID]
		message.Starred = starred[message.ID]
	}
	return messages, nil
}

func (r *repository) MessageCounts(ctx context.Context, mailboxID, membershipID uint) (domain.MailMessageCounts, error) {
	counts := domain.MailMessageCounts{Folders: []domain.MailFolderCount{}}
	if r.db == nil {
		return counts, domain.ErrServiceUnavailable
	}
	db := r.conn(ctx)
	var unread []struct {
		Placement string
		FolderID  *uint
		Unread    int
	}
	err := messageView(db, mailboxID, membershipID, domain.MailMessageFilter{Unread: new(true)}).
		Select("mail_messages.placement, mail_messages.folder_id, COUNT(*) AS unread").
		Group("mail_messages.placement, mail_messages.folder_id").
		Scan(&unread).Error
	if err != nil {
		return counts, err
	}
	inFolder := map[uint]int{}
	for _, row := range unread {
		switch domain.MailPlacement(row.Placement) {
		case domain.MailPlacementDefault:
			counts.DefaultInboundUnread += row.Unread
		case domain.MailPlacementArchive:
			counts.ArchiveUnread += row.Unread
		case domain.MailPlacementFolder:
			if row.FolderID != nil {
				inFolder[*row.FolderID] += row.Unread
			}
		case domain.MailPlacementTrash:
			// The view never selects the trash; it is never counted.
		}
	}
	folders, err := r.ListFolders(ctx, mailboxID)
	if err != nil {
		return counts, err
	}
	for _, folder := range folders {
		counts.Folders = append(counts.Folders, domain.MailFolderCount{FolderID: folder.ID, Unread: inFolder[folder.ID]})
	}
	var starred int64
	err = messageView(db, mailboxID, membershipID, domain.MailMessageFilter{Starred: new(true)}).Count(&starred).Error
	if err != nil {
		return counts, err
	}
	counts.Starred = int(starred)
	return counts, nil
}
