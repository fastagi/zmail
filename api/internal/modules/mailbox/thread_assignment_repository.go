package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailThreadAssignmentPO is who works one conversation of a shared mailbox.
type MailThreadAssignmentPO struct {
	MailboxID       uint   `gorm:"primaryKey;autoIncrement:false"`
	ThreadID        uint   `gorm:"primaryKey;autoIncrement:false"`
	AssigneeUserID  *uint  `gorm:"index"`
	Status          string `gorm:"type:text;not null"`
	UpdatedByUserID *uint  `gorm:"index"`
	UpdatedAt       time.Time
}

func (MailThreadAssignmentPO) TableName() string { return "mail_thread_assignments" }

// FindMailThreadAssignment reads a thread's assignment; one never set is open and unassigned.
func (r *repository) FindMailThreadAssignment(ctx context.Context, mailboxID, threadID uint) (domain.MailThreadAssignment, error) {
	assignment := domain.MailThreadAssignment{MailboxID: mailboxID, ThreadID: threadID, Status: domain.MailThreadOpen}
	if r.db == nil {
		return assignment, domain.ErrServiceUnavailable
	}
	var po MailThreadAssignmentPO
	err := r.conn(ctx).Where("mailbox_id = ? AND thread_id = ?", mailboxID, threadID).Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return assignment, nil
	}
	if err != nil {
		return assignment, err
	}
	return po.toDomain(), nil
}

// SaveMailThreadAssignment replaces a thread's assignment.
func (r *repository) SaveMailThreadAssignment(
	ctx context.Context, assignment domain.MailThreadAssignment, now time.Time,
) (domain.MailThreadAssignment, error) {
	if r.db == nil {
		return assignment, domain.ErrServiceUnavailable
	}
	po := MailThreadAssignmentPO{
		MailboxID: assignment.MailboxID, ThreadID: assignment.ThreadID, AssigneeUserID: assignment.AssigneeUserID,
		Status: string(assignment.Status), UpdatedByUserID: assignment.UpdatedBy, UpdatedAt: now,
	}
	err := r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "mailbox_id"}, {Name: "thread_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"assignee_user_id", "status", "updated_by_user_id", "updated_at"}),
	}).Create(&po).Error
	if err != nil {
		return assignment, err
	}
	return po.toDomain(), nil
}

func (po MailThreadAssignmentPO) toDomain() domain.MailThreadAssignment {
	updated := po.UpdatedAt.UTC()
	return domain.MailThreadAssignment{
		MailboxID: po.MailboxID, ThreadID: po.ThreadID, AssigneeUserID: po.AssigneeUserID,
		Status: domain.MailThreadStatus(po.Status), UpdatedBy: po.UpdatedByUserID, UpdatedAt: &updated,
	}
}
