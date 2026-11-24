package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/broadcast"
)

var _ domain.MailTemplateRepository = (*repository)(nil)

func (r *repository) templateRepo() *broadcast.TemplateRepository {
	return broadcast.NewTemplateRepository(r.db, broadcast.TemplateErrors{
		LimitReached: ErrMailTemplateLimitReached,
		NotFound:     ErrMailTemplateNotFound,
		KeyTaken:     ErrMailTemplateKeyTaken,
		NameTaken:    ErrMailTemplateNameTaken,
	})
}

func (r *repository) CreateTemplate(ctx context.Context, template *domain.MailTemplate, limit int) error {
	return r.templateRepo().CreateTemplate(ctx, template, limit)
}

func (r *repository) ListTemplates(ctx context.Context, organizationID uint, kind domain.MailTemplateKind) ([]*domain.MailTemplate, error) {
	return r.templateRepo().ListTemplates(ctx, organizationID, kind)
}

func (r *repository) FindTemplate(ctx context.Context, organizationID, id uint) (*domain.MailTemplate, error) {
	return r.templateRepo().FindTemplate(ctx, organizationID, id)
}

func (r *repository) FindTemplateByKey(ctx context.Context, organizationID uint, key string) (*domain.MailTemplate, error) {
	return r.templateRepo().FindTemplateByKey(ctx, organizationID, key)
}

func (r *repository) UpdateTemplate(ctx context.Context, template *domain.MailTemplate) error {
	return r.templateRepo().UpdateTemplate(ctx, template)
}

func (r *repository) DeleteTemplate(ctx context.Context, organizationID, id uint) error {
	return r.templateRepo().DeleteTemplate(ctx, organizationID, id)
}
