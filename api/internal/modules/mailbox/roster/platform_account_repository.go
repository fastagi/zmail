package roster

import (
	"context"
	"errors"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// platformAccountRow is an account with its registration mailbox's address; the address is nil
// without a mailbox or when the mailbox was deleted.
type platformAccountRow struct {
	PlatformAccountPO `gorm:"embedded"`
	MailboxAddress    *string
}

func (row *platformAccountRow) toDomain() *domain.PlatformAccount {
	account := row.PlatformAccountPO.ToDomain()
	if row.MailboxAddress != nil {
		account.MailboxAddress = *row.MailboxAddress
	}
	return account
}

// platformAccountQuery selects the organization's accounts with their live registration mailbox.
func platformAccountQuery(db *gorm.DB, organizationID uint) *gorm.DB {
	return db.Table("platform_accounts AS pa").
		Joins("LEFT JOIN mailboxes AS m ON m.id = pa.mailbox_id AND m.deleted_at IS NULL").
		Where("pa.organization_id = ?", organizationID)
}

const platformAccountColumns = "pa.*, m.address AS mailbox_address"

// ListPlatformAccounts lists one page of the organization's accounts by platform, then handle,
// both ignoring case, then id. Every filter set in filter must match.
func (r *Repository) ListPlatformAccounts(ctx context.Context, organizationID uint, filter domain.PlatformAccountFilter) ([]*domain.PlatformAccount, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	page, size := filter.Page, filter.PerPage
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 15
	}
	query := platformAccountQuery(r.conn(ctx), organizationID)
	if filter.Visible != nil {
		query = query.Where("(pa.mailbox_id IN ? OR pa.owner_user_id = ?)", nonEmptyIDs(filter.Visible), filter.VisibleOwner)
	}
	if filter.Query != "" {
		pattern := searchPattern(filter.Query)
		if filter.AddressMailboxIDs != nil {
			query = query.Where(`(pa.platform ILIKE ? ESCAPE '\' OR pa.handle ILIKE ? ESCAPE '\' OR (m.address ILIKE ? ESCAPE '\' AND m.id IN ?))`,
				pattern, pattern, pattern, nonEmptyIDs(filter.AddressMailboxIDs))
		} else {
			query = query.Where(`(pa.platform ILIKE ? ESCAPE '\' OR pa.handle ILIKE ? ESCAPE '\' OR m.address ILIKE ? ESCAPE '\')`,
				pattern, pattern, pattern)
		}
	}
	if filter.RoleID != 0 {
		query = query.Where("pa.role_id = ?", filter.RoleID)
	}
	if filter.MailboxID != 0 {
		query = query.Where("pa.mailbox_id = ?", filter.MailboxID)
	}
	if filter.OwnerUserID != 0 {
		query = query.Where("pa.owner_user_id = ?", filter.OwnerUserID)
	}
	if filter.Status != "" {
		query = query.Where("pa.status = ?", string(filter.Status))
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []platformAccountRow
	err := query.Select(platformAccountColumns).
		Order("lower(pa.platform) ASC, lower(pa.handle) ASC, pa.id ASC").
		Offset((page - 1) * size).Limit(size).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	accounts := make([]*domain.PlatformAccount, 0, len(rows))
	for i := range rows {
		accounts = append(accounts, rows[i].toDomain())
	}
	return accounts, total, nil
}

// FindPlatformAccount returns the organization's account; one of another organization is not found.
func (r *Repository) FindPlatformAccount(ctx context.Context, organizationID, id uint) (*domain.PlatformAccount, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return findPlatformAccount(r.conn(ctx), organizationID, id)
}

func findPlatformAccount(db *gorm.DB, organizationID, id uint) (*domain.PlatformAccount, error) {
	var rows []platformAccountRow
	if err := platformAccountQuery(db, organizationID).Where("pa.id = ?", id).
		Select(platformAccountColumns).Limit(1).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrPlatformAccountNotFound
	}
	return rows[0].toDomain(), nil
}

// CreatePlatformAccount stores the account and its created event in one transaction. A platform
// and handle taken ignoring case is refused by the unique index. The stored account, with its
// registration address, is read back into account.
func (r *Repository) CreatePlatformAccount(ctx context.Context, account *domain.PlatformAccount, actorUserID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		po := platformAccountPO(account)
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrPlatformAccountTaken
			}
			return err
		}
		if err := recordPlatformAccountEvent(tx, account.OrganizationID, po.ID, domain.PlatformAccountEventCreated, actorUserID, "", po.Status); err != nil {
			return err
		}
		stored, err := findPlatformAccount(tx, account.OrganizationID, po.ID)
		if err != nil {
			return err
		}
		*account = *stored
		return nil
	})
}

