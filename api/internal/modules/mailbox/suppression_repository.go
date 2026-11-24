package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	suppressionDefaultPerPage = 50
	suppressionMaxPerPage     = 100
)

func (r *repository) IsSuppressed(ctx context.Context, organizationID uint, email string) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	var found []uint
	err := r.conn(ctx).Model(&MailSuppressionPO{}).
		Where("organization_id = ? AND email = ?", organizationID, normalizeEmail(email)).
		Limit(1).Pluck("id", &found).Error
	return len(found) > 0, err
}

// AddSuppression keeps the first entry of an address: its reason and source say why it was
// suppressed first.
func (r *repository) AddSuppression(ctx context.Context, suppression domain.MailSuppression) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := &MailSuppressionPO{
		OrganizationID: suppression.OrganizationID,
		Email:          normalizeEmail(suppression.Email),
		Reason:         suppression.Reason,
		Source:         suppression.Source,
		CreatedAt:      suppression.CreatedAt,
	}
	if po.CreatedAt.IsZero() {
		po.CreatedAt = time.Now().UTC()
	}
	return r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "organization_id"}, {Name: "email"}},
		DoNothing: true,
	}).Create(po).Error
}

// ListSuppressions pages by (created_at, id), newest first. The cursor is the id of the last entry
// shown; when that entry has since been removed, the page continues below its id.
func (r *repository) ListSuppressions(
	ctx context.Context,
	organizationID uint,
	filter domain.MailSuppressionFilter,
) ([]domain.MailSuppression, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	perPage := filter.PerPage
	if perPage < 1 {
		perPage = suppressionDefaultPerPage
	}
	perPage = min(perPage, suppressionMaxPerPage)

	db := r.conn(ctx)
	query := db.Where("organization_id = ?", organizationID)
	if email := normalizeEmail(filter.Email); email != "" {
		query = query.Where("email = ?", email)
	}
	if filter.Reason != "" {
		query = query.Where("reason = ?", filter.Reason)
	}
	if filter.Cursor != 0 {
		var cursor MailSuppressionPO
		err := db.Select("id", "created_at").Where("organization_id = ? AND id = ?", organizationID, filter.Cursor).First(&cursor).Error
		switch {
		case err == nil:
			query = query.Where("(created_at, id) < (?, ?)", cursor.CreatedAt, cursor.ID)
		case errors.Is(err, gorm.ErrRecordNotFound):
			query = query.Where("id < ?", filter.Cursor)
		default:
			return nil, err
		}
	}
	var rows []MailSuppressionPO
	if err := query.Order("created_at DESC, id DESC").Limit(perPage + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	suppressions := make([]domain.MailSuppression, 0, len(rows))
	for i := range rows {
		suppressions = append(suppressions, rows[i].ToDomain())
	}
	return suppressions, nil
}

func (r *repository) DeleteSuppression(ctx context.Context, organizationID, id uint) (*domain.MailSuppression, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailSuppressionPO
	if err := r.conn(ctx).Raw(`DELETE FROM mail_suppressions WHERE id = ? AND organization_id = ?
		RETURNING id, organization_id, email, reason, source, created_at`, id, organizationID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	suppression := rows[0].ToDomain()
	return &suppression, nil
}
