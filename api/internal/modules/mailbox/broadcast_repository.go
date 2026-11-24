package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/broadcast"
)

var _ domain.MailBroadcastRepository = (*repository)(nil)

func (r *repository) broadcastRepo() *broadcast.BroadcastRepository {
	return broadcast.NewBroadcastRepository(r.db, broadcast.BroadcastErrors{
		NotFound:        ErrMailBroadcastNotFound,
		NotEditable:     ErrMailBroadcastNotEditable,
		NoRecipients:    ErrMailBroadcastNoRecipients,
		ContactNotFound: ErrMailContactNotFound,
	})
}

func (r *repository) ListBroadcasts(ctx context.Context, organizationID uint, mailboxIDs []uint, page, perPage int) ([]*domain.MailBroadcast, int64, error) {
	return r.broadcastRepo().ListBroadcasts(ctx, organizationID, mailboxIDs, page, perPage)
}

func (r *repository) FindBroadcast(ctx context.Context, organizationID, id uint) (*domain.MailBroadcast, error) {
	return r.broadcastRepo().FindBroadcast(ctx, organizationID, id)
}

func (r *repository) FindBroadcastForWorker(ctx context.Context, id uint) (*domain.MailBroadcast, error) {
	return r.broadcastRepo().FindBroadcastForWorker(ctx, id)
}

func (r *repository) CreateBroadcast(ctx context.Context, mailBroadcast *domain.MailBroadcast) error {
	return r.broadcastRepo().CreateBroadcast(ctx, mailBroadcast)
}

func (r *repository) UpdateDraft(ctx context.Context, mailBroadcast *domain.MailBroadcast) error {
	return r.broadcastRepo().UpdateDraft(ctx, mailBroadcast)
}

func (r *repository) DeleteBroadcast(ctx context.Context, organizationID, id uint) error {
	return r.broadcastRepo().DeleteBroadcast(ctx, organizationID, id)
}

func (r *repository) StartBroadcast(ctx context.Context, mailBroadcast *domain.MailBroadcast) (int, error) {
	return r.broadcastRepo().StartBroadcast(ctx, mailBroadcast)
}

func (r *repository) CancelBroadcast(ctx context.Context, organizationID, id uint) error {
	return r.broadcastRepo().CancelBroadcast(ctx, organizationID, id)
}

func (r *repository) ListRecipients(
	ctx context.Context,
	organizationID, broadcastID uint,
	status domain.MailRecipientStatus,
	page, perPage int,
) ([]*domain.MailBroadcastRecipient, int64, error) {
	return r.broadcastRepo().ListRecipients(ctx, organizationID, broadcastID, status, page, perPage)
}

func (r *repository) PromoteScheduled(ctx context.Context, now time.Time) error {
	return r.broadcastRepo().PromoteScheduled(ctx, now)
}

func (r *repository) LeasePending(ctx context.Context, now, leaseUntil time.Time, limit int) ([]*domain.MailBroadcastRecipient, error) {
	return r.broadcastRepo().LeasePending(ctx, now, leaseUntil, limit)
}

func (r *repository) FinishRecipient(ctx context.Context, id uint, status domain.MailRecipientStatus, messageID uint, failureCode string) error {
	return r.broadcastRepo().FinishRecipient(ctx, id, status, messageID, failureCode)
}

func (r *repository) ReleaseRecipient(ctx context.Context, id uint) error {
	return r.broadcastRepo().ReleaseRecipient(ctx, id)
}

func (r *repository) SetStopReason(ctx context.Context, id uint, reason string) error {
	return r.broadcastRepo().SetStopReason(ctx, id, reason)
}

func (r *repository) CountWaitingBroadcastRecipients(ctx context.Context, organizationID uint, stopReason string) (int64, error) {
	return r.broadcastRepo().CountWaitingBroadcastRecipients(ctx, organizationID, stopReason)
}

func (r *repository) FinishBroadcasts(ctx context.Context, now time.Time, allFailedReason string) (int, error) {
	return r.broadcastRepo().FinishBroadcasts(ctx, now, allFailedReason)
}

func (r *repository) ListPropertyNames(ctx context.Context, organizationID, listID uint) ([]string, error) {
	return r.broadcastRepo().ListPropertyNames(ctx, organizationID, listID)
}

func (r *repository) PauseBroadcast(ctx context.Context, organizationID, id uint, reason string) error {
	return r.broadcastRepo().PauseBroadcast(ctx, organizationID, id, reason)
}

func (r *repository) ResumeBroadcast(ctx context.Context, organizationID, id uint, override bool, now time.Time) error {
	return r.broadcastRepo().ResumeBroadcast(ctx, organizationID, id, override, now)
}

func (r *repository) CompleteTestWaves(ctx context.Context, now time.Time) error {
	return r.broadcastRepo().CompleteTestWaves(ctx, now)
}

func (r *repository) ListActiveBroadcasts(ctx context.Context, limit int) ([]*domain.MailBroadcast, error) {
	return r.broadcastRepo().ListActiveBroadcasts(ctx, limit)
}

func (r *repository) FindContactForBroadcast(ctx context.Context, organizationID, id uint) (*domain.MailContact, error) {
	return r.broadcastRepo().FindContactForBroadcast(ctx, organizationID, id)
}

func (r *repository) SetContactStatus(ctx context.Context, organizationID, id uint, status domain.MailContactStatus, reason string) error {
	return r.broadcastRepo().SetContactStatus(ctx, organizationID, id, status, reason)
}

func (r *repository) SuppressContactForMessage(ctx context.Context, messageID uint, reason string) error {
	return r.broadcastRepo().SuppressContactForMessage(ctx, messageID, reason)
}

func (r *repository) CountRecipientsFinishedSince(ctx context.Context, broadcastID uint, since time.Time) (int64, error) {
	return r.broadcastRepo().CountRecipientsFinishedSince(ctx, broadcastID, since)
}

func (r *repository) NthBroadcastAttemptSince(ctx context.Context, organizationID uint, since time.Time, n int64) (*time.Time, error) {
	return r.broadcastRepo().NthBroadcastAttemptSince(ctx, organizationID, since, n)
}

func (r *repository) ListBroadcastNoticesDue(ctx context.Context, finishedSince time.Time, limit int) ([]*domain.MailBroadcastNoticeDue, error) {
	return r.broadcastRepo().ListBroadcastNoticesDue(ctx, finishedSince, limit)
}

func (r *repository) MarkBroadcastNoticed(ctx context.Context, id uint, from, state string, count int) (bool, error) {
	return r.broadcastRepo().MarkBroadcastNoticed(ctx, id, from, state, count)
}
