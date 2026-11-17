package mailbox

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailTemplateRepository = (*repository)(nil)

func (r *repository) CreateTemplate(ctx context.Context, template *domain.MailTemplate, limit int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// Serializes creates per organization so the limit holds under concurrency.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mail_template_owner'), ?)`, int32(template.OrganizationID)).Error; err != nil { //nolint:gosec // lock key only needs to be stable per organization
			return err
		}
		var owned int64
		if err := tx.Model(&MailTemplatePO{}).Where("organization_id = ?", template.OrganizationID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= int64(limit) {
			return ErrMailTemplateLimitReached
		}
		po := newMailTemplatePO(template)
		if err := tx.Create(po).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrMailTemplateNameTaken
			}
			return err
		}
		template.ID, template.CreatedAt, template.UpdatedAt = po.ID, po.CreatedAt, po.UpdatedAt
		return nil
	})
}

func (r *repository) ListTemplates(ctx context.Context, organizationID uint) ([]*domain.MailTemplate, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailTemplatePO
	err := r.conn(ctx).Where("organization_id = ?", organizationID).Order("name ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	templates := make([]*domain.MailTemplate, 0, len(rows))
	for i := range rows {
		templates = append(templates, rows[i].toDomain())
	}
	return templates, nil
}

func (r *repository) FindTemplate(ctx context.Context, organizationID, id uint) (*domain.MailTemplate, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailTemplatePO
	if err := r.conn(ctx).Where("id = ? AND organization_id = ?", id, organizationID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailTemplateNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) UpdateTemplate(ctx context.Context, template *domain.MailTemplate) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := newMailTemplatePO(template)
	result := r.conn(ctx).Model(&MailTemplatePO{}).
		Where("id = ? AND organization_id = ?", template.ID, template.OrganizationID).
		Updates(map[string]any{
			"name":             po.Name,
			"subject":          po.Subject,
			"html_body":        po.HTMLBody,
			"text_body":        po.TextBody,
			"sample_variables": po.SampleVariables,
			"design":           po.Design,
			"updated_at":       gorm.Expr("now()"),
		})
	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			return ErrMailTemplateNameTaken
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailTemplateNotFound
	}
	stored, err := r.FindTemplate(ctx, template.OrganizationID, template.ID)
	if err != nil {
		return err
	}
	*template = *stored
	return nil
}

func (r *repository) DeleteTemplate(ctx context.Context, organizationID, id uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Where("id = ? AND organization_id = ?", id, organizationID).Delete(&MailTemplatePO{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMailTemplateNotFound
	}
	return nil
}
