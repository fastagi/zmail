package mailbox

import (
	"context"
	"errors"
	"strings"

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
		if err := templateKeyFree(tx, template); err != nil {
			return err
		}
		po := newMailTemplatePO(template)
		if err := tx.Create(po).Error; err != nil {
			return templateUniqueViolation(err)
		}
		template.ID, template.CreatedAt, template.UpdatedAt = po.ID, po.CreatedAt, po.UpdatedAt
		return nil
	})
}

func (r *repository) ListTemplates(ctx context.Context, organizationID uint, kind domain.MailTemplateKind) ([]*domain.MailTemplate, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailTemplatePO
	query := r.conn(ctx).Where("organization_id = ?", organizationID)
	if kind != "" {
		query = query.Where("kind = ?", string(kind))
	}
	err := query.Order("name ASC, id ASC").Find(&rows).Error
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

// FindTemplateByKey finds a transactional template by its key within an organization.
func (r *repository) FindTemplateByKey(ctx context.Context, organizationID uint, key string) (*domain.MailTemplate, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailTemplatePO
	if err := r.conn(ctx).Where("organization_id = ? AND template_key = ?", organizationID, key).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailTemplateNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

// UpdateTemplate replaces a template's content. The kind is never written: it is fixed when the
// template is created.
func (r *repository) UpdateTemplate(ctx context.Context, template *domain.MailTemplate) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := newMailTemplatePO(template)
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// The same lock as creates, so the key check below holds under concurrency.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mail_template_owner'), ?)`, int32(template.OrganizationID)).Error; err != nil { //nolint:gosec // lock key only needs to be stable per organization
			return err
		}
		if err := templateKeyFree(tx, template); err != nil {
			return err
		}
		result := tx.Model(&MailTemplatePO{}).
			Where("id = ? AND organization_id = ?", template.ID, template.OrganizationID).
			Updates(map[string]any{
				"name":                 po.Name,
				"subject":              po.Subject,
				"html_body":            po.HTMLBody,
				"text_body":            po.TextBody,
				"sample_variables":     po.SampleVariables,
				"design":               po.Design,
				"template_key":         po.TemplateKey,
				"from_address":         po.FromAddress,
				"from_name":            po.FromName,
				"variable_definitions": po.VariableDefinitions,
				"updated_at":           gorm.Expr("now()"),
			})
		if result.Error != nil {
			return templateUniqueViolation(result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrMailTemplateNotFound
		}
		return nil
	})
	if err != nil {
		return err
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

// templateKeyFree reports ErrMailTemplateKeyTaken when another template of the organization has
// the template's key. Callers hold the organization's template lock, so the answer stays true
// until they commit; the unique index is the backstop.
func templateKeyFree(tx *gorm.DB, template *domain.MailTemplate) error {
	if template.Key == "" {
		return nil
	}
	var taken int64
	if err := tx.Model(&MailTemplatePO{}).
		Where("organization_id = ? AND template_key = ? AND id <> ?", template.OrganizationID, template.Key, template.ID).
		Count(&taken).Error; err != nil {
		return err
	}
	if taken > 0 {
		return ErrMailTemplateKeyTaken
	}
	return nil
}

// templateUniqueViolation names the unique rule a template write broke: the key index, or else
// the name.
func templateUniqueViolation(err error) error {
	if !isUniqueViolation(err) {
		return err
	}
	if strings.Contains(err.Error(), templateKeyIndex) {
		return ErrMailTemplateKeyTaken
	}
	return ErrMailTemplateNameTaken
}

// templateKeyIndex is the unique index on (organization_id, template_key).
const templateKeyIndex = "mail_templates_key_idx"
