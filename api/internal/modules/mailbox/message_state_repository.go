package mailbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// LockMessages locks the named unsealed messages of the mailbox FOR UPDATE, so a concurrent
// change or delete of the same messages waits for the caller's transaction.
func (r *repository) LockMessages(ctx context.Context, mailboxID uint, ids []uint) ([]*domain.MailMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if len(ids) == 0 {
		return []*domain.MailMessage{}, nil
	}
	var rows []MailMessagePO
	err := r.conn(ctx).Omit("text_body", "html_body").
		Where("mailbox_id = ? AND id IN ?", mailboxID, ids).Where(unsealedMessage).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Order("id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	messages := make([]*domain.MailMessage, 0, len(rows))
	for i := range rows {
		messages = append(messages, rows[i].toDomain())
	}
	return messages, nil
}

// LockFolder holds a FOR SHARE lock on the folder; DeleteFolder locks it FOR UPDATE, so a folder
// cannot be deleted while messages move into it.
func (r *repository) LockFolder(ctx context.Context, mailboxID, id uint) (*domain.MailFolder, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailFolderPO
	err := r.conn(ctx).Where("id = ? AND mailbox_id = ?", id, mailboxID).
		Clauses(clause.Locking{Strength: "SHARE"}).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMailFolderNotFound
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

// LockLabels holds a FOR SHARE lock on the labels, so none is deleted while it is put on messages.
func (r *repository) LockLabels(ctx context.Context, mailboxID uint, ids []uint) ([]*domain.MailLabel, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if len(ids) == 0 {
		return []*domain.MailLabel{}, nil
	}
	var rows []MailLabelPO
	err := r.conn(ctx).Where("mailbox_id = ? AND id IN ?", mailboxID, ids).
		Clauses(clause.Locking{Strength: "SHARE"}).Order("id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	labels := make([]*domain.MailLabel, 0, len(rows))
	for i := range rows {
		labels = append(labels, rows[i].toDomain())
	}
	return labels, nil
}

// ApplyMessageChange writes one change to every given message in one transaction. The caller has
// checked that the messages, the folder, and the labels belong to one mailbox.
func (r *repository) ApplyMessageChange(
	ctx context.Context,
	ids []uint,
	membershipID uint,
	change domain.MailMessageChange,
	at time.Time,
) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	if len(ids) == 0 {
		return nil
	}
	if (change.Read != nil || change.Starred != nil) && membershipID == 0 {
		return fmt.Errorf("%w: read and star state need a membership", domain.ErrInvalidInput)
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if change.Read != nil {
			if err := setReads(tx, ids, membershipID, *change.Read, at); err != nil {
				return err
			}
		}
		if change.Starred != nil {
			if err := setStars(tx, ids, membershipID, *change.Starred, at); err != nil {
				return err
			}
		}
		if change.Placement != nil {
			if err := setPlacement(tx, ids, *change.Placement, change.FolderID, at); err != nil {
				return err
			}
		}
		return setLabels(tx, ids, change)
	})
}

// setReads stores or deletes the membership's read rows; outbound messages are always read and
// get none.
func setReads(tx *gorm.DB, ids []uint, membershipID uint, read bool, at time.Time) error {
	if !read {
		return tx.Where("membership_id = ? AND message_id IN ?", membershipID, ids).Delete(&MailMessageReadPO{}).Error
	}
	return tx.Exec(`INSERT INTO mail_message_reads (membership_id, message_id, read_at)
		SELECT ?, id, ? FROM mail_messages WHERE id IN ? AND direction = ?
		ON CONFLICT DO NOTHING`, membershipID, at, ids, string(domain.MailDirectionInbound)).Error
}

func setStars(tx *gorm.DB, ids []uint, membershipID uint, starred bool, at time.Time) error {
	if !starred {
		return tx.Where("membership_id = ? AND message_id IN ?", membershipID, ids).Delete(&MailMessageStarPO{}).Error
	}
	return tx.Exec(`INSERT INTO mail_message_stars (membership_id, message_id, starred_at)
		SELECT ?, id, ? FROM mail_messages WHERE id IN ?
		ON CONFLICT DO NOTHING`, membershipID, at, ids).Error
}

// setPlacement moves the messages. Only the folder placement keeps a folder_id. Moving to the
// trash keeps the time of a message already there, and cancels a send still waiting for the send
// limit exactly as deleting it used to: the message fails with send.canceled and is never sent,
// not even when it is restored later. A send whose provider call has started is left to record
// its real outcome.
func setPlacement(tx *gorm.DB, ids []uint, placement domain.MailPlacement, folderID *uint, at time.Time) error {
	updates := map[string]any{"placement": string(placement), "folder_id": nil, "trashed_at": nil, "updated_at": at}
	switch placement {
	case domain.MailPlacementDefault, domain.MailPlacementArchive:
	case domain.MailPlacementFolder:
		updates["folder_id"] = folderID
	case domain.MailPlacementTrash:
		updates["trashed_at"] = gorm.Expr("CASE WHEN placement = ? AND trashed_at IS NOT NULL THEN trashed_at ELSE ? END",
			string(domain.MailPlacementTrash), at)
	}
	if err := tx.Model(&MailMessagePO{}).Where("id IN ?", ids).Updates(updates).Error; err != nil {
		return err
	}
	if placement != domain.MailPlacementTrash {
		return nil
	}
	return tx.Model(&MailMessagePO{}).Where("id IN ? AND queued_reason IS NOT NULL", ids).Updates(map[string]any{
		"status":           string(domain.MailStatusFailed),
		"failure_code":     failureSendCanceled,
		"queued_reason":    nil,
		"send_lease_until": nil,
		"updated_at":       at,
	}).Error
}

func setLabels(tx *gorm.DB, ids []uint, change domain.MailMessageChange) error {
	add := change.AddLabelIDs
	if change.SetLabelIDs != nil {
		set := *change.SetLabelIDs
		remove := tx.Where("message_id IN ?", ids)
		if len(set) > 0 {
			remove = remove.Where("label_id NOT IN ?", set)
		}
		if err := remove.Delete(&MailMessageLabelPO{}).Error; err != nil {
			return err
		}
		add = set
	}
	if len(change.RemoveLabelIDs) > 0 {
		if err := tx.Where("message_id IN ? AND label_id IN ?", ids, change.RemoveLabelIDs).
			Delete(&MailMessageLabelPO{}).Error; err != nil {
			return err
		}
	}
	if len(add) == 0 {
		return nil
	}
	return tx.Exec(`INSERT INTO mail_message_labels (message_id, label_id)
		SELECT message.id, label.id FROM mail_messages AS message CROSS JOIN mail_labels AS label
		WHERE message.id IN ? AND label.id IN ?
		ON CONFLICT DO NOTHING`, ids, add).Error
}

func (r *repository) StarredMessages(ctx context.Context, membershipID uint, messageIDs []uint) (map[uint]bool, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	starred := make(map[uint]bool, len(messageIDs))
	if membershipID == 0 || len(messageIDs) == 0 {
		return starred, nil
	}
	var ids []uint
	err := r.conn(ctx).Model(&MailMessageStarPO{}).
		Where("membership_id = ? AND message_id IN ?", membershipID, messageIDs).
		Pluck("message_id", &ids).Error
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		starred[id] = true
	}
	return starred, nil
}

