package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/agentgrant"
)

var _ domain.MailAgentRepository = (*repository)(nil)

func (r *repository) agentGrantRepo() *agentgrant.Repository {
	return agentgrant.NewRepository(r.db, ErrMailboxNotFound)
}

func (r *repository) FindAgentKey(ctx context.Context, keyID uint) (*domain.APIKey, error) {
	return r.agentGrantRepo().FindAgentKey(ctx, keyID)
}

func (r *repository) CreateAgentGrant(ctx context.Context, grant *domain.MailAgentGrant, maxLive int) error {
	return r.agentGrantRepo().CreateAgentGrant(ctx, grant, maxLive)
}

func (r *repository) FindAgentGrant(ctx context.Context, mailboxID, grantID uint, sealedBefore *time.Time) (*domain.MailAgentGrant, error) {
	return r.agentGrantRepo().FindAgentGrant(ctx, mailboxID, grantID, sealedBefore)
}

func (r *repository) ListAgentGrants(ctx context.Context, mailboxID uint, sealedBefore *time.Time, page, pageSize int) ([]*domain.MailAgentGrant, int64, error) {
	return r.agentGrantRepo().ListAgentGrants(ctx, mailboxID, sealedBefore, page, pageSize)
}

func (r *repository) ListLiveAgentGrantsOfMailbox(ctx context.Context, mailboxID uint, now time.Time) ([]*domain.MailAgentGrant, error) {
	return r.agentGrantRepo().ListLiveAgentGrantsOfMailbox(ctx, mailboxID, now)
}

func (r *repository) ListLiveAgentGrantsOfKey(ctx context.Context, organizationID, keyID uint, now time.Time) ([]*domain.MailAgentGrant, error) {
	return r.agentGrantRepo().ListLiveAgentGrantsOfKey(ctx, organizationID, keyID, now)
}

func (r *repository) RevokeAgentGrants(ctx context.Context, revocation domain.MailAgentGrantRevocation) ([]*domain.MailAgentGrant, error) {
	return r.agentGrantRepo().RevokeAgentGrants(ctx, revocation)
}

func (r *repository) AppendAgentEvents(ctx context.Context, organizationID uint, events []*domain.MailAgentEvent) error {
	return r.agentGrantRepo().AppendAgentEvents(ctx, organizationID, events)
}

func (r *repository) ListAgentEvents(ctx context.Context, grantIDs []uint, after uint, limit int) ([]*domain.MailAgentEvent, error) {
	return r.agentGrantRepo().ListAgentEvents(ctx, grantIDs, after, limit)
}

func (r *repository) ListAgentMessageEvents(ctx context.Context, grantIDs []uint, messageID uint) ([]*domain.MailAgentEvent, error) {
	return r.agentGrantRepo().ListAgentMessageEvents(ctx, grantIDs, messageID)
}

func (r *repository) AddAgentAudit(ctx context.Context, entry *domain.MailAgentAuditEntry) error {
	return r.agentGrantRepo().AddAgentAudit(ctx, entry)
}

func (r *repository) ListAgentAudit(ctx context.Context, grantID uint, sealedBefore *time.Time, limit int) ([]*domain.MailAgentAuditEntry, error) {
	return r.agentGrantRepo().ListAgentAudit(ctx, grantID, sealedBefore, limit)
}

func (r *repository) SaveAgentDraft(ctx context.Context, draft *domain.MailAgentDraft) (bool, error) {
	return r.agentGrantRepo().SaveAgentDraft(ctx, draft)
}

func (r *repository) ListAgentDrafts(ctx context.Context, mailboxID uint, page, pageSize int) ([]*domain.MailAgentDraft, int64, error) {
	return r.agentGrantRepo().ListAgentDrafts(ctx, mailboxID, page, pageSize)
}

func (r *repository) DeleteAgentDrafts(ctx context.Context, mailboxID uint) error {
	return r.agentGrantRepo().DeleteAgentDrafts(ctx, mailboxID)
}

func (r *repository) PruneAgentAudit(ctx context.Context, before time.Time, limit int) (int64, error) {
	return r.agentGrantRepo().PruneAgentAudit(ctx, before, limit)
}

func (r *repository) PruneAgentEvents(ctx context.Context, before time.Time, limit int) (int64, error) {
	return r.agentGrantRepo().PruneAgentEvents(ctx, before, limit)
}
