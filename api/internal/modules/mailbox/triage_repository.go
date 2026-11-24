package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/triage"
)

var _ domain.MailTriageRepository = (*repository)(nil)

type (
	MailTriagePO         = triage.MailTriagePO
	MailTriageSettingsPO = triage.MailTriageSettingsPO
)

func (r *repository) triageRepo() *triage.Repository {
	return triage.NewRepository(r.db)
}

func (r *repository) FindTriageSettings(ctx context.Context, organizationID uint) (domain.MailTriageSettings, error) {
	return r.triageRepo().FindTriageSettings(ctx, organizationID)
}

func (r *repository) SaveTriageSettings(ctx context.Context, settings domain.MailTriageSettings, now time.Time) (domain.MailTriageSettings, error) {
	return r.triageRepo().SaveTriageSettings(ctx, settings, now)
}

func (r *repository) SetMailboxTriage(ctx context.Context, mailboxID uint, enabled bool) error {
	return r.triageRepo().SetMailboxTriage(ctx, mailboxID, enabled)
}

func (r *repository) StopMailboxTriage(ctx context.Context, mailboxID uint) error {
	return r.triageRepo().StopMailboxTriage(ctx, mailboxID)
}

func (r *repository) EnqueueTriage(ctx context.Context, messageID, mailboxID uint, now time.Time) (bool, error) {
	return r.triageRepo().EnqueueTriage(ctx, messageID, mailboxID, now)
}

func (r *repository) ClaimTriage(ctx context.Context, now time.Time, limit int) ([]domain.MailTriageClaim, error) {
	return r.triageRepo().ClaimTriage(ctx, now, limit)
}

func (r *repository) ReserveTriageCall(ctx context.Context, organizationID uint, limit int, now time.Time) (bool, error) {
	return r.triageRepo().ReserveTriageCall(ctx, organizationID, limit, now)
}

func (r *repository) SaveTriage(ctx context.Context, row domain.MailTriage) error {
	return r.triageRepo().SaveTriage(ctx, row)
}

func (r *repository) OverrideTriage(ctx context.Context, override domain.MailTriageOverride) (*domain.MailTriage, error) {
	return r.triageRepo().OverrideTriage(ctx, override)
}

func (r *repository) FindTriage(ctx context.Context, messageID uint) (*domain.MailTriage, error) {
	return r.triageRepo().FindTriage(ctx, messageID)
}

func (r *repository) ListTriage(ctx context.Context, messageIDs []uint) (map[uint]*domain.MailTriage, error) {
	return r.triageRepo().ListTriage(ctx, messageIDs)
}

func (r *repository) RecentTriageExamples(ctx context.Context, mailboxID uint, limit int) ([]domain.MailTriageExample, error) {
	return r.triageRepo().RecentTriageExamples(ctx, mailboxID, limit)
}

func (r *repository) CountTriage(ctx context.Context, membershipID uint, mailboxIDs []uint) (domain.MailTriageCounts, error) {
	return r.triageRepo().CountTriage(ctx, membershipID, mailboxIDs)
}

func visibleTriageStatuses() []string {
	return []string{string(domain.MailTriageDone), string(domain.MailTriageFailed)}
}