// UpdatePlatformAccount replaces every field of the account while holding its row lock, and
// records one event for each of status, owner, and mailbox that changed. The stored account is
// read back into account.
func (r *Repository) UpdatePlatformAccount(ctx context.Context, account *domain.PlatformAccount, actorUserID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var before PlatformAccountPO
		err := tx.Where("id = ? AND organization_id = ?", account.ID, account.OrganizationID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&before).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrPlatformAccountNotFound
		}
		if err != nil {
			return err
		}
		after := platformAccountPO(account)
		err = tx.Model(&PlatformAccountPO{}).Where("id = ?", account.ID).Updates(map[string]any{
			"platform": after.Platform, "handle": after.Handle, "profile_url": after.ProfileURL,
			"role_id": after.RoleID, "mailbox_id": after.MailboxID, "owner_user_id": after.OwnerUserID,
			"status": after.Status, "notes": after.Notes, "updated_at": gorm.Expr("now()"),
		}).Error
		if err != nil {
			if isUniqueViolation(err) {
				return ErrPlatformAccountTaken
			}
			return err
		}
		changes := []struct {
			kind     domain.PlatformAccountEventKind
			from, to string
		}{
			{domain.PlatformAccountEventStatusChanged, before.Status, after.Status},
			{domain.PlatformAccountEventOwnerChanged, OptionalIDText(before.OwnerUserID), OptionalIDText(after.OwnerUserID)},
			{domain.PlatformAccountEventMailboxChanged, OptionalIDText(before.MailboxID), OptionalIDText(after.MailboxID)},
		}
		for _, change := range changes {
			if change.from == change.to {
				continue
			}
			err = recordPlatformAccountEvent(tx, account.OrganizationID, account.ID, change.kind, actorUserID, change.from, change.to)
			if err != nil {
				return err
			}
		}
		stored, err := findPlatformAccount(tx, account.OrganizationID, account.ID)
		if err != nil {
			return err
		}
		*account = *stored
		return nil
	})
}

// DeletePlatformAccount records the deleted event, carrying the last status, and removes the
// account in one transaction. The event stays after the account is gone.
func (r *Repository) DeletePlatformAccount(ctx context.Context, organizationID, id, actorUserID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []PlatformAccountPO
		err := tx.Where("id = ? AND organization_id = ?", id, organizationID).
			Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find(&rows).Error
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return ErrPlatformAccountNotFound
		}
		if err := recordPlatformAccountEvent(tx, organizationID, id, domain.PlatformAccountEventDeleted, actorUserID, rows[0].Status, ""); err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM platform_accounts WHERE id = ? AND organization_id = ?`, id, organizationID).Error
	})
}

// ListPlatformAccountEvents lists one page of a live account's events, newest first. The events of
// an account of another organization, or of a deleted one, are not found.
func (r *Repository) ListPlatformAccountEvents(ctx context.Context, organizationID, accountID uint, page, size int) ([]*domain.PlatformAccountEvent, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 15
	}
	var found []uint
	if err := r.conn(ctx).Model(&PlatformAccountPO{}).Where("id = ? AND organization_id = ?", accountID, organizationID).
		Limit(1).Pluck("id", &found).Error; err != nil {
		return nil, 0, err
	}
	if len(found) == 0 {
		return nil, 0, ErrPlatformAccountNotFound
	}
	query := r.conn(ctx).Model(&PlatformAccountEventPO{}).Where("account_id = ? AND organization_id = ?", accountID, organizationID)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []PlatformAccountEventPO
	if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	events := make([]*domain.PlatformAccountEvent, 0, len(rows))
	for i := range rows {
		events = append(events, rows[i].ToDomain())
	}
	return events, total, nil
}

// HandOverPlatformAccounts moves every account of the organization owned by fromUserID to
// toUserID in one transaction, with one owner_changed event each, and returns the moved ids
// ascending. Nothing to move is not an error.
func (r *Repository) HandOverPlatformAccounts(ctx context.Context, organizationID, fromUserID, toUserID, actorUserID uint) ([]uint, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var moved []uint
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []struct{ ID uint }
		err := tx.Raw(`UPDATE platform_accounts SET owner_user_id = ?, updated_at = now()
			WHERE organization_id = ? AND owner_user_id = ? RETURNING id`, toUserID, organizationID, fromUserID).Scan(&rows).Error
		if err != nil {
			return err
		}
		moved = make([]uint, 0, len(rows))
		for _, row := range rows {
			moved = append(moved, row.ID)
		}
		slices.Sort(moved)
		for _, id := range moved {
			if err := recordPlatformAccountEvent(tx, organizationID, id, domain.PlatformAccountEventOwnerChanged, actorUserID,
				UintText(fromUserID), UintText(toUserID)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return moved, nil
}

func recordPlatformAccountEvent(tx *gorm.DB, organizationID, accountID uint, kind domain.PlatformAccountEventKind, actorUserID uint, from, to string) error {
	event := &PlatformAccountEventPO{
		OrganizationID: organizationID, AccountID: accountID, Kind: string(kind),
		FromValue: OptionalText(from), ToValue: OptionalText(to),
	}
	if actorUserID != 0 {
		event.ActorUserID = &actorUserID
	}
	return tx.Create(event).Error
}

func platformAccountPO(account *domain.PlatformAccount) *PlatformAccountPO {
	return &PlatformAccountPO{
		ID: account.ID, OrganizationID: account.OrganizationID, Platform: account.Platform, Handle: account.Handle,
		ProfileURL: OptionalText(account.ProfileURL), RoleID: account.RoleID, MailboxID: account.MailboxID,
		OwnerUserID: account.OwnerUserID, Status: string(account.Status), Notes: account.Notes,
	}
}
