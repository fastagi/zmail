package mailbox

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailThreadNotePO is one internal note on a shared mailbox's conversation.
type MailThreadNotePO struct {
	ID           uint   `gorm:"primaryKey"`
	MailboxID    uint   `gorm:"not null;index:idx_mail_thread_notes_thread,priority:1"`
	ThreadID     uint   `gorm:"not null;index:idx_mail_thread_notes_thread,priority:2"`
	AuthorUserID *uint  `gorm:"index"`
	Body         string `gorm:"type:text;not null"`
	CreatedAt    time.Time
}

func (MailThreadNotePO) TableName() string { return "mail_thread_notes" }

// ListMailThreadNotes reads a thread's notes, oldest first.
func (r *repository) ListMailThreadNotes(ctx context.Context, mailboxID, threadID uint) ([]domain.MailThreadNote, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailThreadNotePO
	if err := r.conn(ctx).Where("mailbox_id = ? AND thread_id = ?", mailboxID, threadID).
		Order("id ASC").Limit(domain.MailThreadNoteLimit).Find(&rows).Error; err != nil {
		return nil, err
	}
	notes := make([]domain.MailThreadNote, 0, len(rows))
	for _, row := range rows {
		notes = append(notes, row.toDomain())
	}
	return notes, nil
}

// CreateMailThreadNote stores a note. A transaction-scoped advisory lock per thread keeps two
// concurrent writers from both passing the count.
func (r *repository) CreateMailThreadNote(ctx context.Context, note domain.MailThreadNote, limit int) (domain.MailThreadNote, error) {
	if r.db == nil {
		return domain.MailThreadNote{}, domain.ErrServiceUnavailable
	}
	po := MailThreadNotePO{
		MailboxID: note.MailboxID, ThreadID: note.ThreadID, AuthorUserID: note.AuthorUserID,
		Body: note.Body, CreatedAt: note.CreatedAt,
	}
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		key := fmt.Sprintf("mail_thread_notes:%d:%d", note.MailboxID, note.ThreadID)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&MailThreadNotePO{}).
			Where("mailbox_id = ? AND thread_id = ?", note.MailboxID, note.ThreadID).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(limit) {
			return domain.ErrMailThreadNoteLimit
		}
		return tx.Create(&po).Error
	})
	if err != nil {
		return domain.MailThreadNote{}, err
	}
	return po.toDomain(), nil
}

// DeleteMailThreadNote removes an author's own note from a thread.
func (r *repository) DeleteMailThreadNote(ctx context.Context, mailboxID, threadID, noteID, authorUserID uint) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).
		Where("id = ? AND mailbox_id = ? AND thread_id = ? AND author_user_id = ?", noteID, mailboxID, threadID, authorUserID).
		Delete(&MailThreadNotePO{})
	return result.RowsAffected > 0, result.Error
}

func (po MailThreadNotePO) toDomain() domain.MailThreadNote {
	return domain.MailThreadNote{
		ID: po.ID, MailboxID: po.MailboxID, ThreadID: po.ThreadID, AuthorUserID: po.AuthorUserID,
		Body: po.Body, CreatedAt: po.CreatedAt.UTC(),
	}
}
