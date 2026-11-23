package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// sendLaneWindowHours is the length of the rolling window every lane is counted over.
const sendLaneWindowHours = 24

// SendLaneUsage is how full one send lane of an organization is.
type SendLaneUsage struct {
	Lane  domain.SendLane
	Limit int
	// Used counts the lane's provider calls that started in the window, failed ones included.
	Used int64
	// Waiting counts the messages waiting for room in the transactional or personal lane, and for
	// the broadcast lane the pending recipients of broadcasts paused because the lane was full.
	Waiting int64
}

// ListSendLanes counts the usage of every lane over the rolling window ending at computedAt, in
// the order transactional, broadcast, personal. Owners, admins, and developers may read usage; the
// numbers are the whole organization's.
func (s *service) ListSendLanes(ctx context.Context, viewer domain.OrganizationContext) ([]SendLaneUsage, time.Time, error) {
	if !canUseDeveloperTools(viewer) {
		return nil, time.Time{}, domain.ErrPermissionDenied
	}
	computedAt := s.now().UTC()
	since := computedAt.Add(-sendLimitWindow)
	organizationID := viewer.OrganizationID

	transactionalLimit, err := s.organizationLaneLimit(ctx, organizationID, domain.SendLaneTransactional)
	if err != nil {
		return nil, time.Time{}, err
	}
	transactional := SendLaneUsage{Lane: domain.SendLaneTransactional, Limit: transactionalLimit}
	if s.transactional != nil {
		if transactional.Used, err = s.transactional.CountTransactionalAttemptsSince(ctx, organizationID, since); err != nil {
			return nil, time.Time{}, err
		}
		if transactional.Waiting, err = s.transactional.CountTransactionalWaiting(ctx, organizationID); err != nil {
			return nil, time.Time{}, err
		}
	}

	broadcastLimit, err := s.organizationLaneLimit(ctx, organizationID, domain.SendLaneBroadcast)
	if err != nil {
		return nil, time.Time{}, err
	}
	broadcast := SendLaneUsage{Lane: domain.SendLaneBroadcast, Limit: broadcastLimit}
	if broadcast.Used, err = s.repo.CountOutboundSince(ctx, organizationID, domain.SendLaneBroadcast, since); err != nil {
		return nil, time.Time{}, err
	}
	if s.broadcasts != nil {
		if broadcast.Waiting, err = s.broadcasts.CountWaitingBroadcastRecipients(ctx, organizationID, stopReasonSendLimit); err != nil {
			return nil, time.Time{}, err
		}
	}

	personalLimit, err := s.organizationLaneLimit(ctx, organizationID, domain.SendLanePersonal)
	if err != nil {
		return nil, time.Time{}, err
	}
	personal := SendLaneUsage{Lane: domain.SendLanePersonal, Limit: personalLimit}
	if personal.Used, err = s.repo.CountOutboundSince(ctx, organizationID, domain.SendLanePersonal, since); err != nil {
		return nil, time.Time{}, err
	}
	// Only personal messages wait in mail_messages: broadcasts pause instead.
	if personal.Waiting, err = s.repo.CountWaitingSends(ctx, organizationID); err != nil {
		return nil, time.Time{}, err
	}
	return []SendLaneUsage{transactional, broadcast, personal}, computedAt, nil
}