func (r *repository) MessageLabelIDs(ctx context.Context, messageIDs []uint) (map[uint][]uint, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	labels := make(map[uint][]uint, len(messageIDs))
	if len(messageIDs) == 0 {
		return labels, nil
	}
	var rows []MailMessageLabelPO
	err := r.conn(ctx).Where("message_id IN ?", messageIDs).Order("message_id, label_id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		labels[row.MessageID] = append(labels[row.MessageID], row.LabelID)
	}
	return labels, nil
}

func (r *repository) DeleteTrashedMessages(ctx context.Context, mailboxID uint, ids []uint) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if ids != nil && len(ids) == 0 {
		return 0, nil
	}
	var deleted int
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&MailMessagePO{}).
			Where("mailbox_id = ? AND placement = ?", mailboxID, string(domain.MailPlacementTrash)).
			Where(unsealedMessage)
		if ids != nil {
			query = query.Where("id IN ?", ids)
		}
		var found []uint
		if err := query.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Pluck("id", &found).Error; err != nil {
			return err
		}
		var err error
		deleted, err = deleteMessages(tx, found)
		return err
	})
	return deleted, err
}

func (r *repository) PurgeExpiredTrash(ctx context.Context, before time.Time, limit int) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return 0, nil
	}
	var deleted int
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var found []uint
		// Workers running at once skip each other's rows instead of waiting for them.
		// Sealed handover history is never purged: only the sealed-history delete removes it.
		if err := tx.Raw(`SELECT id FROM mail_messages
			WHERE placement = ? AND trashed_at <= ? AND `+unsealedMessage+`
			ORDER BY trashed_at, id
			LIMIT ?
			FOR UPDATE SKIP LOCKED`, string(domain.MailPlacementTrash), before, limit).Scan(&found).Error; err != nil {
			return err
		}
		var err error
		deleted, err = deleteMessages(tx, found)
		return err
	})
	return deleted, err
}

// deleteMessages deletes the given messages for good with their files, events, and read, star,
// and label rows. Broadcast recipients that point at one keep their row without the message.
func deleteMessages(tx *gorm.DB, ids []uint) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if err := tx.Model(&MailBroadcastRecipientPO{}).Where("message_id IN ?", ids).
		Update("message_id", nil).Error; err != nil {
		return 0, err
	}
	for _, dependant := range []any{&MailAttachmentPO{}, &MailMessageEventPO{}, &MailMessageReadPO{}, &MailMessageStarPO{}, &MailMessageLabelPO{}} {
		if err := tx.Where("message_id IN ?", ids).Delete(dependant).Error; err != nil {
			return 0, err
		}
	}
	result := tx.Where("id IN ?", ids).Delete(&MailMessagePO{})
	return int(result.RowsAffected), result.Error
}
