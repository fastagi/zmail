package mailbox

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailOrganizeRepository = (*repository)(nil)

// CreateFolder stores a folder while holding a per-mailbox lock, so two concurrent creates cannot
// both pass the folder limit. A name taken ignoring case is refused by the unique index.
func (r *repository) CreateFolder(ctx context.Context, folder *domain.MailFolder, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMailbox(tx, "mail_folder_owner", folder.MailboxID); err != nil {
			return err
		}
		var owned int64
		if err := tx.Model(&MailFolderPO{}).Where("mailbox_id = ?", folder.MailboxID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return ErrMailFolderLimitReached
		}
		po := &MailFolderPO{MailboxID: folder.MailboxID, Name: folder.Name}
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrMailFolderNameTaken
			}
			return err
		}
		*folder = *po.toDomain()
		return nil
	})
}

func (r *repository) RenameFolder(ctx context.Context, mailboxID, id uint, name string) (*domain.MailFolder, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailFolderPO{}).
		Where("id = ? AND mailbox_id = ?", id, mailboxID).
		Updates(map[string]any{"name": name, "updated_at": gorm.Expr("now()")})
	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			return nil, ErrMailFolderNameTaken
		}
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrMailFolderNotFound
	}
	var po MailFolderPO
	if err := r.conn(ctx).Where("id = ? AND mailbox_id = ?", id, mailboxID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailFolderNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

// DeleteFolder moves the folder's messages to default and deletes the folder in one transaction.
// A message of the folder already in the trash stays in the trash and only forgets the folder,
// which the schema's ON DELETE SET NULL would also do.
func (r *repository) DeleteFolder(ctx context.Context, mailboxID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var po MailFolderPO
		// FOR UPDATE waits for messages moving into the folder (LockFolder) and then sees them.
		if err := tx.Where("id = ? AND mailbox_id = ?", id, mailboxID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&po).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMailFolderNotFound
			}
			return err
		}
		if err := tx.Model(&MailMessagePO{}).
			Where("folder_id = ? AND placement = ?", id, string(domain.MailPlacementFolder)).
			Updates(map[string]any{"placement": string(domain.MailPlacementDefault), "folder_id": nil}).Error; err != nil {
			return err
		}
		if err := tx.Model(&MailMessagePO{}).Where("folder_id = ?", id).Update("folder_id", nil).Error; err != nil {
			return err
		}
		result := tx.Where("id = ? AND mailbox_id = ?", id, mailboxID).Delete(&MailFolderPO{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMailFolderNotFound
		}
		return nil
	})
}

// ListFolders lists the mailbox's folders by name ignoring case. A mailbox has at most
// domain.MaxMailFoldersPerMailbox of them.
func (r *repository) ListFolders(ctx context.Context, mailboxID uint) ([]*domain.MailFolder, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailFolderPO
	err := r.conn(ctx).Where("mailbox_id = ?", mailboxID).
		Order("lower(name) ASC, id ASC").Limit(domain.MaxMailFoldersPerMailbox).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	folders := make([]*domain.MailFolder, 0, len(rows))
	for i := range rows {
		folders = append(folders, rows[i].toDomain())
	}
	return folders, nil
}

func (r *repository) CountFolders(ctx context.Context, mailboxID uint) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&MailFolderPO{}).Where("mailbox_id = ?", mailboxID).Count(&count).Error
	return count, err
}

// CreateLabel stores a label while holding a per-mailbox lock, so two concurrent creates cannot
// both pass the label limit. A name taken ignoring case is refused by the unique index.
func (r *repository) CreateLabel(ctx context.Context, label *domain.MailLabel, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMailbox(tx, "mail_label_owner", label.MailboxID); err != nil {
			return err
		}
		var owned int64
		if err := tx.Model(&MailLabelPO{}).Where("mailbox_id = ?", label.MailboxID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return ErrMailLabelLimitReached
		}
		po := &MailLabelPO{MailboxID: label.MailboxID, Name: label.Name, Color: string(label.Color)}
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrMailLabelNameTaken
			}
			return err
		}
		*label = *po.toDomain()
		return nil
	})
}

func (r *repository) UpdateLabel(ctx context.Context, label *domain.MailLabel) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&MailLabelPO{}).
		Where("id = ? AND mailbox_id = ?", label.ID, label.MailboxID).
		Updates(map[string]any{"name": label.Name, "color": string(label.Color), "updated_at": gorm.Expr("now()")})
	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			return ErrMailLabelNameTaken
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailLabelNotFound
	}
	var po MailLabelPO
	if err := r.conn(ctx).Where("id = ? AND mailbox_id = ?", label.ID, label.MailboxID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMailLabelNotFound
		}
		return err
	}
	*label = *po.toDomain()
	return nil
}

// DeleteLabel removes the label from every message and deletes it in one transaction. The
// schema cascades too; deleting here keeps the rows from outliving the label where the foreign
// key is absent.
func (r *repository) DeleteLabel(ctx context.Context, mailboxID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND mailbox_id = ?", id, mailboxID).Delete(&MailLabelPO{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMailLabelNotFound
		}
		return tx.Where("label_id = ?", id).Delete(&MailMessageLabelPO{}).Error
	})
}

// ListLabels lists the mailbox's labels by name ignoring case. A mailbox has at most
// domain.MaxMailLabelsPerMailbox of them.
func (r *repository) ListLabels(ctx context.Context, mailboxID uint) ([]*domain.MailLabel, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailLabelPO
	err := r.conn(ctx).Where("mailbox_id = ?", mailboxID).
		Order("lower(name) ASC, id ASC").Limit(domain.MaxMailLabelsPerMailbox).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	labels := make([]*domain.MailLabel, 0, len(rows))
	for i := range rows {
		labels = append(labels, rows[i].toDomain())
	}
	return labels, nil
}

func (r *repository) CountLabels(ctx context.Context, mailboxID uint) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&MailLabelPO{}).Where("mailbox_id = ?", mailboxID).Count(&count).Error
	return count, err
}

// lockMailbox takes a transaction advisory lock for one kind of per-mailbox write.
func lockMailbox(tx *gorm.DB, kind string, mailboxID uint) error {
	return tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?), ?)`, kind, int32(mailboxID)).Error //nolint:gosec // lock key only needs to be stable per mailbox
}
